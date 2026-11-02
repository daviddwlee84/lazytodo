package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"lazytodo/internal/search"
)

func TestEmptyInterruptedInitializationCanRecover(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	if e := os.WriteFile(path, nil, 0600); e != nil {
		t.Fatal(e)
	}
	r, e := Read(ctx, path, nil, search.Query{})
	if e != nil || len(r.Sources) != 0 {
		t.Fatal(r, e)
	}
	db, e := open(ctx, path, true, cacheSchema)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	if _, e = Read(ctx, path, nil, search.Query{}); e != nil {
		t.Fatal(e)
	}
}
func TestForeignAndNewerSchemaNotOverwritten(t *testing.T) {
	for _, stmt := range []string{"CREATE TABLE foreign_data(value TEXT)", "PRAGMA user_version=2"} {
		path := filepath.Join(t.TempDir(), "cache.sqlite")
		db, e := sql.Open("sqlite", path)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(stmt); e != nil {
			t.Fatal(e)
		}
		db.Close()
		if _, e = open(context.Background(), path, true, cacheSchema); e == nil {
			t.Fatal("accepted foreign/newer schema", stmt)
		}
	}
}
