package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// InvalidEdit reports invalid saved content; user changes are never reverted.
type InvalidEdit struct {
	Path string
	Err  error
}

func (e *InvalidEdit) Error() string {
	return fmt.Sprintf("configuration %q is invalid; editor changes were not reverted: %v", e.Path, e.Err)
}
func (e *InvalidEdit) Unwrap() error { return e.Err }

func Editor() string {
	for _, key := range []string{"VISUAL", "EDITOR"} {
		if value := os.Getenv(key); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "vi"
}

// editorArgs supports quotes/escapes, never shell expansion or evaluation.
func editorArgs(value string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	started, escape := false, false
	for _, r := range value {
		if escape {
			if quote == '"' && !strings.ContainsRune("$`\"\\\n", r) {
				word.WriteRune('\\')
			}
			if r != '\n' {
				word.WriteRune(r)
			}
			escape = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escape = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if unicode.IsSpace(r) {
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
			continue
		}
		word.WriteRune(r)
		started = true
	}
	if quote != 0 || escape {
		return nil, errors.New("editor command has an unfinished quote or escape")
	}
	if started {
		args = append(args, word.String())
	}
	if len(args) == 0 || args[0] == "" {
		return nil, errors.New("editor command needs an executable")
	}
	return args, nil
}

func prepareEdit(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("configuration path must be a regular file")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// Do not truncate a file another process created while the editor was resolved.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		info, e := os.Stat(path)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("configuration path must be a regular file")
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(file, Example)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

// Edit owns a standalone editor invocation. The CLI must gate it on a real TTY
// and non-machine intent before calling; existing files need not parse first.
func Edit(ctx context.Context, path, editor string, in io.Reader, out, errOut io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		return errors.New("editor target must be an absolute path")
	}
	args, err := editorArgs(editor)
	if err != nil {
		return err
	}
	executable, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("cannot find configured editor %q; set VISUAL or EDITOR", args[0])
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = prepareEdit(path); err != nil {
		return fmt.Errorf("prepare configuration: %w", err)
	}
	fmt.Fprintf(errOut, "Editing configuration: %q\n", path)
	cmd := exec.CommandContext(ctx, executable, append(args[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errOut
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("editor failed; any file changes were kept: %w", err)
	}
	if _, err = LoadFile(path); err != nil {
		return &InvalidEdit{path, err}
	}
	_, err = fmt.Fprintf(out, "Configuration validated: %q\nNo sources were fetched.\n", path)
	return err
}
