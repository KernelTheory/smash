package main

import (
	"bufio"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPlistIsValidXML(t *testing.T) {
	p := agentPlist("/Users/a&b/bin/smash", "/Users/a&b/.smash/launchd.log")
	d := xml.NewDecoder(strings.NewReader(p))
	d.Strict, d.Entity = true, xml.HTMLEntity
	for {
		if _, err := d.Token(); err != nil {
			if err.Error() != "EOF" {
				t.Fatalf("invalid plist: %v\n%s", err, p)
			}
			break
		}
	}
	if !strings.Contains(p, "<string>/Users/a&amp;b/bin/smash</string><string>run</string>") {
		t.Errorf("program arguments not escaped:\n%s", p)
	}
}

func TestUninstall(t *testing.T) {
	home := t.TempDir()
	keep := filepath.Join(t.TempDir(), "dev-build-smash") // a link target outside the install must survive
	os.WriteFile(keep, []byte("bin"), 0o755)
	os.MkdirAll(filepath.Join(home, ".smash"), 0o700)
	os.WriteFile(filepath.Join(home, ".smash", "config"), []byte("OPENAI_API_KEY=x\n"), 0o600)
	os.MkdirAll(filepath.Dir(agentPath(home)), 0o755)
	os.WriteFile(agentPath(home), []byte("<plist/>"), 0o644)
	os.MkdirAll(unitDir(home), 0o755)
	os.WriteFile(filepath.Join(unitDir(home), "smash.service"), []byte("[Service]"), 0o644)
	os.WriteFile(filepath.Join(unitDir(home), "smash.timer"), []byte("[Timer]"), 0o644)
	os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755)
	os.Symlink(keep, filepath.Join(home, ".local", "bin", "smash"))
	installed := append(jobFiles(home), filepath.Join(home, ".smash"), filepath.Join(home, ".local", "bin", "smash"))

	if err := uninstall(home, bufio.NewReader(strings.NewReader("n\n"))); err != nil {
		t.Fatal(err)
	}
	for _, p := range installed {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("declined, but %s is gone", p)
		}
	}

	if err := uninstall(home, bufio.NewReader(strings.NewReader("y\n"))); err != nil {
		t.Fatal(err)
	}
	for _, p := range installed {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s still exists", p)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("link target was deleted: %v", err)
	}
}

func TestSystemdUnits(t *testing.T) {
	service, timer := systemdUnits(`/home/a b/.local/bin/smash%"x`)
	for _, want := range []string{"Type=oneshot\n", `ExecStart="/home/a b/.local/bin/smash%%\"x" run` + "\n"} {
		if !strings.Contains(service, want) {
			t.Errorf("service missing %q:\n%s", want, service)
		}
	}
	for _, want := range []string{"OnCalendar=*-*-* 10:00:00\n", "Persistent=true\n", "WantedBy=timers.target\n"} {
		if !strings.Contains(timer, want) {
			t.Errorf("timer missing %q:\n%s", want, timer)
		}
	}
}
