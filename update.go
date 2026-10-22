package main

import (
	"archive/tar"
	"bytes"
	"cmp"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const defaultBase = "https://keysmash.website"

// selfUpdate replaces the binary install.sh put in ~/.local/bin with the latest release, if newer.
// The daily job points at that same path, so it picks up the new version on its next run.
func selfUpdate(home string) error {
	if version == "dev" {
		return fmt.Errorf("this is a development build; install a release with: curl -fsSL %s/install.sh | sh", defaultBase)
	}
	bin := filepath.Join(home, ".local", "bin", "smash")
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	real, rerr := filepath.EvalSymlinks(bin)
	fi, lerr := os.Lstat(bin)
	if err != nil || rerr != nil || lerr != nil || !fi.Mode().IsRegular() || exe != real {
		return fmt.Errorf("only the copy install.sh put in ~/.local/bin can update itself (this one is %s)", exe)
	}
	fmt.Printf("Checking for a newer release (you have %s)...\n", version)
	latest, updated, err := update(cmp.Or(os.Getenv("SMASH_BASE"), defaultBase), version, bin, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if !updated {
		fmt.Printf("smash %s is up to date.\n", version)
		return nil
	}
	fmt.Printf("Updated smash %s → %s (checksum verified).\n", version, latest)
	return nil
}

// update installs base/releases/<latest>/smash_<os>_<arch>.tar.gz over target when latest is
// newer than current, after checking it against that release's checksums.txt.
func update(base, current, target, goos, goarch string) (string, bool, error) {
	v, err := get(base+"/releases/latest/VERSION", 64)
	if err != nil {
		return "", false, err
	}
	latest := strings.TrimSpace(string(v))
	if !newer(latest, current) {
		return latest, false, nil
	}
	dir := base + "/releases/" + latest + "/" // pinned, so a deploy mid-update can't mix versions
	file := "smash_" + goos + "_" + goarch + ".tar.gz"
	sums, err := get(dir+"checksums.txt", 64<<10)
	if err != nil {
		return latest, false, err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == file {
			want = f[0]
		}
	}
	if want == "" {
		return latest, false, fmt.Errorf("%s has no build for %s/%s", latest, goos, goarch)
	}
	tgz, err := get(dir+file, 200<<20)
	if err != nil {
		return latest, false, err
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(tgz)); got != want {
		return latest, false, fmt.Errorf("checksum mismatch for %s; not updating", file)
	}
	bin, err := untarSmash(tgz)
	if err != nil {
		return latest, false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".smash-update-*")
	if err != nil {
		return latest, false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return latest, false, err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return latest, false, err
	}
	if err := tmp.Close(); err != nil {
		return latest, false, err
	}
	return latest, true, os.Rename(tmp.Name(), target)
}

func get(url string, limit int64) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func untarSmash(tgz []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("release archive has no smash binary")
		}
		if err != nil {
			return nil, err
		}
		if h.Name == "smash" && h.FileInfo().Mode().IsRegular() {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// newer reports whether version a (vX.Y.Z) is greater than b. Unparseable versions never are.
func newer(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var p [3]int
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) != 3 {
		return p, false
	}
	for i, s := range parts {
		n, err := strconv.Atoi(s)
		if err != nil {
			return p, false
		}
		p[i] = n
	}
	return p, true
}
