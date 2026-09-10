package yahoo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"stock-portfolio/internal/config"
)

func TestLiveMarketQuotes(t *testing.T) {
	if os.Getenv("STOCK_TEST_LIVE_BAROMETER") != "1" {
		t.Skip("live Yahoo check is opt-in")
	}
	client := NewClient(config.YahooAPIConfig{BaseURL: "https://query1.finance.yahoo.com/v8/finance/chart", Timeout: 8, UserAgent: "Mozilla/5.0"})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, ticker := range []string{"ES=F", "NQ=F", "^VIX", "BZ=F", "^TNX"} {
		q, err := client.GetMarketQuote(ctx, ticker)
		if err != nil {
			t.Errorf("%s: %v", ticker, err)
			continue
		}
		t.Logf("%s: %.4f (%+.2f%%), reference %.4f, source time %s", ticker, q.Price, q.ChangePercent(), q.PreviousClose, q.AsOf.Format(time.RFC3339))
	}
}

func TestMarketQuote(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"valid metadata without bars", `{"chart":{"result":[{"meta":{"symbol":"^TNX","regularMarketPrice":4.05,"chartPreviousClose":4,"regularMarketTime":1789050000}}]}}`, 200, true},
		{"no previous close", `{"chart":{"result":[{"meta":{"symbol":"^TNX","regularMarketPrice":4.05,"regularMarketTime":1789050000}}]}}`, 200, false},
		{"no timestamp", `{"chart":{"result":[{"meta":{"symbol":"^TNX","regularMarketPrice":4.05,"chartPreviousClose":4}}]}}`, 200, false},
		{"wrong symbol", `{"chart":{"result":[{"meta":{"symbol":"ES=F","regularMarketPrice":4.05,"chartPreviousClose":4,"regularMarketTime":1789050000}}]}}`, 200, false},
		{"no result", `{"chart":{"result":[]}}`, 200, false},
		{"API error", `{"chart":{"error":{"code":"Not Found"}}}`, 200, false},
		{"HTTP error", `{}`, 429, false},
		{"malformed", `{`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/^TNX" || r.URL.Query().Get("range") != "1d" || r.URL.Query().Get("interval") != "5m" {
					t.Errorf("unexpected request: %s", r.URL)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client := NewClient(config.YahooAPIConfig{BaseURL: server.URL, Timeout: 2})
			q, err := client.GetMarketQuote(context.Background(), "^TNX")
			if (err == nil) != tc.valid {
				t.Fatalf("quote=%+v err=%v", q, err)
			}
			if tc.valid && (q.Price != 4.05 || q.PreviousClose != 4 || q.AsOf.Unix() != 1789050000) {
				t.Fatalf("incorrect quote: %+v", q)
			}
		})
	}
}
