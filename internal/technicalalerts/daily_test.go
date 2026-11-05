package technicalalerts

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"stock-portfolio/internal/config"
)

func TestRecoverDailyCloseRequiresSameSessionAndTradingRange(t *testing.T) {
	now := time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)
	bar := time.Date(2026, 11, 4, 14, 30, 0, 0, time.UTC).Unix()
	for _, tc := range []struct {
		name   string
		mutate func(*yahooSeriesMeta, *float64, *float64, *float64)
		want   bool
	}{
		{"same session after UTC midnight", func(*yahooSeriesMeta, *float64, *float64, *float64) {}, true},
		{"Paris regular period can already point at tomorrow", func(m *yahooSeriesMeta, o, h, l *float64) {
			m.Timezone = "Europe/Paris"
			m.RegularMarketTime = time.Date(2026, 11, 4, 16, 35, 3, 0, time.UTC).Unix()
			m.Period.Regular.End = now.Add(24 * time.Hour).Unix()
		}, true},
		{"older quote", func(m *yahooSeriesMeta, o, h, l *float64) { m.RegularMarketTime -= 86400 }, false},
		{"newer session", func(m *yahooSeriesMeta, o, h, l *float64) { m.RegularMarketTime += 86400 }, false},
		{"future quote same date", func(m *yahooSeriesMeta, o, h, l *float64) {
			m.Timezone = "America/Los_Angeles"
			m.RegularMarketTime = now.Add(time.Hour).Unix()
		}, false},
		{"missing quote timestamp", func(m *yahooSeriesMeta, o, h, l *float64) { m.RegularMarketTime = 0 }, false},
		{"outside range", func(m *yahooSeriesMeta, o, h, l *float64) { m.RegularMarketPrice = 110 }, false},
		{"float32 high rounding", func(m *yahooSeriesMeta, o, h, l *float64) { m.RegularMarketPrice = 105; *h = 104.999999 }, true},
		{"zero quote", func(m *yahooSeriesMeta, o, h, l *float64) { m.RegularMarketPrice = 0 }, false},
		{"infinite quote", func(m *yahooSeriesMeta, o, h, l *float64) { m.RegularMarketPrice = math.Inf(1) }, false},
		{"invalid range", func(m *yahooSeriesMeta, o, h, l *float64) { *l = 106 }, false},
		{"invalid open", func(m *yahooSeriesMeta, o, h, l *float64) { *o = math.NaN() }, false},
		{"unknown timezone", func(m *yahooSeriesMeta, o, h, l *float64) { m.Timezone = "Not/AZone" }, false},
		{"missing timezone", func(m *yahooSeriesMeta, o, h, l *float64) { m.Timezone = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := yahooSeriesMeta{RegularMarketPrice: 104, RegularMarketTime: time.Date(2026, 11, 4, 21, 0, 0, 0, time.UTC).Unix(), Timezone: "America/New_York"}
			o, h, l := 100.0, 105.0, 99.0
			tc.mutate(&meta, &o, &h, &l)
			c, ok := recoverDailyClose(bar, &o, &h, &l, meta, now)
			if ok != tc.want {
				t.Fatalf("recovery=%v, want %v", ok, tc.want)
			}
			if ok && (c.Time != bar || c.Open != o || c.Close != meta.RegularMarketPrice || c.High < c.Close || c.Low > c.Close) {
				t.Fatalf("invalid recovered candle: %+v", c)
			}
		})
	}
}

func TestFetchSeriesRepairsOnlyMissingLastDailyClose(t *testing.T) {
	now := time.Now().UTC()
	bar := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	for _, tc := range []struct {
		name, previous, close, open string
		tf                          Timeframe
		recovered, fail             bool
		want                        float64
	}{
		{"recover", "120", "null", "121", Daily, true, false, 123.5},
		{"preserve actual close", "120", "122", "121", Daily, false, false, 122},
		{"historical hole", "null", "122", "121", Daily, false, true, 0},
		{"missing open", "120", "null", "null", Daily, false, true, 0},
		{"weekly never filled with daily quote", "120", "null", "121", Weekly, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"chart":{"result":[{"meta":{"instrumentType":"ETF","exchangeTimezoneName":"UTC","regularMarketPrice":123.5,"regularMarketTime":%d},"timestamp":[%d,%d],"indicators":{"quote":[{"open":[120,%s],"high":[124,124],"low":[119,120],"close":[%s,%s],"volume":[10,20]}]}}]}}`, bar+60, bar-86400, bar, tc.open, tc.previous, tc.close)
			}))
			defer server.Close()
			s, err := NewClient(config.YahooAPIConfig{BaseURL: server.URL}).FetchSeries(context.Background(), "TEST", tc.tf)
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.fail {
				return
			}
			if s.DailyCloseFromQuote != tc.recovered || len(s.Candles) != 2 || s.Candles[1].Close != tc.want || s.Candles[1].Volume != 20 {
				t.Fatalf("incorrect series: %+v", s)
			}
			if _, err := json.Marshal(s); err != nil {
				t.Fatal(err)
			}
		})
	}
}
