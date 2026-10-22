package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeMessages(t *testing.T, stop string, text string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model        string
			OutputConfig struct {
				Effort string
				Format struct {
					Type   string
					Schema map[string]any
				}
			} `json:"output_config"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "test-key" || req.Model != "claude-haiku-5-5" ||
			req.OutputConfig.Effort != "low" || req.OutputConfig.Format.Type != "json_schema" || req.OutputConfig.Format.Schema == nil {
			t.Errorf("unexpected request %s %+v", r.URL.Path, req)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": req.Model, "stop_reason": stop,
			"content": []any{map[string]any{"type": "text", "text": text}},
			"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
}

func TestAnthropicRedactor(t *testing.T) {
	srv := fakeMessages(t, "end_turn", `{"redacted":[{"id":1,"text":"export K=[REDACTED]"}]}`)
	defer srv.Close()
	got, err := newAnthropic("test-key", srv.URL, defaultAnthropicModel).redact([]redactItem{{0, "ls"}, {1, "export K=sk-proj-DUMMY1234567890abcdef"}})
	if err != nil || len(got) != 1 || got[0] != (redactItem{1, "export K=[REDACTED]"}) {
		t.Fatalf("got %+v err=%v", got, err)
	}

	refuse := fakeMessages(t, "refusal", "")
	defer refuse.Close()
	if _, err := newAnthropic("test-key", refuse.URL, defaultAnthropicModel).redact([]redactItem{{0, "x"}}); !errors.Is(err, errRefused) {
		t.Errorf("refusal: err=%v, want errRefused", err)
	}
}
