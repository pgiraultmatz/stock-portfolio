// Package twitter provides clients for fetching recent tweets,
// either via the official Twitter API v2 or via Nitter RSS feeds.
package twitter

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Tweet represents a single tweet.
type Tweet struct {
	ID        string
	Text      string
	CreatedAt time.Time
}

// Fetcher is the common interface for fetching recent tweets.
type Fetcher interface {
	GetRecentTweets(ctx context.Context, username string, count int) ([]Tweet, error)
}

// NewFetcher returns the appropriate Fetcher based on the provider name.
// provider: "api" (Twitter API v2) or "nitter" (Nitter RSS).
// bearerToken is only required for the "api" provider.
// nitterInstances is the ordered list of Nitter instances to try for the "nitter" provider.
func NewFetcher(provider, bearerToken string, nitterInstances []string) (Fetcher, error) {
	switch provider {
	case "api":
		if bearerToken == "" {
			return nil, fmt.Errorf("TWITTER_BEARER_TOKEN is required for provider %q", provider)
		}
		return NewAPIClient(bearerToken), nil
	case "nitter", "":
		instances := nitterInstances
		if len(instances) == 0 {
			instances = []string{"https://nitter.net"}
		}
		return NewNitterClient(instances), nil
	default:
		return nil, fmt.Errorf("unknown twitter provider %q (valid: \"api\", \"nitter\")", provider)
	}
}

// FilterRecent keeps only tweets from today or yesterday (in local time).
func FilterRecent(tweets []Tweet) []Tweet {
	now := time.Now()
	today := now.Truncate(24 * time.Hour)
	yesterday := today.Add(-24 * time.Hour)

	filtered := tweets[:0]
	for _, t := range tweets {
		day := t.CreatedAt.UTC().Truncate(24 * time.Hour)
		if !day.Before(yesterday) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// FormatTweets formats a list of tweets into a prompt section for the AI.
func FormatTweets(username string, tweets []Tweet) string {
	if len(tweets) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, t := range tweets {
		date := t.CreatedAt.Format("02 Jan 2006 15:04")
		sb.WriteString(fmt.Sprintf("%d. [%s]\n%s\n\n", i+1, date, t.Text))
	}
	return sb.String()
}
