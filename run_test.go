package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var dummySecret = regexp.MustCompile(`sk-proj-\w+|ghp_\w+|hunter2|DUMMYwJalr\S+|S3cr3tPass!|sk_live_\w+|xoxb-[\w-]+`)

// fakeOpenAI masks dummySecret matches and returns only changed items, like the real prompt asks.
// A line with --otp gets a rewritten (invalid) answer to exercise rejection.
func fakeOpenAI(t *testing.T, sent *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model, Input string
		}
		if r.Header.Get("Authorization") != "Bearer test-key" || json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		var items []redactItem
		json.Unmarshal([]byte(req.Input), &items)
		out := []redactItem{}
		for _, it := range items {
			*sent = append(*sent, it.Text)
			if strings.Contains(it.Text, "--otp") {
				out = append(out, redactItem{it.ID, "npm publish"})
			} else if red := dummySecret.ReplaceAllString(it.Text, Mask); red != it.Text {
				out = append(out, redactItem{it.ID, red})
			}
		}
		body, _ := json.Marshal(map[string][]redactItem{"redacted": out})
		json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{
			map[string]any{"type": "reasoning"},
			map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(body)}}},
		}})
	}))
}

func put(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-age)
	os.Chtimes(path, old, old)
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunIncremental(t *testing.T) {
	home := t.TempDir()
	zsh, _ := os.ReadFile("testdata/zsh_history")
	bash, _ := os.ReadFile("testdata/bash_history")
	put(t, home+"/.zsh_history", string(zsh), 0)
	put(t, home+"/.bash_history", string(bash), 0)
	session := home + "/.claude/projects/p/s1.jsonl"
	put(t, session, `{"type":"user","message":{"content":"set it:\nexport STRIPE=\"sk_live_DUMMYabcdef123456\""}}`+"\n"+
		`{"type":"assistant","message":{"content":"Done. Nothing secret here."}}`+"\n", 2*time.Hour)
	codex := home + "/.codex/sessions/2026/10/01/rollout-1.jsonl"
	put(t, codex, `{"payload":{"cmd":["bash","-lc","curl -H 'Authorization: Bearer xoxb-DUMMY-1234567890' slack.com"]}}`+"\n", 2*time.Hour)
	active := home + "/.claude/projects/p/active.jsonl"
	activeBody := `{"message":{"content":"export OPENAI_API_KEY=sk-proj-ACTIVE1234567890abcdef"}}` + "\n"
	put(t, active, activeBody, time.Minute)
	prompts := home + "/.claude/history.jsonl"
	put(t, prompts, `{"display":"use key sk-proj-PROMPT1234567890abcdef please"}`+"\n", 2*time.Hour)

	var sent []string
	srv := fakeOpenAI(t, &sent)
	defer srv.Close()
	db, err := openDB(filepath.Join(home, ".smash", "smash.db"))
	if err != nil {
		t.Fatal(err)
	}
	r := &runner{home: home, db: db, ai: &openAI{url: srv.URL, key: "test-key", model: "m"}, out: io.Discard}

	// Run 1: everything existing is scanned and redacted.
	sum, err := r.run()
	if err != nil {
		t.Fatal(err)
	}
	z := read(t, home+"/.zsh_history")
	for _, s := range []string{"sk-proj-DUMMY", "ghp_DUMMY", "hunter2", "DUMMYwJalr"} {
		if strings.Contains(z, s) {
			t.Errorf("zsh history still has %s", s)
		}
	}
	if !strings.Contains(z, "docker run -e DB_PASSWORD=[REDACTED] \\\\\n  -p") || !strings.Contains(z, "deploy \xf0\x83\xbf\x83\xba\x80 done") {
		t.Errorf("zsh structure or metafied bytes damaged:\n%q", z)
	}
	b := read(t, home+"/.bash_history")
	if strings.Contains(b, "S3cr3tPass!") || strings.Contains(b, "sk_live_") || !strings.Contains(b, "npm publish --otp=123456") {
		t.Errorf("bash history wrong (rewritten answer must be rejected):\n%s", b)
	}
	for _, f := range []string{session, codex, prompts} {
		got := read(t, f)
		if dummySecret.MatchString(got) || !strings.Contains(got, Mask) {
			t.Errorf("%s not redacted:\n%s", f, got)
		}
		for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
			if !json.Valid([]byte(line)) {
				t.Errorf("%s: invalid JSON line %s", f, line)
			}
		}
	}
	if read(t, active) != activeBody {
		t.Error("active session was modified")
	}
	if sum.rejected != 1 {
		t.Errorf("rejected = %d, want 1", sum.rejected)
	}
	dbBytes, _ := os.ReadFile(filepath.Join(home, ".smash", "smash.db"))
	if dummySecret.Match(dbBytes) || bytes.Contains(dbBytes, []byte("123456")) {
		t.Error("database contains secret text")
	}

	// Run 2: nothing new, so nothing is sent (clean and already-redacted snippets are cached).
	sent = nil
	if _, err := r.run(); err != nil || len(sent) != 0 {
		t.Fatalf("run 2 sent %q err=%v", sent, err)
	}

	// Run 3: only the appended command is sent.
	f, _ := os.OpenFile(home+"/.zsh_history", os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(": 1729500070:0;export GITHUB_TOKEN=ghp_NEWDUMMY1234567890abcd\n")
	f.Close()
	if _, err := r.run(); err != nil || len(sent) != 1 || !strings.Contains(sent[0], "ghp_NEWDUMMY") {
		t.Fatalf("run 3 sent %q err=%v", sent, err)
	}

	// Run 4: the shell trimmed its file (head changed); full rescan, but the cache means nothing is resent.
	sent = nil
	z = read(t, home+"/.zsh_history")
	put(t, home+"/.zsh_history", z[strings.Index(z, "\n")+1:], 0)
	if _, err := r.run(); err != nil || len(sent) != 0 {
		t.Fatalf("run 4 sent %q err=%v", sent, err)
	}

	var rows int
	db.QueryRow(`SELECT count(*) FROM scans WHERE status = 'skipped-active'`).Scan(&rows)
	if rows == 0 {
		t.Error("active session skip was not logged")
	}
}

func TestRunSkipsToday(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	yesterday, today := now.Add(-20*time.Hour), now.Add(-11*time.Hour) // 16:00 the day before, 01:00 today
	iso := func(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
	session := home + "/.claude/projects/p/s.jsonl"
	put(t, session, fmt.Sprintf(`{"timestamp":"%s","message":{"content":"export A=sk-proj-OLD01234567890abcdef"}}`+"\n"+
		`{"type":"permission-mode"}`+"\n"+
		`{"timestamp":"%s","message":{"content":"export B=sk-proj-NEW01234567890abcdef"}}`+"\n", iso(yesterday), iso(today)), 2*time.Hour)
	prompts := home + "/.claude/history.jsonl"
	put(t, prompts, fmt.Sprintf(`{"display":"key ghp_OLDDUMMY1234567890abcd","timestamp":%d}`+"\n"+
		`{"display":"key ghp_NEWDUMMY1234567890abcd","timestamp":%d}`+"\n", yesterday.UnixMilli(), today.UnixMilli()), 2*time.Hour)
	put(t, home+"/.zsh_history", fmt.Sprintf(": %d:0;export C=sk-proj-OLDZSH1234567890abcdef\n: %d:0;export D=sk-proj-NEWZSH1234567890abcdef\n",
		yesterday.Unix(), today.Unix()), 0)

	var sent []string
	srv := fakeOpenAI(t, &sent)
	defer srv.Close()
	db, _ := openDB(filepath.Join(home, ".smash", "smash.db"))
	r := &runner{home: home, db: db, ai: &openAI{url: srv.URL, key: "test-key", model: "m"}, out: io.Discard, now: now}
	if _, err := r.run(); err != nil {
		t.Fatal(err)
	}
	all := read(t, session) + read(t, prompts) + read(t, home+"/.zsh_history")
	for _, s := range []string{"OLD0123", "OLDDUMMY", "OLDZSH"} {
		if strings.Contains(all, s) {
			t.Errorf("yesterday's %s not redacted", s)
		}
	}
	for _, s := range []string{"NEW0123", "NEWDUMMY", "NEWZSH"} {
		if !strings.Contains(all, s) {
			t.Errorf("today's %s was redacted", s)
		}
	}

	// Next day: only the lines held back yesterday are sent.
	sent = nil
	r.now = now.Add(24 * time.Hour)
	if _, err := r.run(); err != nil || len(sent) != 3 {
		t.Fatalf("next day sent %d items (%v), err=%v", len(sent), sent, err)
	}
	if all := read(t, session) + read(t, prompts) + read(t, home+"/.zsh_history"); dummySecret.MatchString(all) || strings.Contains(all, "NEW") {
		t.Errorf("held-back lines not redacted next day:\n%s", all)
	}
}
