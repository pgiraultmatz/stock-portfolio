package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"stock-portfolio/internal/models"
)

// mergeEvaluations preserves financial, calendar and unknown fields verbatim.
// Each ticker's evaluation has its own date; refreshing it never refreshes financials.
func mergeEvaluations(content []byte, evaluations map[string]models.Evaluation) ([]byte, error) {
	root := map[string]json.RawMessage{}
	if len(content) > 0 {
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, err
		}
	}
	if root == nil {
		return nil, fmt.Errorf("invalid stock-data document")
	}
	stocks := map[string]json.RawMessage{}
	if raw, ok := root["stocks"]; ok {
		if err := json.Unmarshal(raw, &stocks); err != nil {
			return nil, err
		}
	}
	if stocks == nil {
		stocks = map[string]json.RawMessage{}
	}
	for ticker, e := range evaluations {
		if !e.Current() {
			return nil, fmt.Errorf("invalid evaluation for %s", ticker)
		}
		fields := map[string]json.RawMessage{}
		if raw, ok := stocks[ticker]; ok {
			if err := json.Unmarshal(raw, &fields); err != nil {
				return nil, err
			}
		}
		if fields == nil {
			return nil, fmt.Errorf("invalid stock entry for %s", ticker)
		}
		var previous models.Evaluation
		if raw, ok := fields["evaluation"]; ok && json.Unmarshal(raw, &previous) == nil && previous.EvaluatedAt.After(e.EvaluatedAt) {
			continue
		}
		signal, note := e.Badge()
		for key, value := range map[string]any{"evaluation": e, "signal": signal, "signal_note": note} {
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			fields[key] = raw
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		stocks[ticker] = raw
	}
	raw, err := json.Marshal(stocks)
	if err != nil {
		return nil, err
	}
	root["stocks"] = raw
	return json.MarshalIndent(root, "", "  ")
}

func SaveEvaluations(ctx context.Context, evaluations map[string]models.Evaluation) error {
	id := os.Getenv("GIST_ID")
	if id == "" {
		return nil
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		return fmt.Errorf("GH_TOKEN is required to save evaluations")
	}
	url := "https://api.github.com/gists/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reading evaluations: HTTP %d", resp.StatusCode)
	}
	var gist struct {
		Files map[string]struct {
			Content   string `json:"content"`
			Truncated bool   `json:"truncated"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return err
	}
	file := gist.Files["stock-data.json"]
	if file.Truncated {
		return fmt.Errorf("stock-data response truncated; refusing to overwrite it")
	}
	content, err := mergeEvaluations([]byte(file.Content), evaluations)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"files": map[string]any{"stock-data.json": map[string]string{"content": string(content)}}})
	if err != nil {
		return err
	}
	patch, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	patch.Header = req.Header.Clone()
	patch.Header.Set("Content-Type", "application/json")
	if etag := resp.Header.Get("ETag"); etag != "" {
		patch.Header.Set("If-Match", etag)
	}
	updated, err := http.DefaultClient.Do(patch)
	if err != nil {
		return err
	}
	defer updated.Body.Close()
	if updated.StatusCode != http.StatusOK {
		return fmt.Errorf("saving evaluations: HTTP %d", updated.StatusCode)
	}
	return nil
}
