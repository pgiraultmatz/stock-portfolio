package news

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/models"
)

func TestRankPrioritizesHoldingsAndRejectsUnrelatedOrStaleNews(t *testing.T) {
	now := time.Date(2026, 9, 4, 20, 0, 0, 0, time.UTC)
	watch := false
	stocks := []models.Stock{{Ticker: "ORCL", Name: "Oracle Corporation"}, {Ticker: "MU", Name: "Micron Technology, Inc.", InPortfolio: &watch}, {Ticker: "AI", Name: "C3.ai", InPortfolio: &watch}}
	articles := []Article{
		{Title: "Micron Technology results", URL: "https://example.com/mu", PublishedAt: now},
		{Title: "Data center spending grows", URL: "https://example.com/ai", PublishedAt: now},
		{Title: "Oracle raises outlook", URL: "https://example.com/orcl?utm_source=rss", PublishedAt: now.Add(-time.Hour)},
		{Title: "Oracle raises outlook", URL: "https://example.com/syndication", PublishedAt: now.Add(-2 * time.Hour)},
		{Title: "Oracle old results", URL: "https://example.com/old", PublishedAt: now.Add(-100 * time.Hour)},
		{Title: "Oracle future results", URL: "https://example.com/future", PublishedAt: now.Add(time.Hour)},
		{Title: "AI is everywhere", URL: "https://example.com/unrelated", PublishedAt: now},
	}
	got := Rank(articles, stocks, Config{Themes: []Theme{{Name: "Infrastructure", Keywords: []string{"data center"}}}}, now)
	if len(got) != 3 {
		t.Fatalf("expected 3 relevant articles, got %#v", got)
	}
	for i, article := range got {
		if article.Priority != []int{1, 2, 0}[i] {
			t.Errorf("item %d priority = %d", i, article.Priority)
		}
	}
	if got[2].Tickers[0] != "ORCL" || got[0].Tickers[0] != "MU" {
		t.Fatal("incorrect company matches")
	}
	limited := Rank(articles, stocks, Config{MaxArticles: 1}, now)
	if len(limited) != 1 || limited[0].Tickers[0] != "ORCL" {
		t.Fatal("chronological display changed portfolio selection priority")
	}
}

func TestRankDisplaysNewestFirstForStocksAndCrypto(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	watch := false
	for _, names := range [][2]string{{"Oracle", "Micron"}, {"Bitcoin", "Ethereum"}} {
		t.Run(names[0], func(t *testing.T) {
			stocks := []models.Stock{{Ticker: "HELD", Name: names[0]}, {Ticker: "WATCH", Name: names[1], InPortfolio: &watch}}
			articles := []Article{
				{Title: names[0] + " results", URL: "https://example.com/old", PublishedAt: now.Add(-2 * time.Hour)},
				{Title: "Industry update", Summary: names[0], URL: "https://example.com/middle", PublishedAt: now.Add(-time.Hour)},
				{Title: names[1] + " news", URL: "https://example.com/new", PublishedAt: now},
			}
			got := Rank(articles, stocks, Config{}, now)
			if len(got) != 3 {
				t.Fatalf("unexpected article count: %d", len(got))
			}
			for i, want := range []string{"https://example.com/new", "https://example.com/middle", "https://example.com/old"} {
				if got[i].URL != want {
					t.Errorf("article %d: got %s, want %s", i, got[i].URL, want)
				}
			}
		})
	}
}

func TestRankLimitsPerStockAndDeduplicatesTrackingURLs(t *testing.T) {
	now := time.Now()
	stocks := []models.Stock{{Ticker: "ORCL", Name: "Oracle"}, {Ticker: "ADBE", Name: "Adobe"}}
	articles := []Article{
		{Title: "Oracle news", URL: "https://example.com/1?utm_source=x", PublishedAt: now},
		{Title: "Oracle alternate title", URL: "https://example.com/1?.tsrc=rss", PublishedAt: now.Add(-time.Minute)},
		{Title: "Oracle earnings", URL: "https://example.com/2", PublishedAt: now.Add(-2 * time.Minute)},
		{Title: "Oracle and Adobe news", URL: "https://example.com/3", PublishedAt: now.Add(-3 * time.Minute)},
		{Title: "Adobe results", URL: "https://example.com/4", PublishedAt: now.Add(-4 * time.Minute)},
	}
	got := Rank(articles, stocks, Config{MaxPerStock: 2}, now)
	if len(got) != 3 || got[1].Title != "Oracle earnings" || got[2].Title != "Adobe results" {
		t.Fatalf("unexpected selection: %#v", got)
	}
}

func TestRankPrefersDirectCompanyHeadlineOverIncidentalMention(t *testing.T) {
	now := time.Now()
	articles := []Article{
		{Title: "IonQ board appointment", Summary: "Previously worked at Oracle", URL: "https://example.com/ionq", PublishedAt: now},
		{Title: "Oracle results", URL: "https://example.com/oracle", PublishedAt: now.Add(-time.Hour)},
	}
	got := Rank(articles, []models.Stock{{Ticker: "ORCL", Name: "Oracle Corporation"}}, Config{MaxArticles: 1}, now)
	if len(got) != 1 || got[0].Title != "Oracle results" {
		t.Fatalf("incidental mention ranked before direct news: %#v", got)
	}
}

func TestCompanyMatching(t *testing.T) {
	for _, tc := range []struct {
		text    string
		stock   models.Stock
		aliases []string
		want    bool
	}{
		{"Growth in AI", models.Stock{Ticker: "AI", Name: "C3.ai"}, nil, false},
		{"Buy $PL.", models.Stock{Ticker: "PL", Name: "Planet Labs PBC"}, nil, true},
		{"$PLTR earnings", models.Stock{Ticker: "PL", Name: "Planet Labs PBC"}, nil, false},
		{"Vusion launches product", models.Stock{Ticker: "VU.PA", Name: "Vusion S.A."}, nil, true},
		{"Louis Vuitton opens stores", models.Stock{Ticker: "MC.PA", Name: "LVMH"}, []string{"Louis Vuitton"}, true},
	} {
		if got := mentions(tc.text, tc.stock, tc.aliases); got != tc.want {
			t.Errorf("%q: got %v want %v", tc.text, got, tc.want)
		}
	}
}

func TestParseRSSValidatesDatesLinksAndExtractsText(t *testing.T) {
	feed := `<rss><channel>
<item><title>Oracle &amp; Adobe</title><description><![CDATA[<p>Revenue <b>grows</b> &amp; margins improve.</p>]]></description><link>https://example.com/1?.tsrc=rss</link><pubDate>Fri, 04 Sep 2026 18:00:00 +0000</pubDate><source>Publisher</source></item>
<item><title>Undated</title><link>https://example.com/2</link><pubDate>invalid</pubDate></item>
<item><title>Unsafe</title><link>javascript:alert(1)</link><pubDate>Fri, 04 Sep 2026 18:00:00 +0000</pubDate></item>
</channel></rss>`
	got, err := ParseRSS(strings.NewReader(feed))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 valid item, got %#v", got)
	}
	if got[0].Title != "Oracle & Adobe" || got[0].Summary != "Revenue grows & margins improve." || got[0].URL != "https://example.com/1" || got[0].Source != "Publisher" {
		t.Fatalf("unexpected article: %#v", got[0])
	}
	if _, err := ParseRSS(strings.NewReader("<html>blocked</html>")); err == nil {
		t.Fatal("HTML must not be accepted as RSS")
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCollectKeepsSuccessfulFeedsOnFailure(t *testing.T) {
	client := NewClient()
	client.HTTP.Transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		code, body := 503, "unavailable"
		if r.URL.Query().Get("s") == "ORCL" {
			code = 200
			body = `<rss><channel><item><title>Oracle results</title><link>https://example.com/orcl</link><pubDate>Fri, 04 Sep 2026 18:00:00 GMT</pubDate></item></channel></rss>`
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	digest := client.Collect(context.Background(), []models.Stock{{Ticker: "ORCL", Name: "Oracle"}, {Ticker: "ADBE", Name: "Adobe"}}, Config{}, time.Date(2026, 9, 4, 20, 0, 0, 0, time.UTC))
	if digest.FailedFeeds != 1 || digest.TotalFeeds != 2 || len(digest.Articles) != 1 {
		t.Fatalf("unexpected digest: %#v", digest)
	}
}
