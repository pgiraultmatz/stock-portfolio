package twitter

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type rssRoundTripper func(*http.Request) (*http.Response, error)

func (f rssRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestNitterFallsBackAfterNonTweetFeed(t *testing.T) {
	for _, title := range []string{"RSS reader not yet whitelisted!", "Service notice"} {
		t.Run(title, func(t *testing.T) {
			client := NewNitterClient([]string{"https://blocked.example", "https://working.example"})
			calls := 0
			client.httpClient.Transport = rssRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				body := `<rss><channel><title>` + title + `</title><item><link>https://blocked.example/user/rss</link><description>Access required</description><pubDate>Mon, 01 January 1971 00:00:00 GMT</pubDate></item></channel></rss>`
				if r.URL.Host == "working.example" {
					body = `<rss><channel><item><link>https://working.example/user/status/123#n</link><description>Actual tweet</description><pubDate>Fri, 04 Sep 2026 10:00:00 GMT</pubDate></item></channel></rss>`
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			tweets, err := client.GetRecentTweets(context.Background(), "user", 4)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || len(tweets) != 1 || tweets[0].Text != "Actual tweet" {
				t.Fatalf("expected fallback tweet, got calls=%d tweets=%#v", calls, tweets)
			}
		})
	}
}

func TestRSSItemRejectsInvalidDate(t *testing.T) {
	item := rssItem{Link: "https://nitter.example/user/status/123", PubDate: "invalid", Description: "Old tweet"}
	if _, err := item.toTweet(); err == nil {
		t.Fatal("invalid date must not be replaced with the current time")
	}
}
