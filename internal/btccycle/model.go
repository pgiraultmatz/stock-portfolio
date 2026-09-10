// Package btccycle fits a conditional historical analogy, not a calibrated forecast.
package btccycle

import (
	"fmt"
	"math"
	"sort"
	"time"
)

const day = int64(86400)

type Point struct {
	Time  int64   `json:"time"`
	Value float64 `json:"value"`
}

type Options struct {
	ReferenceStart int64
	TargetStart    int64
	AsOf           int64
	Horizon        int
}

type Fit struct {
	Speed      float64 `json:"speed"`
	Amplitude  float64 `json:"amplitude"`
	LogRMSE    float64 `json:"logRMSE"`
	Samples    int     `json:"samples"`
	AtBoundary bool    `json:"atBoundary"`
}

type Scenario struct {
	Name      string  `json:"name"`
	Speed     float64 `json:"speed"`
	Points    []Point `json:"points"`
	Truncated bool    `json:"truncated"`
}

type Validation struct {
	Horizon     int     `json:"horizon"`
	Samples     int     `json:"samples"`
	ModelLogMAE float64 `json:"modelLogMAE"`
	FlatLogMAE  float64 `json:"flatLogMAE"`
}

type TopForecast struct {
	PreviousATH       Point   `json:"previousATH"`
	LatestATH         Point   `json:"latestATH"`
	PreviousCycleGain float64 `json:"previousCycleGain"`
	DiminishingFactor float64 `json:"diminishingFactor"`
	PriceCentral      float64 `json:"priceCentral"`
	PriceLow          float64 `json:"priceLow"`
	PriceHigh         float64 `json:"priceHigh"`
	DateLow           int64   `json:"dateLow"`
	DateCentral       int64   `json:"dateCentral"`
	DateHigh          int64   `json:"dateHigh"`
	BreakoutDate      int64   `json:"breakoutDate"`
	Method            string  `json:"method"`
}

type BottomSignal struct {
	TwoRedSixMonthCandles bool            `json:"twoRedSixMonthCandles"`
	SecondRedClose        int64           `json:"secondRedClose"`
	RedSixMonthStart      int64           `json:"redSixMonthStart,omitempty"`
	RedSixMonthEnd        int64           `json:"redSixMonthEnd,omitempty"`
	RedSixMonthRanges     []SixMonthRange `json:"redSixMonthRanges,omitempty"`
	CandidateBottom       *Point          `json:"candidateBottom,omitempty"`
	Drawdown              float64         `json:"drawdown"`
	Status                string          `json:"status"`
}

type SixMonthRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type CycleEvent struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Point Point  `json:"point"`
	Cycle string `json:"cycle"`
}

type Result struct {
	ReferenceStart  Point        `json:"referenceStart"`
	TargetStart     Point        `json:"targetStart"`
	ReferenceATH    Point        `json:"referenceATH"`
	ObservedHigh    Point        `json:"observedHigh"`
	ReclaimedATH    *Point       `json:"reclaimedATH,omitempty"`
	AsOf            int64        `json:"asOf"`
	Fit             Fit          `json:"fit"`
	Previous        []Point      `json:"previous"`
	Actual          []Point      `json:"actual"`
	Aligned         []Point      `json:"aligned"`
	Scenarios       []Scenario   `json:"scenarios"`
	Validation      []Validation `json:"validation"`
	TopForecast     *TopForecast `json:"topForecast,omitempty"`
	Bottom          BottomSignal `json:"bottom"`
	Events          []CycleEvent `json:"events"`
	NextCycle       []Point      `json:"nextCycle"`
	NextCycleRecent []Point      `json:"nextCycleRecent"`
	Warnings        []string     `json:"warnings"`
}

// Analyze accepts completed daily closes only. Anchors are user-selected dates;
// historical validation is conditional on those retrospective anchor choices.
func Analyze(history []Point, o Options) (*Result, error) {
	if o.ReferenceStart >= o.TargetStart || o.TargetStart >= o.AsOf || o.Horizon < 30 || o.Horizon > 730 {
		return nil, fmt.Errorf("ancrages, date d'observation ou horizon invalides")
	}
	points := make([]Point, 0, len(history))
	seen := make(map[int64]bool)
	for _, p := range history {
		if p.Time > o.AsOf {
			continue
		}
		p.Time = p.Time / day * day
		if p.Value <= 0 || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) || seen[p.Time] {
			return nil, fmt.Errorf("historique invalide ou dates dupliquées")
		}
		seen[p.Time] = true
		points = append(points, p)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Time < points[j].Time })
	var ref, target []Point
	for _, p := range points {
		if p.Time >= o.ReferenceStart && p.Time < o.TargetStart {
			ref = append(ref, p)
		}
		if p.Time >= o.TargetStart {
			target = append(target, p)
		}
	}
	if len(ref) < 365 || len(target) < 181 || ref[0].Time != o.ReferenceStart || target[0].Time != o.TargetStart {
		return nil, fmt.Errorf("historique insuffisant : ancrages exacts, un an de référence et 180 jours observés requis")
	}
	for _, series := range [][]Point{ref, target} {
		for i := 1; i < len(series); i++ {
			if series[i].Time-series[i-1].Time > 3*day {
				return nil, fmt.Errorf("historique incomplet : interruption supérieure à trois jours")
			}
		}
	}
	fit, ok := fitCycle(ref, target)
	if !ok {
		return nil, fmt.Errorf("aucun ajustement possible dans les limites du cycle de référence")
	}
	r := &Result{ReferenceStart: ref[0], TargetStart: target[0], AsOf: target[len(target)-1].Time, Fit: fit, Previous: ref, Actual: target,
		Warnings: []string{"Analogie expérimentale : scénarios conditionnels, pas des probabilités ni une date certaine de sommet.", "Ancrages de creux choisis rétrospectivement ; les tests ne valident pas leur détection en temps réel."}}
	r.ReferenceATH = highest(ref)
	r.ObservedHigh = highest(target)
	r.Bottom = analyzeBottom(points, target)
	currentBottom := currentBottomCandidate(target, r.ObservedHigh)
	if currentBottom != nil {
		r.Bottom.CandidateBottom = currentBottom
	}
	r.Events = []CycleEvent{
		{Kind: "bottom", Label: "Bottom 2018", Point: ref[0], Cycle: "previous"},
		{Kind: "ath", Label: "ATH 2021", Point: r.ReferenceATH, Cycle: "previous"},
		{Kind: "bottom", Label: "Bottom 2022", Point: target[0], Cycle: "current"},
		{Kind: "ath", Label: "ATH 2025", Point: r.ObservedHigh, Cycle: "current"},
	}
	if currentBottom != nil {
		r.Events = append(r.Events, CycleEvent{Kind: "bottom", Label: "Bottom candidat 2025/26", Point: *currentBottom, Cycle: "current"})
	}
	if previous, ok := highestBefore(points, ref[0].Time); ok && r.ReferenceATH.Value > previous.Value {
		// Killa-style diminishing returns. Keep the factor explicit and visible;
		// it is a hypothesis to backtest, not a fitted certainty.
		gain := r.ReferenceATH.Value/previous.Value - 1
		factor := 0.2935
		central := r.ObservedHigh.Value * (1 + gain*factor)
		cycleGap := r.ObservedHigh.Time - r.ReferenceATH.Time
		if cycleGap <= 0 {
			cycleGap = 1460 * day
		}
		r.TopForecast = &TopForecast{
			PreviousATH: previous, LatestATH: r.ObservedHigh, PreviousCycleGain: gain, DiminishingFactor: factor,
			PriceCentral: central, PriceLow: r.ObservedHigh.Value * (1 + gain*.20), PriceHigh: r.ObservedHigh.Value * (1 + gain*.40),
			DateLow: r.ObservedHigh.Time + int64(float64(cycleGap)*.50), DateCentral: r.ObservedHigh.Time + int64(float64(cycleGap)*.60), DateHigh: r.ObservedHigh.Time + int64(float64(cycleGap)*.80),
			Method: "Prix : sommet de cycle = ATH récent × (1 + gain du cycle précédent × 0,2935). Le franchissement du précédent ATH est ciblé autour de l'automne 2027 ; le sommet absolu du cycle intervient ensuite.",
		}
		if currentBottom != nil {
			// Moderated accelerated-cycle hypothesis: no branch targets a new
			// ATH before mid-September 2027. The central path is late October,
			// with a wider mid-September to mid-December timing window.
			r.TopForecast.BreakoutDate = time.Date(2027, time.October, 31, 0, 0, 0, 0, time.UTC).Unix()
			r.TopForecast.DateLow = r.TopForecast.BreakoutDate + 180*day
			r.TopForecast.DateCentral = r.TopForecast.BreakoutDate + 240*day
			r.TopForecast.DateHigh = r.TopForecast.BreakoutDate + 300*day
			// Start the hypothetical cycle at the candidate bottom itself. The
			// historical bottom is used only as the shape template, never as the
			// projected anchor.
			r.NextCycle = projectFullNextCycle(ref, target[0], *currentBottom, r.ObservedHigh.Value, r.TopForecast.BreakoutDate, r.TopForecast.PriceCentral, r.TopForecast.DateCentral)
			// A second, independent analogy uses the current 2022-2026 cycle
			// itself as the shape template, conditional on this bottom being real.
			candidateIndex := 0
			for i := range target {
				if target[i].Time == currentBottom.Time {
					candidateIndex = i
					break
				}
			}
			recentCycle := append([]Point(nil), target[:candidateIndex+1]...)
			if len(recentCycle) >= 180 {
				r.NextCycleRecent = projectFullNextCycle(recentCycle, *currentBottom, *currentBottom, r.ObservedHigh.Value, r.TopForecast.BreakoutDate, r.TopForecast.PriceCentral, r.TopForecast.DateCentral)
			}
			r.Events = append(r.Events, CycleEvent{Kind: "ath", Label: "Sommet de cycle", Point: Point{Time: r.TopForecast.DateCentral, Value: r.TopForecast.PriceCentral}, Cycle: "next"})
			r.Events = append(r.Events, CycleEvent{Kind: "ath", Label: "Nouvel ATH franchi", Point: Point{Time: r.TopForecast.BreakoutDate, Value: r.ObservedHigh.Value}, Cycle: "next"})
		}
	}
	for _, p := range target {
		if p.Value >= r.ReferenceATH.Value {
			v := p
			r.ReclaimedATH = &v
			break
		}
	}
	for _, p := range target {
		v, _ := referenceLog(ref, float64(p.Time-target[0].Time)/float64(day)*fit.Speed)
		r.Aligned = append(r.Aligned, Point{Time: p.Time, Value: target[0].Value * math.Exp(fit.Amplitude*v)})
	}
	for _, item := range []struct {
		name  string
		speed float64
	}{{"Ralenti", fit.Speed * .85}, {"Ajusté", fit.Speed}, {"Accéléré", fit.Speed * 1.15}} {
		r.Scenarios = append(r.Scenarios, project(ref, target, fit, item.name, item.speed, o.Horizon))
	}
	if r.TopForecast != nil {
		for i := range r.Scenarios {
			extendToForecast(&r.Scenarios[i], target[len(target)-1], *r.TopForecast, o.Horizon)
		}
	}
	// The full-cycle path supersedes the legacy speed branches. Do not expose
	// those branches when they can create an earlier apparent ATH in clients.
	if len(r.NextCycle) > 0 {
		r.Scenarios = nil
	}
	r.Validation = validate(ref, target)
	if fit.AtBoundary {
		r.Warnings = append(r.Warnings, "Ajustement en limite de paramètres : analogie fragile.")
	}
	if fit.LogRMSE > .25 {
		r.Warnings = append(r.Warnings, "Écart important entre le cycle recalé et le prix observé.")
	}
	return r, nil
}

func highest(points []Point) Point {
	p := points[0]
	for _, v := range points[1:] {
		if v.Value > p.Value {
			p = v
		}
	}
	return p
}

func highestBefore(points []Point, before int64) (Point, bool) {
	var result Point
	found := false
	for _, p := range points {
		if p.Time < before && (!found || p.Value > result.Value) {
			result, found = p, true
		}
	}
	return result, found
}

func analyzeBottom(points, target []Point) BottomSignal {
	var signal BottomSignal
	if len(target) == 0 {
		signal.Status = "Indisponible"
		return signal
	}
	// Calendar half-years: the rule is evaluated only after the second candle closes.
	type candle struct {
		start, end  int64
		open, close float64
	}
	var six []candle
	for _, p := range points {
		t := time.Unix(p.Time, 0).UTC()
		half := 0
		if t.Month() >= 7 {
			half = 1
		}
		start := time.Date(t.Year(), time.Month(half*6+1), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 6, 0).Unix() - day
		if len(six) == 0 || six[len(six)-1].start != start.Unix() {
			six = append(six, candle{start: start.Unix(), end: end, open: p.Value, close: p.Value})
		} else {
			six[len(six)-1].close = p.Value
		}
	}
	completed := six[:0]
	latestTime := points[len(points)-1].Time
	for _, c := range six {
		if c.end <= latestTime {
			completed = append(completed, c)
		}
	}
	if len(completed) < 2 {
		signal.Status = "Indisponible"
		return signal
	}
	var previous, last candle
	foundPair := false
	for i := 1; i < len(completed); i++ {
		if completed[i-1].close < completed[i-1].open && completed[i].close < completed[i].open {
			signal.RedSixMonthRanges = append(signal.RedSixMonthRanges, SixMonthRange{Start: completed[i-1].start, End: completed[i].end})
			previous, last, foundPair = completed[i-1], completed[i], true
		}
	}
	if !foundPair {
		signal.Status = "Non confirmé"
		return signal
	}
	if previous.close >= previous.open || last.close >= last.open {
		signal.Status = "Non confirmé"
		return signal
	}
	signal.TwoRedSixMonthCandles, signal.SecondRedClose = true, last.end
	signal.RedSixMonthStart, signal.RedSixMonthEnd = previous.start, last.end
	searchEnd := last.end + 365*day
	var candidate Point
	found := false
	var priorHigh float64
	for _, p := range points {
		if p.Time >= previous.start && p.Time <= last.end && p.Value > priorHigh {
			priorHigh = p.Value
		}
	}
	for _, p := range points {
		if p.Time > last.end && p.Time <= searchEnd && (!found || p.Value < candidate.Value) {
			candidate, found = p, true
		}
	}
	if !found {
		signal.Status = "En attente"
		return signal
	}
	signal.CandidateBottom = &candidate
	if priorHigh > 0 {
		signal.Drawdown = candidate.Value/priorHigh - 1
	}
	if signal.Drawdown <= -.30 && candidate.Time < last.end+365*day {
		signal.Status = "Bottom candidat"
	} else {
		signal.Status = "Signal faible"
	}
	return signal
}

func currentBottomCandidate(target []Point, ath Point) *Point {
	var candidate Point
	found := false
	for _, p := range target {
		if p.Time > ath.Time && (!found || p.Value < candidate.Value) {
			candidate, found = p, true
		}
	}
	if !found || candidate.Value > ath.Value*.80 {
		return nil
	}
	return &candidate
}

func projectFullNextCycle(reference []Point, historicalBottom Point, projectedBottom Point, breakoutPrice float64, breakoutTime int64, targetTop float64, targetTopTime int64) []Point {
	if len(reference) < 2 || breakoutPrice <= projectedBottom.Value || targetTop <= breakoutPrice || breakoutTime <= projectedBottom.Time || targetTopTime <= breakoutTime {
		return nil
	}
	// Preserve the historical post-peak duration, but keep all amplitude
	// anchors explicit so the projected path stays smooth and interpretable.
	postDuration := historicalBottom.Time - highest(reference).Time
	if postDuration < 180*day {
		postDuration = 180 * day
	}
	endTime := targetTopTime + postDuration
	cycle := append(append([]Point(nil), reference...), historicalBottom)
	oldATH := highest(cycle)
	projected := make([]Point, 0, int((endTime-projectedBottom.Time)/day)+1)
	for ts := projectedBottom.Time; ts <= endTime; ts += day {
		var value float64
		switch {
		case ts <= breakoutTime:
			t := float64(ts-projectedBottom.Time) / float64(breakoutTime-projectedBottom.Time)
			source := interpolateLogPoint(cycle, cycle[0].Time+int64(t*float64(oldATH.Time-cycle[0].Time)))
			ratio := math.Log(source.Value/cycle[0].Value) / math.Log(oldATH.Value/cycle[0].Value)
			value = projectedBottom.Value * math.Exp(math.Log(breakoutPrice/projectedBottom.Value)*ratio)
		case ts <= targetTopTime:
			t := float64(ts-breakoutTime) / float64(targetTopTime-breakoutTime)
			// Reuse the final acceleration of the historical projection for the
			// leg between the old ATH break and the later cycle top.
			shapeWindow := int64(180 * day)
			shapeStart := oldATH.Time - shapeWindow
			if shapeStart < cycle[0].Time {
				shapeStart = cycle[0].Time
			}
			source := interpolateLogPoint(cycle, shapeStart+int64(t*float64(oldATH.Time-shapeStart)))
			first := interpolateLogPoint(cycle, shapeStart)
			ratio := math.Log(source.Value/first.Value) / math.Log(oldATH.Value/first.Value)
			value = breakoutPrice * math.Exp(math.Log(targetTop/breakoutPrice)*ratio)
		default:
			t := float64(ts-targetTopTime) / float64(endTime-targetTopTime)
			source := interpolateLogPoint(cycle, oldATH.Time+int64(t*float64(cycle[len(cycle)-1].Time-oldATH.Time)))
			ratio := math.Log(source.Value/oldATH.Value) / math.Log(cycle[len(cycle)-1].Value/oldATH.Value)
			value = targetTop * math.Exp(math.Log(projectedBottom.Value/targetTop)*ratio)
		}
		projected = append(projected, Point{Time: ts, Value: value})
	}
	return projected
}

func interpolateLogPoint(points []Point, timestamp int64) Point {
	if timestamp <= points[0].Time {
		return points[0]
	}
	if timestamp >= points[len(points)-1].Time {
		return points[len(points)-1]
	}
	i := sort.Search(len(points), func(i int) bool { return points[i].Time >= timestamp })
	a, b := points[i-1], points[i]
	f := float64(timestamp-a.Time) / float64(b.Time-a.Time)
	return Point{Time: timestamp, Value: math.Exp(math.Log(a.Value)*(1-f) + math.Log(b.Value)*f)}
}

// referenceLog linearly interpolates log returns, never beyond the known cycle.
func referenceLog(ref []Point, elapsedDays float64) (float64, bool) {
	t := float64(ref[0].Time) + elapsedDays*float64(day)
	if t < float64(ref[0].Time) || t > float64(ref[len(ref)-1].Time) {
		return 0, false
	}
	i := sort.Search(len(ref), func(i int) bool { return float64(ref[i].Time) >= t })
	if i == 0 {
		return 0, true
	}
	a, b := ref[i-1], ref[i]
	fraction := (t - float64(a.Time)) / float64(b.Time-a.Time)
	return math.Log(a.Value/ref[0].Value)*(1-fraction) + math.Log(b.Value/ref[0].Value)*fraction, true
}

func fitCycle(ref, target []Point) (Fit, bool) {
	best := Fit{LogRMSE: math.Inf(1)}
	// A bounded one-parameter grid plus analytic least squares avoids unrestricted time warping.
	for step := 65; step <= 135; step++ {
		speed := float64(step) / 100
		var xs, ys []float64
		xx, xy := 0.0, 0.0
		valid := true
		for i, p := range target {
			if i%7 != 0 && i != len(target)-1 {
				continue
			}
			x, ok := referenceLog(ref, float64(p.Time-target[0].Time)/float64(day)*speed)
			if !ok {
				valid = false
				break
			}
			y := math.Log(p.Value / target[0].Value)
			xs, ys = append(xs, x), append(ys, y)
			xx += x * x
			xy += x * y
		}
		if !valid || xx <= 1e-12 {
			continue
		}
		amplitude := math.Max(.1, math.Min(1.5, xy/xx))
		sse := 0.0
		for i, x := range xs {
			delta := ys[i] - amplitude*x
			sse += delta * delta
		}
		rmse := math.Sqrt(sse / float64(len(xs)))
		if rmse < best.LogRMSE {
			best = Fit{Speed: speed, Amplitude: amplitude, LogRMSE: rmse, Samples: len(xs), AtBoundary: step == 65 || step == 135 || amplitude == .1 || amplitude == 1.5}
		}
	}
	return best, !math.IsInf(best.LogRMSE, 0)
}

func project(ref, target []Point, fit Fit, name string, futureSpeed float64, horizon int) Scenario {
	last := target[len(target)-1]
	phase := float64(last.Time-target[0].Time) / float64(day) * fit.Speed
	base, ok := referenceLog(ref, phase)
	s := Scenario{Name: name, Speed: futureSpeed, Points: []Point{last}}
	if !ok {
		s.Truncated = true
		return s
	}
	for d := 1; d <= horizon; d++ {
		v, ok := referenceLog(ref, phase+float64(d)*futureSpeed)
		if !ok {
			s.Truncated = true
			break
		}
		// Anchor all scenarios at the last actual close, avoiding a forecast discontinuity.
		s.Points = append(s.Points, Point{Time: last.Time + int64(d)*day, Value: last.Value * math.Exp(fit.Amplitude*(v-base))})
	}
	return s
}

// extendToForecast is the explicit hypothetical branch: once the historical
// analog runs out, connect the last observed close to the Killa-style ATH target
// in log space. It is intentionally labelled a scenario, never an observed value.
func extendToForecast(s *Scenario, last Point, f TopForecast, horizon int) {
	date, price := f.DateCentral, f.PriceCentral
	switch s.Name {
	case "Ralenti":
		date, price = f.DateHigh, f.PriceHigh
	case "Accéléré":
		date, price = f.DateLow, f.PriceLow
	}
	if date <= last.Time || price <= 0 {
		return
	}
	limit := last.Time + int64(horizon)*day
	if date > limit {
		date = limit
	}
	if len(s.Points) > 0 && s.Points[len(s.Points)-1].Time >= date {
		return
	}
	start := len(s.Points)
	if start == 0 || s.Points[start-1].Time != last.Time {
		s.Points = append(s.Points, last)
	}
	for t := last.Time + day; t <= date; t += day {
		progress := float64(t-last.Time) / float64(date-last.Time)
		// Smoothstep avoids a discontinuous slope at the observed/forecast join.
		progress = progress * progress * (3 - 2*progress)
		value := math.Exp(math.Log(last.Value) + progress*(math.Log(price)-math.Log(last.Value)))
		s.Points = append(s.Points, Point{Time: t, Value: value})
	}
	s.Truncated = date < (map[string]int64{"Ralenti": f.DateHigh, "Ajusté": f.DateCentral, "Accéléré": f.DateLow})[s.Name]
}

func validate(ref, target []Point) []Validation {
	rows := []Validation{{Horizon: 90}, {Horizon: 180}}
	// Expanding prefixes spaced by 90 days. No target observation after each origin enters its fit.
	for origin := 180; origin < len(target); origin += 90 {
		prefix := target[:origin+1]
		fit, ok := fitCycle(ref, prefix)
		if !ok {
			continue
		}
		for i := range rows {
			v := &rows[i]
			wanted := prefix[len(prefix)-1].Time + int64(v.Horizon)*day
			j := sort.Search(len(target), func(j int) bool { return target[j].Time >= wanted })
			if j == len(target) || target[j].Time != wanted {
				continue
			}
			projection := project(ref, prefix, fit, "", fit.Speed, v.Horizon)
			if projection.Truncated {
				continue
			}
			predicted := projection.Points[len(projection.Points)-1].Value
			v.ModelLogMAE += math.Abs(math.Log(predicted / target[j].Value))
			v.FlatLogMAE += math.Abs(math.Log(prefix[len(prefix)-1].Value / target[j].Value))
			v.Samples++
		}
	}
	for i := range rows {
		if rows[i].Samples > 0 {
			rows[i].ModelLogMAE /= float64(rows[i].Samples)
			rows[i].FlatLogMAE /= float64(rows[i].Samples)
		}
	}
	return rows
}

func Date(value string) (int64, error) {
	t, err := time.Parse("2006-01-02", value)
	return t.Unix(), err
}
