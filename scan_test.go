package main

import (
	"strings"
	"testing"
)

func TestSuspicious(t *testing.T) {
	for line, want := range map[string]bool{
		"export OPENAI_API_KEY=sk-proj-DUMMY1234567890abcdef":                       true,
		`curl -H "Authorization: Bearer ghp_DUMMYtoken0987654321" https://x.io`:     true,
		"aws configure set aws_secret_access_key DUMMYwJalrXUtnFEMI/K7MDENG":        true,
		"mysql -u root -pS3cr3tPass! mydb":                                          true,
		"npm publish --otp=123456":                                                  true,
		"psql postgres://app:Zx9vQ2mP7kL4@db:5432/app":                              true,
		`{"token": "abc123def456"}`:                                                 true,
		"the key is 9fK2mQx7Lp4Vz8Rt3Nw6Yb1H ok":                                    true,
		"ls -la":                                                                    false,
		"git checkout 3f2a9c1d8e7b6a5f4e3d2c1b0a9f8e7d6c5b4a39":                     false,
		"cd ~/Workspace/personal/smash && docker compose run --rm go go test ./...": false,
		"id 123e4567-e89b-12d3-a456-426614174000":                                   false,
		"find . -name '*.go' -print":                                                false,
		"export OPENAI_API_KEY=[REDACTED]":                                          false,
		"tool_use_id toolu_01UaUPwu1uSGm7Td7BXYJF99":                                false,
		"id msg_01XyZaBcDeF9GhIjK2LmNoP3":                                           false,
		"call_Kx9mQ2vL7pR4tZ8wN3yB6cDf done":                                        false,
		"updated_at=2026-10-01T12:00:00Z9aB":                                        false,
		`{"input_tokens":12345,"cached_input_tokens":9000}`:                         false,
		`"token_count":{"info":null}`:                                               false,
		"headers: { Authorization: ${token} }":                                      false,
		"export DB_PASSWORD=hunter2x":                                               true,
	} {
		if got := len(suspicious(line)) > 0; got != want {
			t.Errorf("suspicious(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestSnippetsWindowLongLines(t *testing.T) {
	long := strings.Repeat("lorem ipsum ", 500) + "api_key=sk-proj-DUMMY1234567890abcdef " + strings.Repeat("dolor sit ", 500)
	got := snippets(long)
	if len(got) != 1 || !strings.Contains(got[0], "sk-proj-DUMMY1234567890abcdef") || len(got[0]) > 600 {
		t.Fatalf("snippets = %d items, first len %d", len(got), len(got[0]))
	}
	if s := snippets("ls -la\nexport TOKEN=abc123def456\n"); len(s) != 1 || s[0] != "export TOKEN=abc123def456" {
		t.Errorf("short snippets = %q", s)
	}
}

func TestMasked(t *testing.T) {
	orig := "curl -u admin:hunter22 -H 'X-Key: sk-proj-DUMMY1234567890abcdef' x.io"
	for red, want := range map[string]bool{
		"curl -u admin:[REDACTED] -H 'X-Key: [REDACTED]' x.io":                    true,
		"curl -u admin:[REDACTED] -H 'X-Key: sk-proj-DUMMY1234567890abcdef' x.io": true,
		orig:                            false, // nothing redacted
		"curl -u admin:[REDACTED] x.io": true,  // one wide span: over-masking beats leaving a secret
		"curl -u root:[REDACTED] -H 'X-Key: [REDACTED]' x.io":                             false, // rewrote text
		"curl -u admin:hunter22[REDACTED] -H 'X-Key: sk-proj-DUMMY1234567890abcdef' x.io": false, // inserted, nothing masked
	} {
		if got := masked(orig, red); got != want {
			t.Errorf("masked(%q) = %v, want %v", red, got, want)
		}
	}
}

func TestReplaceJSON(t *testing.T) {
	raw := []byte(`{"type":"user","message":{"content":"run:\nexport KEY=\"sk-proj-DUMMY1234567890abcdef\" && go <b>"},"cwd":"/x"}` + "\n")
	texts, _ := jsonRecord(raw)
	line := `export KEY="sk-proj-DUMMY1234567890abcdef" && go <b>`
	if len(texts) != 3 || !strings.Contains(texts[0]+texts[1]+texts[2], line) {
		t.Fatalf("texts = %q", texts)
	}
	out, ok := replaceJSON(raw, line, `export KEY="[REDACTED]" && go <b>`)
	want := `{"type":"user","message":{"content":"run:\nexport KEY=\"[REDACTED]\" && go <b>"},"cwd":"/x"}` + "\n"
	if !ok || string(out) != want {
		t.Errorf("ok=%v\n got %s\nwant %s", ok, out, want)
	}
	if _, ok := replaceJSON(raw, "not in the line", Mask); ok {
		t.Error("replace of absent text reported ok")
	}
}
