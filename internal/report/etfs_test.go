package report

import (
	"math"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/models"
	"stock-portfolio/internal/news"
)

func etfCandles(end time.Time, step time.Duration, closes ...float64) []chartcalc.Candle {
	out := make([]chartcalc.Candle, len(closes))
	for i, c := range closes {
		out[i] = chartcalc.Candle{Time: end.Add(-time.Duration(len(closes)-1-i) * step).Unix(), Close: c}
	}
	return out
}

func TestETFPeriodRSIAndMissingData(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	// Wilder's initial 14-period RSI for this example is 70.464...
	candles := etfCandles(now.Add(-24*time.Hour), 24*time.Hour, 44.34, 44.09, 44.15, 43.61, 44.33, 44.83, 45.10, 45.42, 45.84, 46.08, 45.89, 46.03, 45.61, 46.28, 46.28)
	p := etfPeriod(candles, now, 7)
	if p.RSI != "70.5" || p.Change != "+0.00%" || p.Date != "03/11" {
		t.Fatalf("incorrect daily metrics: %+v", p)
	}
	short := etfPeriod(candles[len(candles)-2:], now, 7)
	if short.Change != "+0.00%" || short.RSI != "Indisponible" {
		t.Fatalf("insufficient RSI should preserve variation: %+v", short)
	}
	for _, bad := range [][]chartcalc.Candle{nil, etfCandles(now.AddDate(0, 0, -8), 24*time.Hour, 10, 11), etfCandles(now.Add(time.Hour), 24*time.Hour, 10, 11), etfCandles(now, 24*time.Hour, 10, math.NaN()), etfCandles(now, 24*time.Hour, 0, 11)} {
		p := etfPeriod(bad, now, 7)
		if p.rsi != nil || p.change != nil || p.Change != "Indisponible" {
			t.Fatalf("invalid history displayed: %+v", p)
		}
	}
}

func TestETFWeeklyExcludesCurrentWeek(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	monday := time.Date(2026, 11, 2, 9, 30, 0, 0, loc)
	weekly := etfCandles(monday, 7*24*time.Hour, 100, 110, 220)
	s := ETFSeries{Weekly: weekly, WeeklyTimezone: loc.String(), WeeklySessionEnd: time.Date(2026, 11, 6, 16, 0, 0, 0, loc)}
	cases := []struct {
		name string
		now  time.Time
		want int
	}{
		{"Monday before open", monday.Add(-2 * time.Hour), 2},
		{"Wednesday", monday.AddDate(0, 0, 2), 2},
		{"Friday before close", s.WeeklySessionEnd.Add(-time.Minute), 2},
		{"Friday after close", s.WeeklySessionEnd.Add(time.Minute), 3},
		{"Saturday", monday.AddDate(0, 0, 5), 3},
		{"Sunday", monday.AddDate(0, 0, 6), 3},
		{"next Monday", monday.AddDate(0, 0, 7), 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := completedETFWeeks(s, tc.now)
			if len(c) != tc.want {
				t.Fatalf("got %d completed weeks, want %d", len(c), tc.want)
			}
		})
	}
	p := etfPeriod(completedETFWeeks(s, monday.AddDate(0, 0, 2)), monday.AddDate(0, 0, 2), 14)
	if p.Change != "+10.00%" {
		t.Fatalf("weekly used incomplete week's +100%%: %+v", p)
	}
	// ISO year rollover must retain the preceding year's last completed week.
	s.Weekly = etfCandles(time.Date(2027, 1, 4, 14, 30, 0, 0, time.UTC), 7*24*time.Hour, 100, 110, 220)
	if len(completedETFWeeks(s, time.Date(2027, 1, 6, 12, 0, 0, 0, time.UTC))) != 2 {
		t.Fatal("ISO year rollover")
	}
}

func TestETFStatesAndRebound(t *testing.T) {
	for _, tc := range []struct {
		d, w float64
		reb  bool
		want string
	}{
		{76, 79, false, "Surchauffe"}, {71, 65, false, "Tendu"}, {48, 45, false, "Neutre"}, {38, 32, false, "Délaissé"},
		{35, 65, false, "Mixte"}, {65, 75, false, "Mixte"}, {35, 32, true, "Rebond potentiel"}, {70, 40, false, "Neutre"},
	} {
		got, _ := etfState(ETFPeriodData{rsi: &tc.d, rebounding: tc.reb}, ETFPeriodData{rsi: &tc.w})
		if got != tc.want {
			t.Errorf("%+v: got %s", tc, got)
		}
	}
	if got, _ := etfState(ETFPeriodData{}, ETFPeriodData{}); got != "Indisponible" {
		t.Fatal("missing RSI is not neutral")
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	closes := []float64{100, 99, 98, 97, 96, 95, 94, 93, 92, 91, 90, 89, 88, 87, 86, 88}
	p := etfPeriod(etfCandles(now, 24*time.Hour, closes...), now, 7)
	if !p.rebounding {
		t.Fatal("rising RSI from weakness and positive daily return should flag rebound")
	}
	closes[len(closes)-1] = 85
	if etfPeriod(etfCandles(now, 24*time.Hour, closes...), now, 7).rebounding {
		t.Fatal("falling daily price flagged rebound")
	}
}

func TestETFWeeklyDateUsesExchangeTimezone(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	// Yahoo's Swiss weekly candle starts Monday locally, Sunday in UTC.
	weekly := []chartcalc.Candle{
		{Time: time.Date(2026, 10, 19, 0, 0, 0, 0, loc).Unix(), Close: 100},
		{Time: time.Date(2026, 10, 26, 0, 0, 0, 0, loc).Unix(), Close: 110},
		{Time: time.Date(2026, 11, 2, 0, 0, 0, 0, loc).Unix(), Close: 120},
	}
	rows := BuildETFOverview([]models.Stock{{Ticker: "WHEA.SW", Category: "ETFs"}}, map[string]ETFSeries{
		"WHEA.SW": {Weekly: weekly, WeeklyTimezone: loc.String()},
	}, now)
	if rows[0].Weekly.Date != "Sem. du 26/10" || rows[0].Weekly.Change != "+10.00%" || rows[0].Daily.Change != "Indisponible" {
		t.Fatalf("weekly date or independent daily availability incorrect: %+v", rows[0])
	}
}

func TestETFOverviewSelectionOrderingAndTemplate(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	inPortfolio := false
	stocks := []models.Stock{{Ticker: "LOW", Name: "Low <ETF>", Category: "ETFs", InPortfolio: &inPortfolio}, {Ticker: "MISSING", Category: "ETFs"}, {Ticker: "HIGH", Category: "ETFs"}, {Ticker: "OTHER", Category: "Others"}, {Ticker: "HIGH", Category: "ETFs"}}
	up, down := []float64{}, []float64{}
	for i := 0; i < 16; i++ {
		up = append(up, 100+float64(i))
		down = append(down, 100-float64(i))
	}
	s := map[string]ETFSeries{}
	for ticker, closes := range map[string][]float64{"HIGH": up, "LOW": down} {
		s[ticker] = ETFSeries{Daily: etfCandles(now.Add(-24*time.Hour), 24*time.Hour, closes...), Weekly: etfCandles(time.Date(2026, 11, 2, 14, 30, 0, 0, time.UTC), 7*24*time.Hour, closes...)}
	}
	rows := BuildETFOverview(stocks, s, now)
	if len(rows) != 3 || rows[0].Ticker != "HIGH" || rows[1].Ticker != "LOW" || rows[2].Ticker != "MISSING" {
		t.Fatalf("unexpected selection/order: %+v", rows)
	}
	if rows[0].State != "Surchauffe" || rows[1].State != "Délaissé" || rows[2].State != "Indisponible" {
		t.Fatalf("unexpected states: %+v", rows)
	}
	g, err := NewGenerator(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.ShowPositions = false
	g.ETFOverview = rows
	content, err := g.GenerateWithAI(nil, nil, "", nil, nil, &news.Digest{}, &news.Digest{})
	if err != nil {
		t.Fatal(err)
	}
	start, end := strings.Index(content, `<section class="portfolio-news" aria-label="Vue sectorielle ETFs">`), strings.Index(content, `<section class="portfolio-news" aria-label="Veille cryptos">`)
	if start < strings.Index(content, "<h2>Actualités à suivre</h2>") || end <= start {
		t.Fatal("ETF section must follow portfolio news and precede crypto news")
	}
	for _, text := range []string{"Rotation sectorielle", "RSI daily", "RSI weekly", "État", "Sem. du 26/10", "Low &lt;ETF&gt;", "Indisponible"} {
		if !strings.Contains(content[start:end], text) {
			t.Errorf("missing %q", text)
		}
	}
	if strings.Contains(content[start:end], "EMA") {
		t.Fatal("technical alert noise in rotation table")
	}
	g.ETFOverview = nil
	content, err = g.Generate(nil)
	if err != nil || strings.Contains(content, "Vue sectorielle ETFs") {
		t.Fatal("empty ETF section should be omitted")
	}
}
