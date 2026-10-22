package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeReleases serves VERSION, checksums.txt and one archive containing a "smash" with body.
func fakeReleases(t *testing.T, latest, body string, badSum bool) *httptest.Server {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	tw := tar.NewWriter(zw)
	tw.WriteHeader(&tar.Header{Name: "smash", Mode: 0o755, Size: int64(len(body))})
	tw.Write([]byte(body))
	tw.Close()
	zw.Close()
	sum := fmt.Sprintf("%x", sha256.Sum256(gz.Bytes()))
	if badSum {
		sum = fmt.Sprintf("%x", sha256.Sum256([]byte("tampered")))
	}
	files := map[string][]byte{
		"/releases/latest/VERSION":                          []byte(latest + "\n"),
		"/releases/" + latest + "/checksums.txt":            []byte(sum + "  smash_linux_arm64.tar.gz\n"),
		"/releases/" + latest + "/smash_linux_arm64.tar.gz": gz.Bytes(),
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, ok := files[r.URL.Path]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
}

func TestUpdate(t *testing.T) {
	target := filepath.Join(t.TempDir(), "smash")
	os.WriteFile(target, []byte("old"), 0o755)
	check := func(name, want string) {
		t.Helper()
		if b, _ := os.ReadFile(target); string(b) != want {
			t.Errorf("%s: binary = %q, want %q", name, b, want)
		}
	}

	srv := fakeReleases(t, "v0.2.0", "new", false)
	defer srv.Close()
	if latest, updated, err := update(srv.URL, "v0.1.3", target, "linux", "arm64"); err != nil || !updated || latest != "v0.2.0" {
		t.Fatalf("newer: latest=%s updated=%v err=%v", latest, updated, err)
	}
	check("newer", "new")
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", fi.Mode().Perm())
	}
	if _, updated, err := update(srv.URL, "v0.2.0", target, "linux", "arm64"); err != nil || updated {
		t.Errorf("same version: updated=%v err=%v", updated, err)
	}
	if _, updated, err := update(srv.URL, "v0.10.0", target, "linux", "arm64"); err != nil || updated {
		t.Errorf("older remote must not downgrade: updated=%v err=%v", updated, err)
	}

	os.WriteFile(target, []byte("old"), 0o755)
	bad := fakeReleases(t, "v0.2.0", "evil", true)
	defer bad.Close()
	if _, _, err := update(bad.URL, "v0.1.3", target, "linux", "arm64"); err == nil {
		t.Error("checksum mismatch: want error")
	}
	check("checksum mismatch", "old")
}
