package main

import (
	"bytes"
	"os"
	"testing"
)

func parseFixture(t *testing.T, path string) (*History, []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return ParseHistory(path, data), data
}

func TestZshParseAndEncode(t *testing.T) {
	h, data := parseFixture(t, "testdata/zsh_history")
	if !h.Zsh || len(h.Entries) != 7 {
		t.Fatalf("zsh=%v entries=%d, want zsh with 7 entries", h.Zsh, len(h.Entries))
	}
	docker, echo := h.Entries[3], h.Entries[6]
	if docker.Cmd != "docker run -e DB_PASSWORD=hunter2 \\\n  -p 5432:5432 postgres:16" {
		t.Errorf("multi-line entry = %q", docker.Cmd)
	}
	if echo.Cmd != `echo "deploy 🚀 done"` {
		t.Errorf("metafied entry = %q", echo.Cmd)
	}
	// Re-encoding unchanged commands must reproduce the file byte-for-byte.
	var out []byte
	for _, e := range h.Entries {
		out = append(out, h.encode(e, e.Cmd)...)
	}
	if !bytes.Equal(out, data) {
		t.Errorf("round trip differs:\n%q\n%q", out, data)
	}
	if docker.Off != bytes.Index(data, []byte(": 1729500030")) {
		t.Errorf("offset = %d", docker.Off)
	}
}

func TestBashParse(t *testing.T) {
	h, _ := parseFixture(t, "testdata/bash_history")
	if h.Zsh || len(h.Entries) != 4 {
		t.Fatalf("zsh=%v entries=%d, want bash with 4 entries", h.Zsh, len(h.Entries))
	}
	if e := h.Entries[0]; e.Line != 2 || e.Cmd != "mysql -u root -pS3cr3tPass! mydb" || string(e.prefix) != "#1729500100\n" {
		t.Errorf("entry = %+v", e)
	}
}
