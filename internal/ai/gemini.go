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
	geminiBaseURL      = "https://generativelanguage.googleapis.com/v1beta/models"
	geminiDefaultModel = "gemini-2.0-flash"
)

// GeminiClient handles communication with the Gemini API.
type GeminiClient struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewGeminiClient creates a new Gemini API client.
func NewGeminiClient(apiKey, model string, timeout time.Duration) *GeminiClient {
	if model == "" {
		model = geminiDefaultModel
	}
	return &GeminiClient{
		apiKey:  apiKey,
		baseURL: geminiBaseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// GeminiRequest represents a Gemini API request.
type GeminiRequest struct {
	Contents         []GeminiContent   `json:"contents"`
	SystemInstruct   *GeminiContent    `json:"systemInstruction,omitempty"`
	GenerationConfig *GenerationConfig `json:"generationConfig,omitempty"`
}

// GeminiContent represents content in a Gemini request/response.
type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart represents a part of content.
type GeminiPart struct {
	Text string `json:"text"`
}

// GenerationConfig contains generation parameters.
type GenerationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
}

// GeminiResponse represents a Gemini API response.
type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
			Role string `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	Error *GeminiError `json:"error,omitempty"`
}

// GeminiError represents an API error.
type GeminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// Complete sends a completion request to Gemini.
func (c *GeminiClient) Complete(ctx context.Context, system string, userMessage string, maxTokens int) (string, error) {
	req := GeminiRequest{
		Contents: []GeminiContent{
			{
				Role: "user",
				Parts: []GeminiPart{
					{Text: userMessage},
				},
			},
		},
		GenerationConfig: &GenerationConfig{
			MaxOutputTokens: maxTokens,
			Temperature:     0.7,
		},
	}

	// Add system instruction if provided
	if system != "" {
		req.SystemInstruct = &GeminiContent{
			Parts: []GeminiPart{
				{Text: system},
			},
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	// Build URL: https://generativelanguage.googleapis.com/v1beta/models/gemini-1.5-flash:generateContent?key=API_KEY
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", c.baseURL, c.model, c.apiKey)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	var apiResp GeminiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("unmarshaling response: %w", err)
	}

	// Check for API error
	if apiResp.Error != nil {
		return "", fmt.Errorf("API error (%d): %s", apiResp.Error.Code, apiResp.Error.Message)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Extract text from response
	if len(apiResp.Candidates) == 0 {
		return "", fmt.Errorf("empty response from API")
	}

	if len(apiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content in response")
	}

	return apiResp.Candidates[0].Content.Parts[0].Text, nil
}
