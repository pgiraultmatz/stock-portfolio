package yahoo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"stock-portfolio/internal/config"
)

func TestDailyHistory(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"valid with missing bar", `{"chart":{"result":[{"meta":{"symbol":"SOXX"},"timestamp":[1000,2000,3000],"indicators":{"quote":[{"open":[100,null,102],"close":[101,null,103]}]}}]}}`, 200, true},
		{"misaligned", `{"chart":{"result":[{"meta":{"symbol":"SOXX"},"timestamp":[1000,2000],"indicators":{"quote":[{"open":[100],"close":[101,102]}]}}]}}`, 200, false},
		{"wrong ticker", `{"chart":{"result":[{"meta":{"symbol":"QQQ"},"timestamp":[1000],"indicators":{"quote":[{"open":[100],"close":[101]}]}}]}}`, 200, false},
		{"duplicate date", `{"chart":{"result":[{"meta":{"symbol":"SOXX"},"timestamp":[1000,1000],"indicators":{"quote":[{"open":[100,100],"close":[101,101]}]}}]}}`, 200, false},
		{"empty", `{"chart":{"result":[]}}`, 200, false},
		{"API error", `{"chart":{"error":{"code":"Not Found"}}}`, 200, false},
		{"HTTP error", `{}`, 429, false},
		{"malformed", `{`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.URL.Path != "/SOXX" || q.Get("interval") != "1d" || q.Get("period1") != "500" || q.Get("period2") != "3000" || q.Get("includePrePost") != "false" || q.Has("range") {
					t.Errorf("unexpected request: %s", r.URL)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client := NewClient(config.YahooAPIConfig{BaseURL: server.URL, Timeout: 2, Range: "1y", Interval: "1wk"})
			bars, err := client.GetDailyHistory(context.Background(), "SOXX", time.Unix(500, 0), time.Unix(3000, 0))
			if (err == nil) != tc.valid {
				t.Fatalf("bars=%+v err=%v", bars, err)
			}
			if tc.valid && (len(bars) != 2 || bars[0].Open != 100 || bars[0].Close != 101 || bars[1].Open != 0 || bars[1].Close != 0 || bars[1].Date.Unix() != 2000) {
				t.Fatalf("lost alignment or end bound: %+v", bars)
			}
		})
	}
}

func TestDailyHistoryKeepsSplitAdjustedOHLC(t *testing.T) {
	// Provider OHLC already reflects the split; applying another adjustment would
	// invent a large return. adjclose includes dividends and must not replace close.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"chart":{"result":[{"meta":{"symbol":"SOXX"},"timestamp":[1000,2000],"events":{"splits":{"2000":{"numerator":3,"denominator":1}}},"indicators":{"quote":[{"open":[100,101],"close":[101,102]}],"adjclose":[{"adjclose":[95,96]}]}}]}}`)
	}))
	defer server.Close()
	client := NewClient(config.YahooAPIConfig{BaseURL: server.URL, Timeout: 2})
	bars, err := client.GetDailyHistory(context.Background(), "SOXX", time.Unix(500, 0), time.Unix(3000, 0))
	if err != nil || len(bars) != 2 || bars[0].Close != 101 || bars[1].Close != 102 {
		t.Fatalf("bars=%+v err=%v", bars, err)
	}
}
