package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultOpenAIURL   = "https://api.openai.com/v1"
	defaultOpenAIModel = "gpt-6-luna"
	batchItems         = 100
	batchBytes         = 100 << 10
)

const redactPrompt = `You redact secrets. The input is a JSON array of {id, text}: lines from shell history and AI coding-session logs.
A secret is a credential value: API keys, access tokens, passwords, private keys, session cookies, OTP codes, credentials embedded in URLs.
For each item that contains a secret, return {id, text} where text is the input text unchanged except that each secret value is replaced with exactly [REDACTED].
Keep everything else byte-for-byte: variable names, flags, quotes, whitespace.
Omit items with no secret. Not secrets: variable names, usernames, hostnames, file paths, commit hashes, UUIDs, public identifiers, placeholders (<token>, xxx, your-key-here) and variable references ($TOKEN, ${API_KEY}).`

var redactSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{"redacted": map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"id": map[string]string{"type": "integer"}, "text": map[string]string{"type": "string"}},
			"required":             []string{"id", "text"},
			"additionalProperties": false,
		},
	}},
	"required":             []string{"redacted"},
	"additionalProperties": false,
}

type redactItem struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

// redactor sends one batch of snippets and returns only the items the model changed.
type redactor interface {
	redact(items []redactItem) ([]redactItem, error)
}

type openAI struct{ url, key, model string }

var httpClient = &http.Client{Timeout: 3 * time.Minute}

// redact sends one batch and returns only the items the model changed.
func (c *openAI) redact(items []redactItem) ([]redactItem, error) {
	input, _ := json.Marshal(items)
	body, _ := json.Marshal(map[string]any{
		"model":        c.model,
		"reasoning":    map[string]string{"effort": "low"},
		"instructions": redactPrompt,
		"input":        string(input),
		"store":        false, // don't keep these lines on OpenAI's side
		"text":         map[string]any{"format": map[string]any{"type": "json_schema", "name": "redactions", "strict": true, "schema": redactSchema}},
	})
	for attempt := 1; ; attempt++ {
		req, _ := http.NewRequest("POST", c.url+"/responses", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		var data []byte
		if err == nil {
			data, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			if err == nil && resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("openai: %s: %.300s", resp.Status, data)
				if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
					return nil, err // not retryable: bad key, bad model, bad request
				}
			}
		}
		if err == nil {
			return parseResponse(data)
		}
		if attempt == 3 {
			return nil, err
		}
		time.Sleep(time.Duration(attempt*attempt) * 5 * time.Second)
	}
}

func parseResponse(data []byte) ([]redactItem, error) {
	var resp struct {
		Status string
		Output []struct {
			Type    string
			Content []struct{ Type, Text string }
		}
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	if resp.Status != "completed" {
		return nil, fmt.Errorf("openai: response %s", resp.Status)
	}
	var text string
	for _, o := range resp.Output {
		for _, c := range o.Content {
			if o.Type == "message" && c.Type == "output_text" {
				text += c.Text
			}
		}
	}
	return parseRedacted(text)
}

// parseRedacted reads the model's {"redacted": [{id, text}]} answer (same schema for both providers).
func parseRedacted(text string) ([]redactItem, error) {
	var out struct{ Redacted []redactItem }
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("unexpected model output: %w", err)
	}
	return out.Redacted, nil
}
