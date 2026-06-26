package chartcalc

import (
	"fmt"
	"math"
	"sort"
	"time"
)

type Candle struct {
	Time   int64   `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
}

type LinePoint struct {
	Time  int64   `json:"time"`
	Value float64 `json:"value"`
}

type Pivot struct {
	Time  int64   `json:"time"`
	Kind  string  `json:"kind"`
	Price float64 `json:"price"`
	RSI   float64 `json:"rsi,omitempty"`
}

type Level struct {
	Kind     string  `json:"kind"`
	Price    float64 `json:"price"`
	Strength int     `json:"strength"`
	Touches  []int64 `json:"touches"`
}

type Divergence struct {
	Kind      string  `json:"kind"`
	FromTime  int64   `json:"fromTime"`
	ToTime    int64   `json:"toTime"`
	FromPrice float64 `json:"fromPrice"`
	ToPrice   float64 `json:"toPrice"`
	FromRSI   float64 `json:"fromRsi"`
	ToRSI     float64 `json:"toRsi"`
}

func CalcRSI14(candles []Candle) []LinePoint {
	const period = 14
	if len(candles) <= period {
		return nil
	}
	gains := make([]float64, len(candles))
	losses := make([]float64, len(candles))
	for i := 1; i < len(candles); i++ {
		diff := candles[i].Close - candles[i-1].Close
		if diff >= 0 {
			gains[i] = diff
		} else {
			losses[i] = -diff
		}
	}

	var avgGain, avgLoss float64
	for i := 1; i <= period; i++ {
		avgGain += gains[i]
		avgLoss += losses[i]
	}
	avgGain /= period
	avgLoss /= period

	points := make([]LinePoint, 0, len(candles)-period)
	points = append(points, LinePoint{Time: candles[period].Time, Value: rsiValue(avgGain, avgLoss)})
	for i := period + 1; i < len(candles); i++ {
		avgGain = (avgGain*float64(period-1) + gains[i]) / period
		avgLoss = (avgLoss*float64(period-1) + losses[i]) / period
		points = append(points, LinePoint{Time: candles[i].Time, Value: rsiValue(avgGain, avgLoss)})
	}
	return points
}

func CalcPivots(candles []Candle, rsi []LinePoint) []Pivot {
	const left = 3
	const right = 3
	if len(candles) < left+right+1 {
		return nil
	}
	rsiByTime := make(map[int64]float64, len(rsi))
	for _, p := range rsi {
		rsiByTime[p.Time] = p.Value
	}
	var pivots []Pivot
	for i := left; i < len(candles)-right; i++ {
		hi := candles[i].High
		lo := candles[i].Low
		isHigh := true
		isLow := true
		for j := i - left; j <= i+right; j++ {
			if j == i {
				continue
			}
			if candles[j].High >= hi {
				isHigh = false
			}
			if candles[j].Low <= lo {
				isLow = false
			}
		}
		if isHigh {
			pivots = append(pivots, Pivot{Time: candles[i].Time, Kind: "high", Price: hi, RSI: rsiByTime[candles[i].Time]})
		}
		if isLow {
			pivots = append(pivots, Pivot{Time: candles[i].Time, Kind: "low", Price: lo, RSI: rsiByTime[candles[i].Time]})
		}
	}
	pivots = AppendRecentEdgeRSIPivots(pivots, candles, "high", rsiByTime)
	pivots = AppendRecentEdgeRSIPivots(pivots, candles, "low", rsiByTime)
	sort.SliceStable(pivots, func(i, j int) bool { return pivots[i].Time < pivots[j].Time })
	return pivots
}

func AppendRecentEdgeRSIPivots(pivots []Pivot, candles []Candle, pivotKind string, rsiByTime map[int64]float64) []Pivot {
	edge := AppendRecentEdgePivots(nil, candles, pivotKind, 0)
	if len(edge) == 0 {
		return pivots
	}
	existing := make(map[string]bool, len(pivots))
	for _, p := range pivots {
		existing[fmt.Sprintf("%s:%d", p.Kind, p.Time)] = true
	}
	for _, p := range edge {
		key := fmt.Sprintf("%s:%d", p.Kind, p.Time)
		if existing[key] {
			continue
		}
		p.RSI = rsiByTime[p.Time]
		pivots = append(pivots, p)
	}
	return pivots
}

func AppendRecentEdgePivots(candidates []Pivot, candles []Candle, pivotKind string, firstTime int64) []Pivot {
	if len(candles) < 8 {
		return candidates
	}
	existing := make(map[int64]bool, len(candidates))
	for _, p := range candidates {
		existing[p.Time] = true
	}
	start := maxInt(4, len(candles)-24)
	for i := start; i < len(candles); i++ {
		c := candles[i]
		if c.Time < firstTime || existing[c.Time] {
			continue
		}
		left := 4
		right := minInt(2, len(candles)-1-i)
		if right < 1 && i != len(candles)-1 {
			continue
		}
		isPivot := true
		for j := i - left; j <= i+right; j++ {
			if j == i {
				continue
			}
			if pivotKind == "high" && candles[j].High >= c.High {
				isPivot = false
				break
			}
			if pivotKind == "low" && candles[j].Low <= c.Low {
				isPivot = false
				break
			}
		}
		if !isPivot {
			continue
		}
		price := c.High
		if pivotKind == "low" {
			price = c.Low
		}
		candidates = append(candidates, Pivot{
			Time:  c.Time,
			Kind:  pivotKind,
			Price: price,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Time < candidates[j].Time })
	return candidates
}

func CalcRSIDivergences(candles []Candle, pivots []Pivot) []Divergence {
	var divs []Divergence
	lastLow := Pivot{}
	lastHigh := Pivot{}
	for _, p := range pivots {
		if p.RSI == 0 {
			continue
		}
		switch p.Kind {
		case "low":
			if lastLow.Time != 0 && p.Price < lastLow.Price && p.RSI > lastLow.RSI+2 && divergenceSameSwing(candles, lastLow, p, "bullish") {
				divs = append(divs, Divergence{
					Kind: "bullish", FromTime: lastLow.Time, ToTime: p.Time,
					FromPrice: lastLow.Price, ToPrice: p.Price, FromRSI: lastLow.RSI, ToRSI: p.RSI,
				})
			}
			lastLow = p
		case "high":
			if lastHigh.Time != 0 && p.Price > lastHigh.Price && p.RSI < lastHigh.RSI-2 && divergenceSameSwing(candles, lastHigh, p, "bearish") {
				divs = append(divs, Divergence{
					Kind: "bearish", FromTime: lastHigh.Time, ToTime: p.Time,
					FromPrice: lastHigh.Price, ToPrice: p.Price, FromRSI: lastHigh.RSI, ToRSI: p.RSI,
				})
			}
			lastHigh = p
		}
	}
	return divs
}

func FilterDivergencesByContext(divs []Divergence, candles []Candle, ma50, ma100, ma200 []LinePoint, levels []Level) []Divergence {
	if len(divs) == 0 {
		return nil
	}
	out := make([]Divergence, 0, len(divs))
	for _, div := range divs {
		if div.Kind == "bullish" && divergenceOverlapsMASupportBounce(div, candles, ma50, ma100, ma200, levels) {
			continue
		}
		out = append(out, div)
	}
	return out
}

func divergenceSameSwing(candles []Candle, from, to Pivot, kind string) bool {
	if len(candles) == 0 || from.Time == 0 || to.Time == 0 || to.Time <= from.Time {
		return true
	}
	const maxSwingCandles = 70
	count := 0
	maxHigh := math.Max(from.Price, to.Price)
	minLow := math.Min(from.Price, to.Price)
	for _, c := range candles {
		if c.Time < from.Time || c.Time > to.Time {
			continue
		}
		count++
		maxHigh = math.Max(maxHigh, c.High)
		minLow = math.Min(minLow, c.Low)
	}
	if count == 0 {
		return true
	}
	if count > maxSwingCandles {
		return false
	}
	switch kind {
	case "bullish":
		base := math.Max(from.Price, to.Price)
		return base > 0 && maxHigh <= base*1.35
	case "bearish":
		base := math.Min(from.Price, to.Price)
		return base > 0 && minLow >= base*0.65
	default:
		return true
	}
}

func divergenceOverlapsMASupportBounce(div Divergence, candles []Candle, ma50, ma100, ma200 []LinePoint, levels []Level) bool {
	pivotCandle, ok := candleAtOrAfter(candles, div.ToTime)
	if !ok || div.ToPrice <= 0 {
		return false
	}
	nearMA := priceNearLineAt(ma50, div.ToTime, div.ToPrice, 0.08) ||
		priceNearLineAt(ma100, div.ToTime, div.ToPrice, 0.08) ||
		priceNearLineAt(ma200, div.ToTime, div.ToPrice, 0.08)
	nearSupport := priceNearSupport(levels, div.ToPrice, 0.06)
	if !nearMA && !nearSupport {
		return false
	}
	recovered := pivotCandle.Close > pivotCandle.Open
	for _, c := range candles {
		if c.Time <= div.ToTime {
			continue
		}
		if c.Time > div.ToTime+int64(14*24*time.Hour/time.Second) {
			break
		}
		if c.Close >= div.ToPrice*1.08 || priceAboveLineAt(ma50, c.Time, c.Close, 0.01) {
			recovered = true
			break
		}
	}
	return recovered
}

func candleAtOrAfter(candles []Candle, ts int64) (Candle, bool) {
	for _, c := range candles {
		if c.Time >= ts {
			return c, true
		}
	}
	return Candle{}, false
}

func priceNearLineAt(points []LinePoint, ts int64, price, tolerance float64) bool {
	value, ok := lineValueAtOrBefore(points, ts)
	return ok && value > 0 && math.Abs(price-value)/value <= tolerance
}

func priceAboveLineAt(points []LinePoint, ts int64, price, tolerance float64) bool {
	value, ok := lineValueAtOrBefore(points, ts)
	return ok && value > 0 && price >= value*(1-tolerance)
}

func lineValueAtOrBefore(points []LinePoint, ts int64) (float64, bool) {
	idx := sort.Search(len(points), func(i int) bool { return points[i].Time > ts }) - 1
	if idx < 0 {
		return 0, false
	}
	return points[idx].Value, true
}

func priceNearSupport(levels []Level, price, tolerance float64) bool {
	if price <= 0 {
		return false
	}
	for _, lvl := range levels {
		if lvl.Kind == "support" && lvl.Price > 0 && math.Abs(price-lvl.Price)/price <= tolerance {
			return true
		}
	}
	return false
}

func rsiValue(avgGain, avgLoss float64) float64 {
	if avgLoss == 0 {
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
