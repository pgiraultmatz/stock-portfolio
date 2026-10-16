package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/config"
)

type tweetTestTransport func(*http.Request) (*http.Response, error)

func (f tweetTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTweetCollectionPreservesFailuresAndEmptyAccounts(t *testing.T) {
	t.Setenv("TWITTER_BEARER_TOKEN", "")
	t.Setenv("NITTER_INSTANCES", "")
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	http.DefaultTransport = tweetTestTransport(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		date := time.Now().UTC()
		if r.URL.Path == "/failed/rss" {
			status = http.StatusForbidden
		}
		if r.URL.Path == "/old/rss" {
			date = date.Add(-72 * time.Hour)
		}
		body := fmt.Sprintf(`<rss><channel><item><link>https://nitter.example/user/status/123</link><description>Market update</description><pubDate>%s</pubDate></item></channel></rss>`, date.Format(time.RFC1123Z))
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	cfg := config.DefaultConfig()
	cfg.XGroups = []config.XGroup{{Name: "Market", Accounts: []string{"failed", "old", " @working ", " "}}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	got := fetchAllXGroups(context.Background(), cfg, logger)
	if len(got.Accounts) != 3 || len(got.Groups) != 1 {
		t.Fatalf("expected three account outcomes and one prompt section, got %#v", got)
	}
	if !got.Accounts[0].FetchFailed || got.Accounts[1].FetchFailed || len(got.Accounts[1].Tweets) != 0 {
		t.Fatalf("failed and stale accounts must be distinguished: %#v", got.Accounts)
	}
	if got.Accounts[2].Username != "working" || len(got.Accounts[2].Tweets) != 1 || got.Groups[0].Accounts[0] != "working" {
		t.Fatalf("successful account missing after earlier failures: %#v", got)
	}
	// Prompts are disabled by default, but the report still receives account outcomes.
	if cfg.Report.EnablePrompts {
		t.Fatal("test requires disabled prompts")
	}
}

func TestTweetCollectionDisabledOrUnavailable(t *testing.T) {
	for _, mode := range []string{"disabled", "invalid provider", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.XGroups = []config.XGroup{{Name: "Market", Accounts: []string{"first", "second"}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "disabled":
				cfg.Twitter.Enabled = false
			case "invalid provider":
				cfg.Twitter.Provider = "unknown"
			case "cancelled":
				cancel()
			}
			got := fetchAllXGroups(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if len(got.Groups) != 0 {
				t.Fatal("unexpected prompt content")
			}
			if mode == "disabled" {
				if len(got.Accounts) != 0 {
					t.Fatal("disabled collection must not report failures")
				}
			} else if len(got.Accounts) != 2 || !got.Accounts[0].FetchFailed || !got.Accounts[1].FetchFailed {
				t.Fatalf("each unavailable account must be reported: %#v", got.Accounts)
			}
		})
	}
}
