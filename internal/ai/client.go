// Package ai provides AI-powered analysis using multiple LLM providers.
package ai

import (
	"context"
	"time"
)

const (
	defaultTimeout = 60 * time.Second
)

// Provider represents an LLM provider type.
type Provider string

const (
	ProviderGemini    Provider = "gemini"
	ProviderAnthropic Provider = "anthropic"
)

// Client interface for LLM providers.
type Client interface {
	Complete(ctx context.Context, system string, userMessage string, maxTokens int) (string, error)
}

// ClientConfig holds configuration for creating a client.
type ClientConfig struct {
	Provider Provider
	APIKey   string
	Model    string
	Timeout  time.Duration
}

// NewClient creates a new AI client based on the provider.
func NewClient(cfg ClientConfig) Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}

	switch cfg.Provider {
	case ProviderAnthropic:
		return NewAnthropicClient(cfg.APIKey, cfg.Model, cfg.Timeout)
	case ProviderGemini:
		fallthrough
	default:
		return NewGeminiClient(cfg.APIKey, cfg.Model, cfg.Timeout)
	}
}
