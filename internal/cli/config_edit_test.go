package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigEditNonTTYDoesNotCreateOrLaunch(t *testing.T) {
	dir := isolated(t)
	path := filepath.Join(dir, "new/config.toml")
	t.Setenv("VISUAL", "definitely-not-an-editor")
	code, out, err := invoke("--config", path, "config", "edit")
	if code != 2 || out != "" || !strings.Contains(err, "requires input/output terminals") {
		t.Fatal(code, out, err)
	}
	if _, e := os.Stat(filepath.Dir(path)); !os.IsNotExist(e) {
		t.Fatal("non-TTY edit created files")
	}
}
