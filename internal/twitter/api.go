package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// APIClient fetches tweets via the official Twitter API v2.
// Requires a Bearer Token from the Twitter Developer Portal.
type APIClient struct {
	bearerToken string
	httpClient  *http.Client
}

// NewAPIClient creates a new Twitter API v2 client.
func NewAPIClient(bearerToken string) *APIClient {
	return &APIClient{
		bearerToken: bearerToken,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

// GetRecentTweets fetches the last `count` original tweets (no retweets, no replies).
func (c *APIClient) GetRecentTweets(ctx context.Context, username string, count int) ([]Tweet, error) {
	userID, err := c.getUserID(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("getting user ID for %q: %w", username, err)
	}
	return c.getTweets(ctx, userID, count)
}

func (c *APIClient) getUserID(ctx context.Context, username string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.twitter.com/2/users/by/username/"+username, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearerToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("twitter API status %d for user lookup", resp.StatusCode)
	}

	var result struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
		Errors []struct {
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Data.ID == "" {
		if len(result.Errors) > 0 {
			return "", fmt.Errorf("twitter API error: %s", result.Errors[0].Detail)
		}
		return "", fmt.Errorf("user %q not found", username)
	}
	return result.Data.ID, nil
}

func (c *APIClient) getTweets(ctx context.Context, userID string, count int) ([]Tweet, error) {
	url := fmt.Sprintf(
		"https://api.twitter.com/2/users/%s/tweets?max_results=%d&tweet.fields=created_at,text&exclude=retweets,replies",
		userID, count,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.bearerToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("twitter API status %d for user timeline", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID        string    `json:"id"`
			Text      string    `json:"text"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	tweets := make([]Tweet, len(result.Data))
	for i, t := range result.Data {
		tweets[i] = Tweet{ID: t.ID, Text: t.Text, CreatedAt: t.CreatedAt}
	}
	return tweets, nil
}
