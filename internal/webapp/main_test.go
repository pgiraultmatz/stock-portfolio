package webapp

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTrimChartResponseKeepsEMA200WarmedFromFullHistory(t *testing.T) {
	candles := make([]ChartCandle, 0, 760)
	start := time.Date(2024, time.May, 20, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 760; i++ {
		close := 100 + float64(i)*0.45 + math.Sin(float64(i)/9)*4
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 0.7,
			High:   close + 1.4,
			Low:    close - 1.6,
			Close:  close,
			Volume: int64(1_000_000 + i*1000),
		})
	}

	fullEMA200 := calcEMALine(candles, 200)
	trimmed := trimChartResponse(ChartResponse{
		Symbol:   "TEST",
		Range:    "2y",
		Interval: "1d",
		Candles:  candles,
	}, "1y", "1d", "ema")

	if len(trimmed.Candles) == 0 {
		t.Fatal("expected visible candles after trim")
	}
	if len(trimmed.EMA200) == 0 {
		t.Fatal("expected EMA200 after trim")
	}

	firstVisibleTime := trimmed.Candles[0].Time
	if trimmed.EMA200[0].Time != firstVisibleTime {
		t.Fatalf("EMA200 should be warmed before trim: first EMA time %d, first visible candle time %d", trimmed.EMA200[0].Time, firstVisibleTime)
	}

	expected, ok := chartLineValueAtOrBefore(fullEMA200, firstVisibleTime)
	if !ok {
		t.Fatal("expected full-history EMA200 at first visible candle")
	}
	if math.Abs(trimmed.EMA200[0].Value-expected) > 0.000001 {
		t.Fatalf("EMA200 should match full-history value: got %.8f want %.8f", trimmed.EMA200[0].Value, expected)
	}
}

func TestChartFetchRangeWarmsWeeklyEMA200(t *testing.T) {
	if got := chartFetchRange("2y", "1wk"); got != "10y" {
		t.Fatalf("expected 10y fetch range for 2y weekly chart, got %q", got)
	}
	if got := chartFetchRange("5y", "1wk"); got != "10y" {
		t.Fatalf("expected 10y fetch range for 5y weekly chart, got %q", got)
	}
}

func TestTrimWeeklyChartResponseKeepsEMA200WarmedFromFullHistory(t *testing.T) {
	candles := make([]ChartCandle, 0, 520)
	start := time.Date(2016, time.May, 20, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 520; i++ {
		close := 100 + float64(i)*0.35 + math.Sin(float64(i)/6)*5
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i*7).Unix(),
			Open:   close - 0.7,
			High:   close + 1.4,
			Low:    close - 1.6,
			Close:  close,
			Volume: int64(1_000_000 + i*1000),
		})
	}

	fullEMA200 := calcEMALine(candles, 200)
	trimmed := trimChartResponse(ChartResponse{
		Symbol:   "TEST",
		Range:    "10y",
		Interval: "1wk",
		Candles:  candles,
	}, "2y", "1wk", "ema")

	if len(trimmed.Candles) == 0 {
		t.Fatal("expected visible candles after trim")
	}
	if len(trimmed.EMA200) == 0 {
		t.Fatal("expected EMA200 after trim")
	}

	firstVisibleTime := trimmed.Candles[0].Time
	if trimmed.EMA200[0].Time != firstVisibleTime {
		t.Fatalf("EMA200 should be warmed before trim: first EMA time %d, first visible candle time %d", trimmed.EMA200[0].Time, firstVisibleTime)
	}

	expected, ok := chartLineValueAtOrBefore(fullEMA200, firstVisibleTime)
	if !ok {
		t.Fatal("expected full-history EMA200 at first visible candle")
	}
	if math.Abs(trimmed.EMA200[0].Value-expected) > 0.000001 {
		t.Fatalf("EMA200 should match full-history value: got %.8f want %.8f", trimmed.EMA200[0].Value, expected)
	}
}

func TestTrimChartResponseSeedsMovingAverageAtFirstVisibleCandle(t *testing.T) {
	start := time.Date(2026, time.July, 12, 21, 0, 0, 0, time.UTC)
	candles := make([]ChartCandle, 0, 8)
	for i := 0; i < 8; i++ {
		close := 100 + float64(i)
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i*7).Unix(),
			Open:   close - 1,
			High:   close + 1,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}

	trimmed := trimChartResponse(ChartResponse{
		Symbol:   "TEST",
		Range:    "5y",
		Interval: "1wk",
		Candles:  candles,
		EMA50: []ChartLinePoint{
			{Time: candles[1].Time, Value: 101.5},
			{Time: candles[4].Time, Value: 104.5},
		},
	}, "1mo", "1wk", "ema")

	if len(trimmed.Candles) == 0 {
		t.Fatal("expected visible candles after trim")
	}
	if len(trimmed.EMA50) == 0 {
		t.Fatal("expected EMA50 after trim")
	}
	if trimmed.EMA50[0].Time != trimmed.Candles[0].Time {
		t.Fatalf("expected EMA50 to start at first visible candle: got %d want %d", trimmed.EMA50[0].Time, trimmed.Candles[0].Time)
	}
	if trimmed.EMA50[0].Value != 101.5 {
		t.Fatalf("expected seeded EMA50 value from previous point, got %.2f", trimmed.EMA50[0].Value)
	}
}

func TestChartEMALineStartsBeforeFullPeriod(t *testing.T) {
	candles := make([]ChartCandle, 0, 20)
	start := time.Date(2026, time.January, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		close := 50 + float64(i)
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i*7).Unix(),
			Open:   close - 1,
			High:   close + 1,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}

	ema200 := calcEMALine(candles, 200)
	if len(ema200) != len(candles) {
		t.Fatalf("expected EMA200 chart line to start before 200 candles: got %d points for %d candles", len(ema200), len(candles))
	}
	if ema200[0].Time != candles[0].Time || ema200[0].Value != candles[0].Close {
		t.Fatalf("expected EMA200 to be seeded from first close, got %+v", ema200[0])
	}
}

func TestChartMACDStartsBeforeFullWarmup(t *testing.T) {
	candles := make([]ChartCandle, 0, 20)
	start := time.Date(2026, time.January, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		close := 100 + float64(i*i)/10
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i*7).Unix(),
			Open:   close - 1,
			High:   close + 1,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}

	macd := calcMACD(candles)
	if len(macd) != len(candles) {
		t.Fatalf("expected chart MACD to start before full warmup: got %d points for %d candles", len(macd), len(candles))
	}
	if macd[0].Time != candles[0].Time || macd[0].MACD != 0 || macd[0].Signal != 0 {
		t.Fatalf("expected MACD to be seeded from first close, got %+v", macd[0])
	}
}

func TestLowerHighsRemainVisibleAfterReclaimButSetupIsInactive(t *testing.T) {
	candles := make([]ChartCandle, 0, 60)
	start := time.Date(2026, time.January, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		close := 80.0
		switch i {
		case 10:
			close = 100
		case 20:
			close = 90
		case 28:
			close = 92
		case 59:
			close = 88
		}
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 1,
			High:   close + 2,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	pivots := []ChartPivot{
		{Time: candles[10].Time, Kind: "high", Price: 100},
		{Time: candles[20].Time, Kind: "high", Price: 90},
	}

	lowerHighs := calcChartPivotStructures(candles, pivots, "lower_high")
	if len(lowerHighs) == 0 {
		t.Fatal("expected lower highs to remain visible after reclaim")
	}

	analysis := calcChartAnalysis(ChartResponse{
		Symbol:     "TEST",
		Range:      "1y",
		Interval:   "1d",
		Candles:    candles,
		Pivots:     pivots,
		LowerHighs: lowerHighs,
	})
	for _, setup := range analysis.Setups {
		if setup.Kind == "lower_highs_pressure" {
			t.Fatalf("expected reclaimed lower-high structure to be inactive, got setup %+v", setup)
		}
	}
}

func TestCalcChartPivotStructuresDetectsAllDirections(t *testing.T) {
	candles := make([]ChartCandle, 0, 70)
	start := time.Date(2026, time.January, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 70; i++ {
		close := 100.0
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 1,
			High:   close + 2,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	pivots := []ChartPivot{
		{Time: candles[10].Time, Kind: "high", Price: 100},
		{Time: candles[15].Time, Kind: "low", Price: 50},
		{Time: candles[20].Time, Kind: "high", Price: 90},
		{Time: candles[25].Time, Kind: "low", Price: 45},
		{Time: candles[30].Time, Kind: "high", Price: 110},
		{Time: candles[35].Time, Kind: "low", Price: 55},
		{Time: candles[40].Time, Kind: "high", Price: 125},
		{Time: candles[45].Time, Kind: "low", Price: 65},
	}

	cases := []string{"lower_high", "lower_low", "higher_high", "higher_low"}
	for _, kind := range cases {
		got := calcChartPivotStructures(candles, pivots, kind)
		if len(got) < 2 {
			t.Fatalf("expected %s structure, got %+v", kind, got)
		}
		if got[len(got)-1].Kind != kind {
			t.Fatalf("expected kind %s, got %s", kind, got[len(got)-1].Kind)
		}
	}
}

func TestCalcChartPivotStructuresDetectsTimeframeATHAsHigherHigh(t *testing.T) {
	candles := make([]ChartCandle, 0, 70)
	start := time.Date(2026, time.January, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 70; i++ {
		close := 650.0
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 1,
			High:   close + 2,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	pivots := []ChartPivot{
		{Time: candles[20].Time, Kind: "high", Price: 688},
		{Time: candles[45].Time, Kind: "high", Price: 698},
	}

	higherHighs := calcChartPivotStructures(candles, pivots, "higher_high")
	if len(higherHighs) != 2 {
		t.Fatalf("expected timeframe ATH to be accepted as higher high below normal threshold, got %+v", higherHighs)
	}
	if higherHighs[1].Price != 698 {
		t.Fatalf("expected 698 ATH to be retained, got %+v", higherHighs[1])
	}
}

func TestCalcChartPivotStructuresPrefersRecentAdjacentLowerPair(t *testing.T) {
	candles := make([]ChartCandle, 0, 90)
	start := time.Date(2026, time.January, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 90; i++ {
		close := 100.0
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 1,
			High:   close + 2,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	highs := []ChartPivot{
		{Time: candles[10].Time, Kind: "high", Price: 100},
		{Time: candles[20].Time, Kind: "high", Price: 120},
		{Time: candles[40].Time, Kind: "high", Price: 140},
		{Time: candles[70].Time, Kind: "high", Price: 130},
	}
	lows := []ChartPivot{
		{Time: candles[12].Time, Kind: "low", Price: 80},
		{Time: candles[22].Time, Kind: "low", Price: 90},
		{Time: candles[42].Time, Kind: "low", Price: 100},
		{Time: candles[72].Time, Kind: "low", Price: 95},
	}

	lowerHighs := calcChartPivotStructures(candles, highs, "lower_high")
	if len(lowerHighs) != 2 || lowerHighs[1].Price != 130 {
		t.Fatalf("expected recent adjacent lower high pair ending at 130, got %+v", lowerHighs)
	}
	lowerLows := calcChartPivotStructures(candles, lows, "lower_low")
	if len(lowerLows) != 2 || lowerLows[1].Price != 95 {
		t.Fatalf("expected recent adjacent lower low pair ending at 95, got %+v", lowerLows)
	}
}

func TestCalcChartPivotStructuresDetectsRecentVOOLowerHighAndLowerLow(t *testing.T) {
	candles := make([]ChartCandle, 0, 70)
	start := time.Date(2026, time.March, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 70; i++ {
		close := 680.0
		high := close + 2
		low := close - 2
		switch i {
		case 35:
			close, high, low = 688, 689.10, 684
		case 38:
			close, high, low = 674, 680, 672.55
		case 49:
			close, high, low = 698, 699.15, 695
		case 52:
			close, high, low = 696, 697, 690
		case 54:
			close, high, low = 684, 692, 676
		case 56:
			close, high, low = 678, 686, 664.32
		case 62:
			close, high, low = 693, 695.75, 691
		case 63:
			close, high, low = 689, 694.57, 688
		case 64:
			close, high, low = 681, 691.53, 679
		}
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 1,
			High:   high,
			Low:    low,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	pivots := []ChartPivot{
		{Time: candles[35].Time, Kind: "high", Price: 689.10},
		{Time: candles[38].Time, Kind: "low", Price: 672.55},
		{Time: candles[49].Time, Kind: "high", Price: 699.15},
		{Time: candles[56].Time, Kind: "low", Price: 664.32},
	}

	lowerLows := calcChartPivotStructures(candles, pivots, "lower_low")
	if len(lowerLows) != 2 || math.Abs(lowerLows[1].Price-664.32) > 0.001 {
		t.Fatalf("expected recent VOO lower low ending at 664.32, got %+v", lowerLows)
	}

	lowerHighs := calcChartPivotStructures(candles, pivots, "lower_high")
	if len(lowerHighs) != 2 || math.Abs(lowerHighs[1].Price-695.75) > 0.001 {
		t.Fatalf("expected recent VOO lower high ending at 695.75, got %+v", lowerHighs)
	}
}

func TestCalcChartPivotStructuresDetectsLatestEdgeLowerLow(t *testing.T) {
	candles := make([]ChartCandle, 0, 64)
	start := time.Date(2026, time.April, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 64; i++ {
		close := 20.0
		high := close + 0.4
		low := close - 0.4
		switch i {
		case 44:
			close, high, low = 25.25, 25.95, 24.38
		case 53:
			close, high, low = 20.06, 20.08, 19.14
		case 57:
			close, high, low = 20.21, 20.30, 19.37
		case 60:
			close, high, low = 19.77, 20.71, 19.68
		case 61:
			close, high, low = 19.73, 20.19, 19.25
		case 62:
			close, high, low = 18.50, 19.64, 18.50
		case 63:
			close, high, low = 18.90, 18.96, 17.82
		}
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 0.1,
			High:   high,
			Low:    low,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	pivots := []ChartPivot{
		{Time: candles[44].Time, Kind: "high", Price: 25.95},
		{Time: candles[53].Time, Kind: "low", Price: 19.14},
	}

	lowerLows := calcChartPivotStructures(candles, pivots, "lower_low")
	if len(lowerLows) != 2 || math.Abs(lowerLows[1].Price-17.82) > 0.001 {
		t.Fatalf("expected latest edge lower low ending at 17.82, got %+v", lowerLows)
	}

	lowerHighs := calcChartPivotStructures(candles, pivots, "lower_high")
	if len(lowerHighs) != 2 || math.Abs(lowerHighs[1].Price-20.71) > 0.001 {
		t.Fatalf("expected recent lower high ending at 20.71, got %+v", lowerHighs)
	}
}

func TestChartExtendedMarketFromMeta(t *testing.T) {
	pre := chartExtendedMarketFromMeta(yahooChartMeta{
		MarketState:            "PRE",
		PreMarketPrice:         101.5,
		PreMarketChangePercent: 1.5,
		PostMarketPrice:        99.5,
	})
	if pre == nil || pre.Session != "pre" || pre.Price != 101.5 || pre.ChangePercent != 1.5 {
		t.Fatalf("expected pre-market quote, got %+v", pre)
	}

	post := chartExtendedMarketFromMeta(yahooChartMeta{
		MarketState:             "CLOSED",
		PostMarketPrice:         98.25,
		PostMarketChangePercent: -1.75,
	})
	if post == nil || post.Session != "post" || post.Price != 98.25 || post.ChangePercent != -1.75 {
		t.Fatalf("expected post-market quote, got %+v", post)
	}

	regular := chartExtendedMarketFromMeta(yahooChartMeta{
		MarketState:     "REGULAR",
		PostMarketPrice: 98.25,
	})
	if regular != nil {
		t.Fatalf("expected no extended quote during regular market, got %+v", regular)
	}
}

func TestChartExtendedMarketFromIntradayPostSession(t *testing.T) {
	priceA := 1058.0
	priceB := 1064.9987
	meta := yahooChartMeta{RegularMarketPrice: 1051.77}
	meta.TradingPeriods.Post = [][]yahooTradingPeriod{{
		{Start: 1782244800, End: 1782259200},
	}}

	extended := chartExtendedMarketFromIntraday(
		meta,
		[]int64{1782244500, 1782245100, 1782259199},
		[]*float64{ptrFloat64(1051.77), &priceA, &priceB},
	)
	if extended == nil {
		t.Fatal("expected derived post-market quote")
	}
	if extended.Session != "post" || extended.Price != priceB || extended.Time != 1782259199 {
		t.Fatalf("unexpected post-market quote: %+v", extended)
	}
	if math.Abs(extended.ChangePercent-percentChange(priceB, meta.RegularMarketPrice)) > 0.0001 {
		t.Fatalf("unexpected post-market change percent: %+v", extended)
	}
}

func TestChartExtendedMarketFromIntradayPrefersLatestPreSession(t *testing.T) {
	postPrice := 1064.9987
	prePrice := 1087.31
	meta := yahooChartMeta{RegularMarketPrice: 1051.77}
	meta.TradingPeriods.Post = [][]yahooTradingPeriod{{
		{Start: 1782244800, End: 1782259200},
	}}
	meta.TradingPeriods.Pre = [][]yahooTradingPeriod{{
		{Start: 1782288000, End: 1782307800},
	}}

	extended := chartExtendedMarketFromIntraday(
		meta,
		[]int64{1782259199, 1782291600},
		[]*float64{&postPrice, &prePrice},
	)
	if extended == nil {
		t.Fatal("expected derived pre-market quote")
	}
	if extended.Session != "pre" || extended.Price != prePrice || extended.Time != 1782291600 {
		t.Fatalf("expected latest pre-market quote, got %+v", extended)
	}
}

func TestChartExtendedMarketFromIntradaySuppressesPreDuringRegularSession(t *testing.T) {
	prePrice := 8.6
	meta := yahooChartMeta{RegularMarketPrice: 8.28}
	meta.CurrentTradingPeriod.Pre = yahooTradingPeriod{Start: 1000, End: 2000}
	meta.CurrentTradingPeriod.Regular = yahooTradingPeriod{Start: 2000, End: 6000}
	meta.TradingPeriods.Pre = [][]yahooTradingPeriod{{meta.CurrentTradingPeriod.Pre}}

	extended := chartExtendedMarketFromIntradayAt(
		meta,
		[]int64{1500},
		[]*float64{&prePrice},
		3000,
	)
	if extended != nil {
		t.Fatalf("expected no pre-market badge during regular session, got %+v", extended)
	}
}

func TestChartExtendedMarketFromIntradaySuppressesStalePostBeforePremarket(t *testing.T) {
	postPrice := 1064.99
	meta := yahooChartMeta{
		MarketState:        "CLOSED",
		RegularMarketPrice: 1051.77,
		PostMarketPrice:    postPrice,
	}
	meta.CurrentTradingPeriod.Pre = yahooTradingPeriod{Start: 10_000, End: 20_000}
	meta.CurrentTradingPeriod.Regular = yahooTradingPeriod{Start: 20_000, End: 30_000}
	meta.CurrentTradingPeriod.Post = yahooTradingPeriod{Start: 30_000, End: 40_000}
	meta.TradingPeriods.Post = [][]yahooTradingPeriod{{{Start: 1_000, End: 2_000}}}

	extended := chartExtendedMarketFromIntradayAt(
		meta,
		[]int64{1_500},
		[]*float64{&postPrice},
		9_000,
	)
	if extended != nil {
		t.Fatalf("expected stale post-market badge to be hidden before pre-market, got %+v", extended)
	}
}

func TestChartExtendedMarketFromIntradayTrustsExplicitPremarketState(t *testing.T) {
	prePrice := 1087.31
	meta := yahooChartMeta{
		MarketState:            "PREPRE",
		RegularMarketPrice:     1051.77,
		PreMarketPrice:         prePrice,
		PreMarketChangePercent: 3.38,
	}
	meta.CurrentTradingPeriod.Pre = yahooTradingPeriod{Start: 10_000, End: 20_000}
	meta.CurrentTradingPeriod.Regular = yahooTradingPeriod{Start: 20_000, End: 30_000}
	meta.CurrentTradingPeriod.Post = yahooTradingPeriod{Start: 30_000, End: 40_000}

	extended := chartExtendedMarketFromIntradayAt(
		meta,
		[]int64{1_500},
		[]*float64{ptrFloat64(1064.99)},
		9_000,
	)
	if extended == nil || extended.Session != "pre" || extended.Price != prePrice {
		t.Fatalf("expected explicit pre-market meta quote, got %+v", extended)
	}
}

func TestDecodeYahooStreamOvernightQuote(t *testing.T) {
	var message []byte
	message = appendProtoString(message, 1, "MU")
	message = appendProtoFloat(message, 2, 1149.50)
	message = appendProtoSint(message, 3, 1782816816000)
	message = appendProtoVarint(message, 7<<3, 4)
	message = appendProtoFloat(message, 8, 0.36846)
	message = appendProtoFloat(message, 12, 4.22)

	quote, ok := decodeYahooStreamQuote([]byte(base64.StdEncoding.EncodeToString(message)))
	if !ok {
		t.Fatal("expected stream quote to decode")
	}
	if quote.Symbol != "MU" || math.Abs(quote.Price-1149.50) > 0.01 || quote.MarketHours != 4 {
		t.Fatalf("unexpected stream quote: %+v", quote)
	}
	extended := chartExtendedMarketFromStream(quote)
	if extended == nil || extended.Session != "overnight" || extended.MarketState != "OVERNIGHT" {
		t.Fatalf("expected overnight extended quote, got %+v", extended)
	}
	if extended.Time != 1782816816 {
		t.Fatalf("expected milliseconds converted to seconds, got %d", extended.Time)
	}

	envelope := []byte(`{"message":"` + base64.StdEncoding.EncodeToString(message) + `"}`)
	wrappedQuote, ok := decodeYahooStreamQuote(envelope)
	if !ok || wrappedQuote.Symbol != "MU" || wrappedQuote.MarketHours != 4 {
		t.Fatalf("expected version 2 envelope to decode, got %+v", wrappedQuote)
	}
}

func TestFetchYahooStreamQuoteLive(t *testing.T) {
	if os.Getenv("LIVE_YAHOO_TEST") == "" {
		t.Skip("set LIVE_YAHOO_TEST=1 to query Yahoo's live streamer")
	}
	symbol := os.Getenv("LIVE_YAHOO_SYMBOL")
	if symbol == "" {
		symbol = "MU"
	}
	quotes := fetchYahooStreamQuotes(t.Context(), []string{symbol}, 18*time.Second)
	quote, ok := quotes[symbol]
	if !ok {
		t.Fatalf("expected a live %s stream quote", symbol)
	}
	if quote.Price <= 0 {
		t.Fatalf("expected a positive live price, got %+v", quote)
	}
	t.Logf("live Yahoo quote: %+v", quote)
}

func TestQuotesYahooLiveIncludesExtendedMarket(t *testing.T) {
	if os.Getenv("LIVE_YAHOO_TEST") == "" {
		t.Skip("set LIVE_YAHOO_TEST=1 to query Yahoo's live APIs")
	}
	request := httptest.NewRequest("GET", "/api/quotes?tickers=MU", nil)
	response := httptest.NewRecorder()
	(&Server{}).quotesYahoo(response, request)
	if response.Code != 200 {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	t.Logf("live quotes response: %s", response.Body.String())
	if !strings.Contains(response.Body.String(), `"extendedMarket"`) {
		t.Fatalf("expected extended market quote: %s", response.Body.String())
	}
}

func appendProtoString(dst []byte, field int, value string) []byte {
	dst = appendProtoVarint(dst, field<<3|2)
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendProtoFloat(dst []byte, field int, value float32) []byte {
	dst = appendProtoVarint(dst, field<<3|5)
	return binary.LittleEndian.AppendUint32(dst, math.Float32bits(value))
}

func appendProtoSint(dst []byte, field int, value int64) []byte {
	zigzag := uint64(value<<1) ^ uint64(value>>63)
	return appendProtoVarint(dst, field<<3, int(zigzag))
}

func appendProtoVarint(dst []byte, values ...int) []byte {
	for _, value := range values {
		dst = binary.AppendUvarint(dst, uint64(value))
	}
	return dst
}

func TestYahooChartReferenceClosePrefersPreviousClose(t *testing.T) {
	ref := yahooChartReferenceClose(yahooChartMeta{
		PreviousClose:      1211.38,
		ChartPreviousClose: 1133.99,
	})
	if ref != 1211.38 {
		t.Fatalf("expected previousClose to win over chartPreviousClose, got %f", ref)
	}
}

func TestBearishDivergenceStillActionableWithMASupportBounce(t *testing.T) {
	div := ChartDivergence{Kind: "bearish", ToPrice: 1089.29}
	setups := []ChartSetup{{Kind: "ma_support_bounce"}}
	if !isDivergenceStillActionable(div, setups, 1051.77) {
		t.Fatal("expected bearish divergence warning to remain visible with MA support bounce")
	}
}

func TestCalcRSIDivergencesUsesRecentEdgeHigh(t *testing.T) {
	start := time.Date(2026, time.June, 1, 13, 30, 0, 0, time.UTC)
	candles := make([]ChartCandle, 0, 18)
	rsi := make([]ChartLinePoint, 0, 18)
	for i := 0; i < 18; i++ {
		high := 900.0 + float64(i)
		low := high - 60
		close := high - 20
		rsiValue := 60.0
		switch i {
		case 3:
			high, low, close, rsiValue = 1089.29, 1010, 1065, 82.36
		case 4:
			high, low, close, rsiValue = 960, 890, 910, 55
		case 5:
			high, low, close, rsiValue = 940, 870, 900, 54
		case 6:
			high, low, close, rsiValue = 930, 860, 895, 53
		case 13:
			high, low, close, rsiValue = 1149.43, 1090, 1133, 66.39
		case 14:
			high, low, close, rsiValue = 1213.56, 1168, 1211, 69.76
		case 15:
			high, low, close, rsiValue = 1211.38, 1038, 1051, 57.04
		}
		ts := start.AddDate(0, 0, i).Unix()
		candles = append(candles, ChartCandle{Time: ts, Open: close - 5, High: high, Low: low, Close: close, Volume: 1_000_000})
		rsi = append(rsi, ChartLinePoint{Time: ts, Value: rsiValue})
	}

	pivots := calcChartPivots(candles, rsi)
	divs := calcRSIDivergences(candles, pivots)
	if len(divs) == 0 {
		t.Fatalf("expected bearish divergence from confirmed high to recent edge high, pivots=%+v", pivots)
	}
	last := divs[len(divs)-1]
	if last.Kind != "bearish" || math.Abs(last.ToPrice-1213.56) > 0.001 || math.Abs(last.ToRSI-69.76) > 0.001 {
		t.Fatalf("unexpected latest divergence: %+v", last)
	}
}

func ptrFloat64(v float64) *float64 {
	return &v
}
