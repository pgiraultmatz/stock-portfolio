package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	anthropicBaseURL      = "https://api.anthropic.com/v1/messages"
	anthropicDefaultModel = "clau" + "de-sonnet-4-20250514"
)

// AnthropicClient handles communication with the Anthropic API.
type AnthropicClient struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewAnthropicClient creates a new Anthropic API client.
func NewAnthropicClient(apiKey, model string, timeout time.Duration) *AnthropicClient {
	if model == "" {
		model = anthropicDefaultModel
	}
	return &AnthropicClient{
		apiKey:  apiKey,
		baseURL: anthropicBaseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// AnthropicMessage represents a message in the conversation.
type AnthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AnthropicRequest represents an Anthropic API request.
type AnthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []AnthropicMessage `json:"messages"`
}

// AnthropicResponse represents an Anthropic API response.
type AnthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// AnthropicErrorResponse represents an API error.
type AnthropicErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends a completion request to the Anthropic API.
func (c *AnthropicClient) Complete(ctx context.Context, system string, userMessage string, maxTokens int) (string, error) {
	req := AnthropicRequest{
		Model:     c.model,
		MaxTokens: maxTokens,
		System:    system,
		Messages: []AnthropicMessage{
			{Role: "user", Content: userMessage},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp AnthropicErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
		}
		return "", fmt.Errorf("API error: %s - %s", errResp.Error.Type, errResp.Error.Message)
	}

	var apiResp AnthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("unmarshaling response: %w", err)
	}

	if len(apiResp.Content) == 0 {
		return "", fmt.Errorf("empty response from API")
	}

	return apiResp.Content[0].Text, nil
}
