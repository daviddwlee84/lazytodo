// Package store owns rebuildable observations and separate durable pins.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lazytodo/internal/config"
	"lazytodo/internal/model"
	"lazytodo/internal/search"
	_ "modernc.org/sqlite"
)

const cacheSchema = `CREATE TABLE IF NOT EXISTS sources(id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, status TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS items(ref TEXT PRIMARY KEY, source_id TEXT NOT NULL, body TEXT NOT NULL, search_text TEXT NOT NULL, state TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS items_source ON items(source_id);
CREATE TABLE IF NOT EXISTS labels(ref TEXT NOT NULL, name TEXT NOT NULL, PRIMARY KEY(ref,name));`
const pinSchema = `CREATE TABLE IF NOT EXISTS pins(ref TEXT PRIMARY KEY, body TEXT NOT NULL);`

func open(ctx context.Context, path string, write bool, schema string) (*sql.DB, error) {
	if !write {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
	}
	if write {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		f.Close()
		if err := os.Chmod(path, 0600); err != nil {
			return nil, err
		}
	}
	mode := "ro"
	if write {
		mode = "rw"
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", mode)
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	good := false
	defer func() {
		if !good {
			db.Close()
		}
	}()
	var version int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version > 1 {
		return nil, errors.New("database is from a newer lazytodo; upgrade the binary")
	}
	if version == 0 {
		var tables int
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return nil, err
		}
		if tables > 0 {
			return nil, errors.New("unrecognized unversioned database; refusing to modify it")
		}
		if !write {
			return nil, nil
		} // An interrupted first initialization is still unknown, not corrupt data.
		if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
			return nil, err
		}
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			return nil, e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, schema+"PRAGMA user_version=1;"); e != nil {
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
	}
	good = true
	return db, nil
}

type CachedSource struct {
	Fingerprint string
	Status      model.SourceStatus
}
type ReadResult struct {
	Items         []model.Item
	Sources       map[string]CachedSource
	AvailableRefs map[string]bool
}

// Read uses a single SQLite read transaction for items and source revisions.
func Read(ctx context.Context, path string, active []config.Source, q search.Query) (ReadResult, error) {
	result := ReadResult{Items: []model.Item{}, Sources: map[string]CachedSource{}, AvailableRefs: map[string]bool{}}
	db, err := open(ctx, path, false, cacheSchema)
	if err != nil || db == nil {
		return result, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id,fingerprint,status FROM sources")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id, fp, body string
		if err = rows.Scan(&id, &fp, &body); err != nil {
			rows.Close()
			return result, err
		}
		var s model.SourceStatus
		if err = json.Unmarshal([]byte(body), &s); err != nil {
			rows.Close()
			return result, err
		}
		result.Sources[id] = CachedSource{fp, s}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	var sourceParts []string
	var args []any
	for _, s := range active {
		if c, ok := result.Sources[s.ID]; ok && c.Fingerprint == s.Fingerprint() {
			sourceParts = append(sourceParts, "source_id=?")
			args = append(args, s.ID)
		}
	}
	if len(sourceParts) == 0 {
		return result, nil
	}
	where := []string{"(" + strings.Join(sourceParts, " OR ") + ")"}
	if q.Pinned != nil && *q.Pinned {
		refs, e := tx.QueryContext(ctx, "SELECT ref FROM items WHERE "+where[0], args...)
		if e != nil {
			return result, e
		}
		for refs.Next() {
			var ref string
			if e = refs.Scan(&ref); e != nil {
				refs.Close()
				return result, e
			}
			result.AvailableRefs[ref] = true
		}
		e = refs.Err()
		refs.Close()
		if e != nil {
			return result, e
		}
	}
	if q.Ref != "" {
		where = append(where, "ref=?")
		args = append(args, q.Ref)
	}
	for _, v := range q.Terms {
		where = append(where, "instr(search_text,?)>0")
		args = append(args, v)
	}
	for _, v := range q.Sources {
		where = append(where, "source_id=?")
		args = append(args, v)
	}
	for _, v := range q.States {
		where = append(where, "state=?")
		args = append(args, v)
	}
	for _, v := range q.Labels {
		where = append(where, "EXISTS (SELECT 1 FROM labels l WHERE l.ref=items.ref AND l.name=?)")
		args = append(args, v)
	}
	rows, err = tx.QueryContext(ctx, "SELECT body FROM items WHERE "+strings.Join(where, " AND "), args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return result, err
		}
		var i model.Item
		if err = json.Unmarshal([]byte(body), &i); err != nil {
			return result, err
		}
		result.Items = append(result.Items, i)
	}
	return result, rows.Err()
}

// WriteSnapshot never changes the old snapshot unless the whole transaction commits.
func WriteSnapshot(ctx context.Context, path string, s config.Source, snap model.Snapshot, status model.SourceStatus, active []config.Source) error {
	db, err := open(ctx, path, true, cacheSchema)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Retire disabled, removed or reconfigured targets only during an explicit write.
	valid := map[string]string{}
	for _, s := range active {
		valid[s.ID] = s.Fingerprint()
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,fingerprint FROM sources")
	if err != nil {
		return err
	}
	var obsolete []string
	for rows.Next() {
		var id, fp string
		if err = rows.Scan(&id, &fp); err != nil {
			rows.Close()
			return err
		}
		if valid[id] != fp {
			obsolete = append(obsolete, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range append(obsolete, s.ID) {
		if _, err = tx.ExecContext(ctx, "DELETE FROM labels WHERE ref IN (SELECT ref FROM items WHERE source_id=?)", id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM items WHERE source_id=?", id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM sources WHERE id=?", id); err != nil {
			return err
		}
	}
	for _, i := range snap.Items {
		i.SourceID = s.ID
		i.Pinned = false
		i.Available = true
		var other string
		err = tx.QueryRowContext(ctx, "SELECT source_id FROM items WHERE ref=?", i.Ref).Scan(&other)
		if err == nil {
			return fmt.Errorf("canonical identity overlaps another configured source (%s)", other)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		b, e := json.Marshal(i)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO items(ref,source_id,body,search_text,state) VALUES(?,?,?,?,?)", i.Ref, s.ID, string(b), search.Text(i), i.State); err != nil {
			return err
		}
		for _, l := range i.Labels {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO labels(ref,name) VALUES(?,?)", i.Ref, l.Name); err != nil {
				return err
			}
		}
	}
	b, _ := json.Marshal(status)
	if _, err = tx.ExecContext(ctx, "INSERT INTO sources(id,fingerprint,status) VALUES(?,?,?)", s.ID, s.Fingerprint(), string(b)); err != nil {
		return err
	}
	return tx.Commit()
}

func WriteStatus(ctx context.Context, path string, s config.Source, status model.SourceStatus) error {
	db, err := open(ctx, path, true, cacheSchema)
	if err != nil {
		return err
	}
	defer db.Close()
	b, _ := json.Marshal(status)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old string
	err = tx.QueryRowContext(ctx, "SELECT fingerprint FROM sources WHERE id=?", s.ID).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if old != "" && old != s.Fingerprint() {
		if _, err = tx.ExecContext(ctx, "DELETE FROM labels WHERE ref IN (SELECT ref FROM items WHERE source_id=?)", s.ID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM items WHERE source_id=?", s.ID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sources(id,fingerprint,status) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET fingerprint=excluded.fingerprint,status=excluded.status", s.ID, s.Fingerprint(), string(b)); err != nil {
		return err
	}
	return tx.Commit()
}

func Pins(ctx context.Context, path string) (map[string]model.Item, error) {
	out := map[string]model.Item{}
	db, err := open(ctx, path, false, pinSchema)
	if err != nil || db == nil {
		return out, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT ref,body FROM pins")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref, body string
		if err = rows.Scan(&ref, &body); err != nil {
			return out, err
		}
		var i model.Item
		if err = json.Unmarshal([]byte(body), &i); err != nil {
			return out, err
		}
		out[ref] = i
	}
	return out, rows.Err()
}
func Pin(ctx context.Context, path string, i model.Item, add bool) error {
	db, err := open(ctx, path, true, pinSchema)
	if err != nil {
		return err
	}
	defer db.Close()
	if !add {
		_, err = db.ExecContext(ctx, "DELETE FROM pins WHERE ref=?", i.Ref)
		return err
	}
	// Only a locator/display hint is durable; the item body is not a second task store.
	hint := model.Item{Ref: i.Ref, Origin: i.Origin, SourceID: i.SourceID, Scope: i.Scope, DisplayRef: i.DisplayRef, Title: i.Title, State: "unknown", Pinned: true, Available: false, ObservedAt: time.Time{}}
	b, _ := json.Marshal(hint)
	_, err = db.ExecContext(ctx, "INSERT INTO pins(ref,body) VALUES(?,?) ON CONFLICT(ref) DO UPDATE SET body=excluded.body", i.Ref, string(b))
	return err
}
