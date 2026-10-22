package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const defaultAnthropicModel = "claude-haiku-5-5"

// errRefused: the model's safety classifier declined the batch. Its items are counted as
// rejected instead of failing the run (Haiku has no server-side refusal fallback).
var errRefused = errors.New("model declined the batch")

type anthropicRedactor struct {
	client anthropic.Client
	model  string
}

func newAnthropic(key, baseURL, model string) *anthropicRedactor {
	opts := []option.RequestOption{option.WithAPIKey(key), option.WithMaxRetries(3)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &anthropicRedactor{client: anthropic.NewClient(opts...), model: model}
}

func (c *anthropicRedactor) redact(items []redactItem) ([]redactItem, error) {
	input, _ := json.Marshal(items)
	resp, err := c.client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: 16000,
		System:    []anthropic.TextBlockParam{{Text: redactPrompt}},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffortLow,
			Format: anthropic.JSONOutputFormatParam{Schema: redactSchema},
		},
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(string(input)))},
	})
	if err != nil {
		return nil, err
	}
	switch resp.StopReason {
	case anthropic.StopReasonRefusal:
		return nil, errRefused
	case anthropic.StopReasonMaxTokens:
		return nil, errors.New("anthropic: output hit max_tokens")
	}
	var text string
	for _, b := range resp.Content {
		if t, ok := b.AsAny().(anthropic.TextBlock); ok {
			text += t.Text
		}
	}
	return parseRedacted(text)
}
