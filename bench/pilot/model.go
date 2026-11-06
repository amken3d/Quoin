package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// Usage is the token usage of one model turn. Every category counts toward
// tokens-to-correct: cached input tokens are still tokens the model read.
type Usage struct {
	In, CacheWrite, CacheRead, Out int64
}

func (u Usage) Total() int64 { return u.In + u.CacheWrite + u.CacheRead + u.Out }

func (u Usage) add(v Usage) Usage {
	return Usage{u.In + v.In, u.CacheWrite + v.CacheWrite, u.CacheRead + v.CacheRead, u.Out + v.Out}
}

// Price per million tokens (Claude Opus 5.5): input $4, output $20, cache
// reads $0.20; 5-minute cache writes are billed at 1.25x input.
func (u Usage) USD() float64 {
	return (float64(u.In)*4 + float64(u.CacheWrite)*5 + float64(u.CacheRead)*0.20 + float64(u.Out)*20) / 1e6
}

// Reply is one model turn.
type Reply struct {
	Text      string
	Assistant anthropic.MessageParam // appended to history unchanged
	Usage     Usage
	Stop      string
}

// Provider produces model turns.
type Provider interface {
	Turn(ctx context.Context, key, system string, msgs []anthropic.MessageParam) (Reply, error)
}

// anthropicProvider calls the Messages API with one fixed model and effort.
// Thinking is adaptive (the default, and the only mode on Claude Opus 5.5).
// No refusal fallback is configured: a benchmark run must stay on one model,
// so a refusal is recorded as a failed run instead.
type anthropicProvider struct {
	client anthropic.Client
	model  string
	effort anthropic.OutputConfigEffort
}

func newAnthropic(apiKey, model, effort string) *anthropicProvider {
	return &anthropicProvider{
		client: anthropic.NewClient(option.WithAPIKey(apiKey), option.WithMaxRetries(4)),
		model:  model,
		effort: anthropic.OutputConfigEffort(effort),
	}
}

func (p *anthropicProvider) Turn(ctx context.Context, key, system string, msgs []anthropic.MessageParam) (Reply, error) {
	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: 16000,
		System: []anthropic.TextBlockParam{{
			Text:         system,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages:     msgs,
		OutputConfig: anthropic.OutputConfigParam{Effort: p.effort},
	})
	if err != nil {
		return Reply{}, err
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	return Reply{
		Text:      text.String(),
		Assistant: resp.ToParam(),
		Usage: Usage{In: resp.Usage.InputTokens, CacheWrite: resp.Usage.CacheCreationInputTokens,
			CacheRead: resp.Usage.CacheReadInputTokens, Out: resp.Usage.OutputTokens},
		Stop: string(resp.StopReason),
	}, nil
}

// replayProvider returns scripted replies (for the self-test): the n-th
// call for a run key returns script[key][n]. It spends no tokens.
type replayProvider struct {
	script map[string][]string
	calls  map[string]int
}

func (p *replayProvider) Turn(_ context.Context, key, _ string, _ []anthropic.MessageParam) (Reply, error) {
	n := p.calls[key]
	p.calls[key] = n + 1
	s := p.script[key]
	if n >= len(s) {
		return Reply{}, fmt.Errorf("replay: no reply %d for %s", n, key)
	}
	return Reply{Text: s[n], Assistant: anthropic.NewAssistantMessage(anthropic.NewTextBlock(s[n])), Stop: "end_turn"}, nil
}
