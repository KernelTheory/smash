package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	activeAge  = time.Hour // JSONL files touched more recently belong to a live session; skip them
	flushItems = 100
	flushBytes = 64 << 20
)

const schema = `
CREATE TABLE IF NOT EXISTS files (path TEXT PRIMARY KEY, done INTEGER NOT NULL, tail TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS seen (hash TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS settings (name TEXT PRIMARY KEY, value BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS scans (
  id INTEGER PRIMARY KEY, at TEXT NOT NULL, source TEXT NOT NULL, path TEXT NOT NULL, status TEXT NOT NULL,
  bytes INTEGER NOT NULL, candidates INTEGER NOT NULL, redacted INTEGER NOT NULL, rejected INTEGER NOT NULL);`

func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, os.Chmod(path, 0o600)
}

type summary struct{ files, skipped, bytes, candidates, redacted, rejected int }

func (s *summary) add(o summary) {
	s.files, s.skipped, s.bytes = s.files+o.files, s.skipped+o.skipped, s.bytes+o.bytes
	s.candidates, s.redacted, s.rejected = s.candidates+o.candidates, s.redacted+o.redacted, s.rejected+o.rejected
}

type report struct {
	summary
	bySource map[string]*summary
}

type runner struct {
	home string
	db   *sql.DB
	ai   redactor // nil = preview: count only, send and write nothing
	out  io.Writer

	now        time.Time // zero = time.Now(); lines dated on or after its local midnight wait for the next day
	at, cutoff time.Time // this run's clock and its local midnight
	seen       map[string]bool
	rep        report
}

type source struct {
	name, path string
	shell      bool
}

// historyFiles returns existing shell history files. $HISTFILE is usually a non-exported
// shell variable, so the well-known locations are checked too.
func historyFiles(home string) []string {
	seen := map[string]bool{}
	var files []string
	for _, p := range []string{os.Getenv("HISTFILE"), filepath.Join(home, ".zsh_history"),
		filepath.Join(home, ".zhistory"), filepath.Join(home, ".bash_history")} {
		real, err := filepath.EvalSymlinks(p)
		if p == "" || err != nil || seen[real] {
			continue
		}
		if fi, err := os.Stat(real); err == nil && fi.Mode().IsRegular() {
			seen[real] = true
			files = append(files, p)
		}
	}
	return files
}

func (r *runner) sources() []source {
	var out []source
	for _, p := range historyFiles(r.home) {
		out = append(out, source{"shell", p, true})
	}
	for _, d := range [][2]string{{"claude", ".claude/projects"}, {"codex", ".codex/sessions"}} {
		filepath.WalkDir(filepath.Join(r.home, d[1]), func(p string, e fs.DirEntry, err error) error {
			if err == nil && e.Type().IsRegular() && strings.HasSuffix(p, ".jsonl") {
				out = append(out, source{d[0], p, false})
			}
			return nil
		})
	}
	for _, p := range []string{".claude/history.jsonl", ".codex/history.jsonl"} {
		if fi, err := os.Stat(filepath.Join(r.home, p)); err == nil && fi.Mode().IsRegular() {
			out = append(out, source{"prompts", filepath.Join(r.home, p), false})
		}
	}
	return out
}

// record is one shell entry or one JSONL line in the unprocessed part of a file.
type record struct {
	raw   []byte
	entry *Entry // shell history only
	snips []string
}

type job struct {
	src        source
	hist       *History
	data       []byte
	start, end int // records cover data[start:end]
	recs       []*record
	cands      int
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// tailHash fingerprints the 4 KB before off; if it changes, the processed part was rewritten.
func tailHash(data []byte, off int) string { return hash(data[max(0, off-4096):off]) }

func (r *runner) run() (report, error) {
	r.rep = report{bySource: map[string]*summary{}}
	r.seen = map[string]bool{}
	r.at = r.now
	if r.at.IsZero() {
		r.at = time.Now()
	}
	r.cutoff = time.Date(r.at.Year(), r.at.Month(), r.at.Day(), 0, 0, 0, 0, r.at.Location())
	rows, err := r.db.Query(`SELECT hash FROM seen`)
	if err != nil {
		return r.rep, err
	}
	for rows.Next() {
		var h string
		rows.Scan(&h)
		r.seen[h] = true
	}
	rows.Close()

	var pending []*job
	items, held := 0, 0
	for _, src := range r.sources() {
		j, status, err := r.scan(src)
		if err != nil {
			fmt.Fprintf(r.out, "smash: %s: %v\n", src.path, err)
			r.finish(src, status, summary{})
			continue
		}
		if j == nil {
			r.finish(src, status, summary{skipped: 1})
			continue
		}
		pending = append(pending, j)
		items, held = items+j.cands, held+len(j.data)
		if items >= flushItems || held >= flushBytes {
			if err := r.flush(pending); err != nil {
				return r.rep, err
			}
			pending, items, held = nil, 0, 0
		}
	}
	return r.rep, r.flush(pending)
}

func (r *runner) scan(src source) (*job, string, error) {
	fi, err := os.Stat(src.path)
	if err != nil {
		return nil, "error", err
	}
	if !src.shell && r.at.Sub(fi.ModTime()) < activeAge {
		return nil, "skipped-active", nil
	}
	data, err := os.ReadFile(src.path)
	if err != nil {
		return nil, "error", err
	}
	done, tail := 0, ""
	r.db.QueryRow(`SELECT done, tail FROM files WHERE path = ?`, src.path).Scan(&done, &tail)
	if done > len(data) || tailHash(data, done) != tail {
		done = 0 // new, truncated or rewritten: scan from the start
	}
	j := &job{src: src, data: data}
	end := bytes.LastIndexByte(data, '\n') + 1 // a last line without newline may still be being written

	if src.shell {
		j.hist = ParseHistory(src.path, data)
		start := -1
		for _, e := range j.hist.Entries {
			if e.Off == done {
				start = done
			}
			if start >= 0 && e.Off+len(e.raw) <= end {
				j.recs = append(j.recs, &record{raw: e.raw, entry: e})
			}
		}
		if start < 0 && done != end && done != 0 {
			r.db.Exec(`DELETE FROM files WHERE path = ?`, src.path) // offset isn't an entry boundary
			return r.scan(src)
		}
		j.start = max(start, done)
	} else {
		j.start = done
		for off := done; off < end; {
			n := bytes.IndexByte(data[off:end], '\n') + 1
			j.recs = append(j.recs, &record{raw: data[off : off+n]})
			off += n
		}
	}
	j.end = j.start
	for i, rec := range j.recs {
		var texts []string
		var ts time.Time
		if rec.entry != nil {
			texts, ts = []string{rec.entry.Cmd}, rec.entry.Time()
		} else {
			texts, ts = jsonRecord(rec.raw)
		}
		if !ts.Before(r.cutoff) { // dated today: this and everything after waits for tomorrow (undated = old)
			j.recs = j.recs[:i]
			break
		}
		j.end += len(rec.raw)
		local := map[string]bool{}
		for _, t := range texts {
			for _, s := range snippets(t) {
				if !local[s] && !r.seen[hash([]byte(s))] {
					local[s] = true
					rec.snips = append(rec.snips, s)
				}
			}
		}
		j.cands += len(rec.snips)
	}
	return j, "scanned", nil
}

// flush sends the unique snippets of the pending jobs to the model, then applies and writes each job.
func (r *runner) flush(jobs []*job) error {
	var items []redactItem
	ids := map[string]int{}
	for _, j := range jobs {
		for _, rec := range j.recs {
			for _, s := range rec.snips {
				if _, ok := ids[s]; !ok {
					ids[s] = len(items)
					items = append(items, redactItem{len(items), s})
				}
			}
		}
	}
	if r.ai == nil {
		for _, j := range jobs {
			r.add(j.src.name, summary{files: 1, bytes: j.end - j.start, candidates: j.cands})
		}
		return nil
	}

	results := map[string]string{} // snippet -> validated redaction
	rejected := map[string]bool{}
	for lo := 0; lo < len(items); {
		hi, size := lo, 0
		for hi < len(items) && hi-lo < batchItems && (hi == lo || size+len(items[hi].Text) <= batchBytes) {
			size += len(items[hi].Text)
			hi++
		}
		got, err := r.ai.redact(items[lo:hi])
		if errors.Is(err, errRefused) {
			for _, it := range items[lo:hi] {
				rejected[it.Text] = true
			}
			lo = hi
			continue
		}
		if err != nil {
			return err // nothing in this flush is written or advanced; the next run retries
		}
		for _, g := range got {
			if g.ID < lo || g.ID >= hi {
				continue
			}
			if orig := items[g.ID].Text; masked(orig, g.Text) {
				results[orig] = g.Text
			} else {
				rejected[orig] = true
			}
		}
		lo = hi
	}

	for _, j := range jobs {
		if err := r.apply(j, results, rejected); err != nil {
			fmt.Fprintf(r.out, "smash: %s: %v\n", j.src.path, err)
			r.finish(j.src, "error", summary{})
		}
	}
	// Clean snippets and our own redacted output never need to be sent again.
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	for _, it := range items {
		if rejected[it.Text] {
			continue
		}
		s := it.Text
		if red, ok := results[s]; ok {
			s = red
		}
		h := hash([]byte(s))
		r.seen[h] = true
		tx.Exec(`INSERT OR IGNORE INTO seen(hash) VALUES (?)`, h)
	}
	return tx.Commit()
}

func (r *runner) apply(j *job, results map[string]string, rejected map[string]bool) error {
	sum := summary{files: 1, bytes: j.end - j.start, candidates: j.cands}
	var region []byte
	for _, rec := range j.recs {
		for _, s := range rec.snips {
			red, ok := results[s]
			switch {
			case rejected[s]:
				sum.rejected++
			case !ok:
			case rec.entry != nil:
				rec.entry.Cmd = strings.ReplaceAll(rec.entry.Cmd, s, red)
				rec.raw = j.hist.encode(rec.entry, rec.entry.Cmd)
				sum.redacted++
			default:
				if raw, ok := replaceJSON(rec.raw, s, red); ok {
					rec.raw = raw
					sum.redacted++
				} else {
					sum.rejected++
				}
			}
		}
		region = append(region, rec.raw...)
	}

	data := j.data
	if sum.redacted > 0 {
		// Re-read so anything appended while the model was thinking is kept.
		cur, err := os.ReadFile(j.src.path)
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(cur, j.data[:j.end]) {
			r.finish(j.src, "changed-during-run", summary{skipped: 1})
			return nil
		}
		data = append(append(append([]byte{}, j.data[:j.start]...), region...), cur[j.end:]...)
		if err := writeAtomic(j.src.path, data); err != nil {
			return err
		}
	}
	end := j.start + len(region)
	if _, err := r.db.Exec(`INSERT INTO files(path, done, tail) VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET done = excluded.done, tail = excluded.tail`,
		j.src.path, end, tailHash(data, end)); err != nil {
		return err
	}
	r.finish(j.src, "scanned", sum)
	return nil
}

// finish records one file's outcome in the totals and, outside preview, in the scans log.
func (r *runner) finish(src source, status string, sum summary) {
	r.add(src.name, sum)
	if r.ai == nil {
		return
	}
	r.db.Exec(`INSERT INTO scans(at, source, path, status, bytes, candidates, redacted, rejected) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), src.name, src.path, status, sum.bytes, sum.candidates, sum.redacted, sum.rejected)
}

func (r *runner) add(name string, sum summary) {
	if r.rep.bySource[name] == nil {
		r.rep.bySource[name] = &summary{}
	}
	r.rep.bySource[name].add(sum)
	r.rep.summary.add(sum)
}
