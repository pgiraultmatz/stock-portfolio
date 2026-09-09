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

func TestCryptoUniversePreservesHoldingsAndAddsBenchmarks(t *testing.T) {
	companies, crypto := CryptoUniverse([]models.Stock{
		{Ticker: "BTC-USD", Name: "Bitcoin USD", Category: "Other"},
		{Ticker: "LINK-USD", Name: "Chainlink", Category: "Cryptos"},
		{Ticker: "ORCL", Name: "Oracle", Category: "Tech"},
	})
	if len(companies) != 1 || companies[0].Ticker != "ORCL" || len(crypto) != 4 {
		t.Fatalf("unexpected split: %#v %#v", companies, crypto)
	}
	if !crypto[0].IsInPortfolio() || crypto[1].IsInPortfolio() || crypto[2].IsInPortfolio() || !crypto[3].IsInPortfolio() {
		t.Fatal("holding flags were changed or default assets marked as owned")
	}
}

func TestCollectSectionsSeparatesAndDeduplicatesCrypto(t *testing.T) {
	c := NewClient()
	c.HTTP.Transport = roundTripper(func(r *http.Request) (*http.Response, error) {
		items := `<item><title>Oracle uses blockchain</title><link>https://example.com/shared</link><pubDate>Sat, 05 Sep 2026 12:00:00 GMT</pubDate></item>`
		if r.URL.Query().Get("s") == "BTC-USD" {
			items += `<item><title>Bitcoin ETF flows</title><link>https://example.com/btc</link><pubDate>Sat, 05 Sep 2026 11:00:00 GMT</pubDate></item>`
		}
		if r.URL.Query().Get("s") == "ORCL" {
			items += `<item><title>Oracle results</title><link>https://example.com/oracle</link><pubDate>Sat, 05 Sep 2026 10:00:00 GMT</pubDate></item>`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("<rss><channel>" + items + "</channel></rss>")), Header: make(http.Header)}, nil
	})
	stocks := []models.Stock{{Ticker: "ORCL", Name: "Oracle"}, {Ticker: "BTC-USD", Name: "Bitcoin USD", Category: "Cryptos"}}
	now := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	portfolio, crypto := c.CollectSections(context.Background(), stocks, Config{Enabled: true}, DefaultCryptoConfig(), now)
	if portfolio.TotalFeeds != 1 || crypto.TotalFeeds != 3 || len(portfolio.Articles) != 1 || len(crypto.Articles) != 2 {
		t.Fatalf("unexpected sections: %#v %#v", portfolio, crypto)
	}
	if portfolio.Articles[0].Title != "Oracle results" || crypto.Articles[0].Title != "Oracle uses blockchain" || crypto.Articles[1].Title != "Bitcoin ETF flows" || crypto.Articles[1].Priority != 0 {
		t.Fatal("incorrect routing, chronological order or holding priority")
	}
	portfolio, crypto = c.CollectSections(context.Background(), stocks, Config{Enabled: true}, Config{}, now)
	if portfolio.TotalFeeds != 2 || crypto != nil {
		t.Fatal("disabling crypto should preserve existing portfolio coverage")
	}
	portfolio, crypto = c.CollectSections(context.Background(), stocks, Config{}, DefaultCryptoConfig(), now)
	if portfolio != nil || crypto == nil {
		t.Fatal("crypto should work independently of portfolio news")
	}
}
