package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Mask = "[REDACTED]"

var (
	zshPrefix  = regexp.MustCompile(`^: \d+:\d+;`)
	bashStamp  = regexp.MustCompile(`^#\d+\n$`)
	zshMeta    = byte(0x83)
	zshNewline = []byte("\\\n")
)

// Entry is one shell command.
type Entry struct {
	Line int    // 1-based first line of the entry
	Off  int    // byte offset of raw in the file
	Cmd  string // decoded command text

	prefix []byte // zsh ": <ts>:<dur>;" or bash "#<ts>\n" lines, kept verbatim
	raw    []byte // exact bytes in the file, including the trailing newline if any
}

type History struct {
	Zsh     bool
	Entries []*Entry
}

func ParseHistory(path string, data []byte) *History {
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	base := filepath.Base(path)
	h := &History{Zsh: strings.Contains(base, "zsh") || strings.Contains(base, "zhist") ||
		(len(lines) > 0 && zshPrefix.Match(lines[0]))}

	off := 0
	for i := 0; i < len(lines); {
		e := &Entry{Line: i + 1, Off: off}
		if h.Zsh {
			// zsh writes an embedded newline as backslash-newline; keep reading until a line doesn't end that way.
			for {
				e.raw = append(e.raw, lines[i]...)
				i++
				if i >= len(lines) || !bytes.HasSuffix(lines[i-1], zshNewline) {
					break
				}
			}
			body := bytes.TrimSuffix(e.raw, []byte("\n"))
			e.prefix = zshPrefix.Find(body)
			body = bytes.ReplaceAll(body[len(e.prefix):], zshNewline, []byte("\n"))
			e.Cmd = string(unmetafy(body))
		} else {
			if bashStamp.Match(lines[i]) && i+1 < len(lines) {
				e.prefix = lines[i]
				e.raw = append(e.raw, lines[i]...)
				i++
				e.Line = i + 1
			}
			e.raw = append(e.raw, lines[i]...)
			e.Cmd = strings.TrimSuffix(string(lines[i]), "\n")
			i++
		}
		off += len(e.raw)
		h.Entries = append(h.Entries, e)
	}
	return h
}

func (h *History) encode(e *Entry, cmd string) []byte {
	out := append([]byte{}, e.prefix...)
	if h.Zsh {
		out = append(out, bytes.ReplaceAll(metafy([]byte(cmd)), []byte("\n"), zshNewline)...)
	} else {
		out = append(out, strings.ReplaceAll(cmd, "\n", " ")...)
	}
	if bytes.HasSuffix(e.raw, []byte("\n")) {
		out = append(out, '\n')
	}
	return out
}

// zsh stores NUL and bytes 0x83-0xa2 as Meta followed by byte^0x20.
func unmetafy(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == zshMeta && i+1 < len(b) {
			i++
			out = append(out, b[i]^0x20)
		} else {
			out = append(out, b[i])
		}
	}
	return out
}

func metafy(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 || (c >= zshMeta && c <= 0xa2) {
			out = append(out, zshMeta, c^0x20)
		} else {
			out = append(out, c)
		}
	}
	return out
}

// writeAtomic replaces path's target (following symlinks) via rename, keeping its mode.
// ponytail: takes no lock; a writer appending in the ms between read and rename loses that line.
func writeAtomic(path string, data []byte) error {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".smash-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(fi.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Time is the entry's timestamp from a zsh ": <epoch>:" or bash "#<epoch>" prefix; zero if undated.
func (e *Entry) Time() time.Time {
	s := strings.TrimLeft(string(e.prefix), ": #")
	if i := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		s = s[:i]
	}
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
