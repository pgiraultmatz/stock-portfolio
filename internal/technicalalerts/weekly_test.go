package technicalalerts

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/config"
	"stock-portfolio/internal/decisions"
)

func weeklyTestCandle(date time.Time, close float64) chartcalc.Candle {
	return chartcalc.Candle{Time: date.Unix(), Open: close, High: close + 1, Low: close - 1, Close: close, Volume: 100}
}

func TestFetchWeeklyMergesLiveRowBeforeEvaluation(t *testing.T) {
	for _, tc := range []struct {
		ticker, tz                         string
		previousWeek, previousDay, current float64
	}{
		{"XLV", "America/New_York", 184.87, 184.02, 189.65},
		{"EXA.PA", "Europe/Paris", 125.90, 125.40, 126.00},
	} {
		t.Run(tc.ticker, func(t *testing.T) {
			loc, _ := time.LoadLocation(tc.tz)
			monday := time.Date(2026, 11, 2, 0, 0, 0, 0, loc)
			now := time.Date(2026, 11, 4, 22, 0, 0, 0, time.UTC)
			var weekly, daily []chartcalc.Candle
			for i := 0; i < 60; i++ {
				weekly = append(weekly, weeklyTestCandle(monday.AddDate(0, 0, -7*(60-i)), 100+float64(i)))
				daily = append(daily, weeklyTestCandle(now.AddDate(0, 0, i-59).Add(-8*time.Hour), 100+float64(i)))
			}
			weekly[59] = weeklyTestCandle(monday.AddDate(0, 0, -7), tc.previousWeek)
			weekly = append(weekly, weeklyTestCandle(monday, tc.previousDay), weeklyTestCandle(now.Add(-time.Hour), tc.current))
			daily[58] = weeklyTestCandle(now.Add(-32*time.Hour), tc.previousDay)
			daily[59] = weeklyTestCandle(now.Add(-8*time.Hour), tc.current)
			rawCopy := append([]chartcalc.Candle(nil), weekly...)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				candles := weekly
				if r.URL.Query().Get("interval") == "1d" {
					candles = daily
				} else if r.URL.Query().Get("interval") != "1wk" {
					t.Errorf("unexpected interval: %s", r.URL.RawQuery)
				}
				ts := []int64{}
				open, high, low, close, volume := []float64{}, []float64{}, []float64{}, []float64{}, []int64{}
				for _, c := range candles {
					ts = append(ts, c.Time)
					open = append(open, c.Open)
					high = append(high, c.High)
					low = append(low, c.Low)
					close = append(close, c.Close)
					volume = append(volume, c.Volume)
				}
				json.NewEncoder(w).Encode(map[string]any{"chart": map[string]any{"result": []any{map[string]any{
					"meta": map[string]any{"instrumentType": "ETF", "exchangeTimezoneName": tc.tz}, "timestamp": ts,
					"indicators": map[string]any{"quote": []any{map[string]any{"open": open, "high": high, "low": low, "close": close, "volume": volume}}},
				}}}})
			}))
			defer server.Close()
			client := NewClient(config.YahooAPIConfig{BaseURL: server.URL})
			w, err := client.FetchSeries(context.Background(), tc.ticker, Weekly)
			if err != nil {
				t.Fatal(err)
			}
			d, err := client.FetchSeries(context.Background(), tc.ticker, Daily)
			if err != nil {
				t.Fatal(err)
			}
			if len(w.Candles) != 61 || !reflect.DeepEqual(d.Candles, daily) {
				t.Fatal("weekly live row not merged or daily changed")
			}
			last := w.Candles[60]
			if last.Time != monday.Unix() || last.Open != tc.previousDay || last.Close != tc.current || last.High != max(tc.previousDay, tc.current)+1 || last.Low != min(tc.previousDay, tc.current)-1 || last.Volume != 200 {
				t.Fatalf("incorrect OHLC aggregation: %+v", last)
			}
			e := decisions.Evaluate(decisions.Input{QuoteType: "ETF", Daily: decisions.Series{Candles: d.Candles, Timezone: d.Timezone}, Weekly: decisions.Series{Candles: w.Candles, Timezone: w.Timezone}}, now)
			if !e.Eligible || e.DailyChange == nil || e.WeeklyChange == nil {
				t.Fatalf("evaluation unavailable: %+v", e)
			}
			wantD, wantW := (tc.current/tc.previousDay-1)*100, (tc.current/tc.previousWeek-1)*100
			if math.Abs(*e.DailyChange-wantD) > 1e-9 || math.Abs(*e.WeeklyChange-wantW) > 1e-9 || math.Abs(*e.WeeklyChange-*e.DailyChange) < 0.01 {
				t.Fatalf("wrong returns D %v W %v", *e.DailyChange, *e.WeeklyChange)
			}
			if !reflect.DeepEqual(weekly, rawCopy) {
				t.Fatal("source mutated")
			}
			again, err := mergeWeeklyQuoteRows(w.Candles, tc.tz)
			if err != nil || !reflect.DeepEqual(again, w.Candles) {
				t.Fatal("normalization is not idempotent")
			}
		})
	}
}

func TestWeeklyQuoteCalendarBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, tz, first, last string
		want                  int
	}{
		{"Paris Monday is Sunday UTC", "Europe/Paris", "2026-11-01T23:00:00Z", "2026-11-04T16:35:03Z", 1},
		{"ISO week spans new year", "America/New_York", "2026-12-28T05:00:00Z", "2027-01-01T21:00:00Z", 1},
		{"holiday Tuesday cannot absorb next Monday", "America/New_York", "2026-09-08T13:30:00Z", "2026-09-14T13:30:00Z", 2},
		{"DST week boundary", "America/New_York", "2026-10-26T04:00:00Z", "2026-11-02T05:00:00Z", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := time.Parse(time.RFC3339, tc.first)
			b, _ := time.Parse(time.RFC3339, tc.last)
			got, err := mergeWeeklyQuoteRows([]chartcalc.Candle{weeklyTestCandle(a, 100), weeklyTestCandle(b, 110)}, tc.tz)
			if err != nil || len(got) != tc.want {
				t.Fatalf("got %d rows, want %d: %v", len(got), tc.want, err)
			}
		})
	}
	now := time.Date(2026, 11, 4, 22, 0, 0, 0, time.UTC)
	a, b := weeklyTestCandle(now.Add(-24*time.Hour), 100), weeklyTestCandle(now, 110)
	bad := b
	bad.Close = math.NaN()
	for _, candles := range [][]chartcalc.Candle{{b, a}, {a, a}, {a, bad}} {
		if _, err := mergeWeeklyQuoteRows(candles, "UTC"); err == nil {
			t.Fatal("invalid candles must not be hidden by merging")
		}
	}
	if _, err := mergeWeeklyQuoteRows([]chartcalc.Candle{a, b}, "Not/AZone"); err == nil {
		t.Fatal("invalid exchange timezone silently accepted")
	}
}
