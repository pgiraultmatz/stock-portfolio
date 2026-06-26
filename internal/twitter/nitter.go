package twitter

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

// NitterClient fetches tweets via a Nitter RSS feed.
// Nitter is an open-source Twitter front-end that exposes RSS feeds without authentication.
// Instance list: https://github.com/zedeus/nitter/wiki/Instances
type NitterClient struct {
	instances  []string
	httpClient *http.Client
}

// NewNitterClient creates a new Nitter RSS client.
// instances is a list of base URLs tried in order until one works.
func NewNitterClient(instances []string) *NitterClient {
	clean := make([]string, len(instances))
	for i, inst := range instances {
		clean[i] = strings.TrimRight(inst, "/")
	}
	return &NitterClient{
		instances:  clean,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetRecentTweets fetches the last `count` tweets from the user's Nitter RSS feed.
// It tries each configured instance in order and returns on the first success.
func (c *NitterClient) GetRecentTweets(ctx context.Context, username string, count int) ([]Tweet, error) {
	var lastErr error
	for _, instance := range c.instances {
		tweets, err := c.fetchFromInstance(ctx, instance, username, count)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", instance, err)
			continue
		}
		return tweets, nil
	}
	return nil, fmt.Errorf("all Nitter instances failed for @%s (last error: %w)", username, lastErr)
}

func (c *NitterClient) fetchFromInstance(ctx context.Context, instance, username string, count int) ([]Tweet, error) {
	url := fmt.Sprintf("%s/%s/rss", instance, username)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var feed rssChannel
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("parsing RSS: %w", err)
	}

	if len(feed.Channel.Items) == 0 {
		return nil, fmt.Errorf("empty feed")
	}

	tweets := make([]Tweet, 0, count)
	for _, item := range feed.Channel.Items {
		if isPinned(item) || isRetweet(item) {
			continue
		}
		t, err := item.toTweet()
		if err != nil {
			continue
		}
		tweets = append(tweets, t)
		if len(tweets) == count {
			break
		}
	}
	return tweets, nil
}

// rssChannel is the top-level RSS envelope.
type rssChannel struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

// rssItem represents a single <item> in the RSS feed.
type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
	// Description contains the full tweet as HTML.
	Description string `xml:"description"`
}

// toTweet converts an RSS item to a Tweet.
func (item rssItem) toTweet() (Tweet, error) {
	t, err := time.Parse(time.RFC1123Z, item.PubDate)
	if err != nil {
		// Fallback: try without timezone offset
		t, err = time.Parse(time.RFC1123, item.PubDate)
		if err != nil {
			t = time.Now()
		}
	}
	return Tweet{
		ID:        item.Link,
		Text:      cleanTweetText(item.Description),
		CreatedAt: t,
	}, nil
}

// isPinned returns true if the RSS item is a pinned tweet (Nitter marks them in the title).
func isPinned(item rssItem) bool {
	return strings.Contains(item.Title, "pinned")
}

// isRetweet returns true if the description starts with an RT marker.
func isRetweet(item rssItem) bool {
	text := strings.TrimSpace(item.Description)
	return strings.HasPrefix(text, "RT ") || strings.Contains(item.Title, "RT @")
}

// cleanTweetText strips HTML tags and normalizes whitespace from a Nitter description field.
func cleanTweetText(raw string) string {
	// Unescape HTML entities (&amp; &lt; etc.)
	s := html.UnescapeString(raw)

	// Strip HTML tags character by character
	var sb strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			sb.WriteRune(r)
		}
	}

	// Normalize whitespace: collapse runs of spaces/newlines
	lines := strings.Split(sb.String(), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}
