package barometer

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/yahoo"
)

func dailyFixture(start, end time.Time) []yahoo.DailyBar {
	var bars []yahoo.DailyBar
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		// Constant intra-month prices and exactly 1% growth between months.
		price := 100 * math.Pow(1.01, float64((d.Year()-2010)*12+int(d.Month())-1))
		bars = append(bars, yahoo.DailyBar{Date: d, Open: price / 1.002, Close: price})
	}
	return bars
}

func TestStatisticalBiasThresholds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		returns []float64
		minimum int
		label   string
	}{
		{"60 percent", []float64{1, 1, 1, -0.1, -0.1}, 5, "Favorable"},
		{"40 percent", []float64{0.1, 0.1, -1, -1, -1}, 5, "Défavorable"},
		{"negative mean", []float64{1, 1, 1, -10, -10}, 5, "Neutre"},
		{"flat median", []float64{1, 0, 0, 0, -0.1}, 5, "Neutre"},
		{"insufficient", []float64{1, 1}, 8, "Indisponible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var values []observation
			for _, r := range tc.returns {
				values = append(values, observation{testTime("2025-10-31T16:00:00-04:00"), r})
			}
			s := summarize("test", values, tc.minimum, 15)
			if s.Bias.Label != tc.label || s.Positive+s.Negative+s.Flat != s.Count {
				t.Fatalf("%+v", s)
			}
		})
	}
	a, b := Bias{"Favorable", "positive"}, Bias{"Défavorable", "negative"}
	if concordance(a, b).Label != "Neutre" || concordance(a, unavailableBias()).Label != "Indisponible" {
		t.Fatal("invalid aggregation")
	}
}

func TestWeekdayHistoryNoLookaheadOrGapBridging(t *testing.T) {
	now := testTime("2026-10-12T10:00:00-04:00")
	bars := dailyFixture(testTime("2026-06-01T09:30:00-04:00"), now)
	baseline := weekdayStatistics("Nasdaq", bars, now)
	if baseline.Count != 8 || baseline.OpenCount != 8 || math.Abs(baseline.OpenMean-.2) > 1e-9 || !strings.HasSuffix(baseline.Period, "05/10/2026") {
		t.Fatalf("%+v", baseline)
	}
	// Today's partial candle cannot enter the backtest even after the close.
	bars[len(bars)-1].Close = 99999
	if got := weekdayStatistics("Nasdaq", bars, now.Add(10*time.Hour)); !reflect.DeepEqual(got, baseline) {
		t.Fatalf("lookahead: %+v", got)
	}
	// A missing Friday close invalidates Monday's C-to-C, never bridges to Thursday.
	for i := range bars {
		if bars[i].Date.Format("2006-01-02") == "2026-10-02" {
			bars[i].Close = 0
		}
	}
	got := weekdayStatistics("Nasdaq", bars, now)
	if got.Count != 8 || strings.HasSuffix(got.Period, "05/10/2026") {
		t.Fatalf("gap bridged: %+v", got)
	}
	// Opening-price gaps affect only the intraday coverage.
	for i := range bars {
		bars[i].Open = 0
	}
	got = weekdayStatistics("Nasdaq", bars, now)
	if got.Count != 8 || got.OpenCount != 0 {
		t.Fatalf("%+v", got)
	}
	// A holiday has no bar; Monday still compares with the last actual session.
	for i := range bars {
		if bars[i].Date.Format("2006-01-02") == "2026-09-04" {
			bars = append(bars[:i], bars[i+1:]...)
			break
		}
	}
	if got := weekdayStatistics("Nasdaq", bars, now); got.Count != 8 {
		t.Fatalf("holiday: %+v", got)
	}
}

func TestMonthlyHistoryAndCoverage(t *testing.T) {
	now := testTime("2026-10-12T10:00:00-04:00")
	bars := dailyFixture(testTime("2010-01-01T09:30:00-05:00"), now)
	s := monthlyStatistics("SOXX", bars, now)
	if s.Count != 15 || s.Positive != 15 || math.Abs(s.Mean-1) > 1e-9 || math.Abs(s.Median-1) > 1e-9 || s.Bias.Label != "Favorable" || !strings.HasSuffix(s.Period, "31/10/2025") {
		t.Fatalf("%+v", s)
	}
	// Reject a month containing an explicitly missing close.
	for i := range bars {
		if bars[i].Date.Format("2006-01-02") == "2025-10-15" {
			bars[i].Close = 0
		}
	}
	s = monthlyStatistics("SOXX", bars, now)
	if s.Count != 15 || strings.HasSuffix(s.Period, "31/10/2025") {
		t.Fatalf("incomplete month: %+v", s)
	}
	short := dailyFixture(testTime("2016-01-01T09:30:00-05:00"), now)
	if got := monthlyStatistics("SOXX", short, now); got.Count != 10 || got.Bias.Label != "Favorable" {
		t.Fatalf("%+v", got)
	}
	short = dailyFixture(testTime("2017-01-01T09:30:00-05:00"), now)
	if got := monthlyStatistics("SOXX", short, now); got.Count != 9 || got.Bias.Label != "Indisponible" {
		t.Fatalf("%+v", got)
	}
	values := []observation{{testTime("2025-10-31T16:00:00-04:00"), 3}, {testTime("2024-10-31T16:00:00-04:00"), -5}, {testTime("2023-10-31T16:00:00-04:00"), 1}}
	extremes := summarize("test", values, 3, 15)
	if extremes.Best != 3 || extremes.BestYear != 2025 || extremes.Worst != -5 || extremes.WorstYear != 2024 || extremes.Median != 1 {
		t.Fatalf("%+v", extremes)
	}
	// January uses December of the preceding year as its reference.
	jan := testTime("2026-01-12T10:00:00-05:00")
	if got := monthlyStatistics("SOXX", dailyFixture(testTime("2009-12-01T09:30:00-05:00"), jan), jan); got.Count != 15 || math.Abs(got.Mean-1) > 1e-9 {
		t.Fatalf("January: %+v", got)
	}
}

func TestSeasonalityIndependentOfSOXX(t *testing.T) {
	now := testTime("2026-10-12T10:00:00-04:00")
	nasdaq := dailyFixture(testTime("2010-01-01T09:30:00-05:00"), now)
	soxx := append([]yahoo.DailyBar(nil), nasdaq...)
	for i := range soxx {
		soxx[i].Close = 10000 / soxx[i].Close
	}
	for _, bars := range [][]yahoo.DailyBar{nil, soxx} {
		c := EvaluateContext(nil, nil, map[string][]yahoo.DailyBar{"^NDX": nasdaq, "SOXX": bars}, now)
		if c.Monthly.Bias.Label != "Favorable" || len(c.Monthly.Instruments) != 1 || c.Monthly.Instruments[0].Name != "Nasdaq 100" || len(c.Weekday.Instruments) != 2 {
			t.Fatalf("SOXX changed Nasdaq seasonality: %+v", c)
		}
	}
}

func TestSectorContextAndMainScoreIsolation(t *testing.T) {
	now := testTime("2026-10-12T10:00:00-04:00")
	quotes := testQuotes(now, .4, .4, -4, 0, 0)
	main := Evaluate(quotes, now)
	for _, tc := range []struct {
		change float64
		label  string
	}{{.1, "Favorable"}, {-.4, "Neutre"}, {0, "Neutre"}} {
		quotes["SOXX"] = yahoo.MarketQuote{Price: 100 * (1 + tc.change/100), PreviousClose: 100, AsOf: now}
		c := EvaluateContext(main, quotes, nil, now)
		if c.Sector.Label != tc.label || c.Weekday.Bias.Label != "Indisponible" {
			t.Fatalf("%+v", c)
		}
		if got := Evaluate(quotes, now); !reflect.DeepEqual(got, main) {
			t.Fatal("supplemental quote changed score")
		}
	}
	quotes["SOXX"] = yahoo.MarketQuote{Price: 101, PreviousClose: 100, AsOf: now.Add(-2 * time.Hour)}
	if c := EvaluateContext(main, quotes, nil, now); c.Sector.Label != "Indisponible" {
		t.Fatal("stale sector signal")
	}
	quotes["NQ=F"] = yahoo.MarketQuote{Price: 99.9, PreviousClose: 100, AsOf: now}
	quotes["SOXX"] = quotes["NQ=F"]
	if c := EvaluateContext(main, quotes, nil, now); c.Sector.Label != "Défavorable" {
		t.Fatal("negative threshold")
	}
	weekend := EvaluateContext(main, quotes, nil, testTime("2026-10-11T10:00:00-04:00"))
	if weekend.Weekday.Note == "" || len(weekend.Weekday.Instruments) != 0 {
		t.Fatal("weekend must not substitute Monday")
	}
	// 01:00 UTC is still Monday in New York, across the DST boundary.
	c := EvaluateContext(main, nil, nil, testTime("2026-11-03T01:00:00Z"))
	if c.Weekday.Title != "Backtest du lundi" || c.Monthly.Title != "Saisonnalité de novembre" {
		t.Fatalf("%+v", c)
	}
}
