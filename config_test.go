package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigEncryptedAtRest(t *testing.T) {
	home := t.TempDir()
	db, _ := openDB(filepath.Join(home, ".smash", "smash.db"))
	if err := writeConfig(home, db, "openai", "OPENAI_API_KEY", "sk-proj-SECRET1234567890"); err != nil {
		t.Fatal(err)
	}
	raw := read(t, home+"/.smash/config")
	if strings.Contains(raw, "SECRET") || !strings.Contains(raw, "PROVIDER=openai\n") || !strings.Contains(raw, "OPENAI_API_KEY=enc:v1:") {
		t.Fatalf("config not encrypted at rest:\n%s", raw)
	}
	ai, _, err := loadConfig(home, db)
	if err != nil || ai.(*openAI).key != "sk-proj-SECRET1234567890" {
		t.Fatalf("decrypt: %v", err)
	}

	// The encrypted value is bound to its variable name...
	enc := raw[strings.Index(raw, "enc:v1:"):]
	put(t, home+"/.smash/config", "PROVIDER=anthropic\nANTHROPIC_API_KEY="+enc, 0)
	if _, _, err := loadConfig(home, db); !errors.Is(err, errUndecryptable) {
		t.Errorf("value moved to another name: err=%v, want errUndecryptable", err)
	}
	// ...and to this database's key.
	put(t, home+"/.smash/config", raw, 0)
	other, _ := openDB(filepath.Join(t.TempDir(), "other.db"))
	if _, _, err := loadConfig(home, other); !errors.Is(err, errUndecryptable) {
		t.Errorf("other database: err=%v, want errUndecryptable", err)
	}
}

func TestConfigEncryptsPlaintextInPlace(t *testing.T) {
	home := t.TempDir()
	put(t, home+"/.smash/config", "# mine\nPROVIDER=anthropic\nANTHROPIC_API_KEY=sk-ant-PLAIN1234567890\nANTHROPIC_MODEL=claude-haiku-5-5\n", 0)
	db, _ := openDB(filepath.Join(home, ".smash", "smash.db"))
	if _, label, err := loadConfig(home, db); err != nil || label != "Anthropic claude-haiku-5-5" {
		t.Fatalf("label=%q err=%v", label, err)
	}
	raw := read(t, home+"/.smash/config")
	if strings.Contains(raw, "PLAIN") || !strings.Contains(raw, "ANTHROPIC_API_KEY=enc:v1:") ||
		!strings.HasPrefix(raw, "# mine\nPROVIDER=anthropic\n") || !strings.Contains(raw, "ANTHROPIC_MODEL=claude-haiku-5-5\n") {
		t.Fatalf("not migrated in place:\n%s", raw)
	}
	if fi, _ := os.Stat(home + "/.smash/config"); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if _, _, err := loadConfig(home, db); err != nil {
		t.Errorf("second load: %v", err)
	}
}
