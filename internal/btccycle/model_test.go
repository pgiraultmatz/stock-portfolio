package btccycle

import (
	"math"
	"reflect"
	"testing"
)

func synthetic(targetDays int, speed, amplitude float64) ([]Point, Options) {
	start, _ := Date("2015-01-01")
	var ref, history []Point
	refStart := start + 600*day
	for d := 0; d < 1800; d++ {
		p := Point{Time: start + int64(d)*day, Value: 100 * math.Exp(.0015*float64(d)+.25*math.Sin(float64(d)/53))}
		history = append(history, p)
		if p.Time >= refStart {
			ref = append(ref, p)
		}
	}
	targetStart := refStart + 1200*day
	for d := 0; d < targetDays; d++ {
		v, ok := referenceLog(ref, float64(d)*speed)
		if !ok {
			panic("synthetic target beyond reference")
		}
		history = append(history, Point{Time: targetStart + int64(d)*day, Value: 200 * math.Exp(amplitude*v)})
	}
	return history, Options{ReferenceStart: refStart, TargetStart: targetStart, AsOf: history[len(history)-1].Time, Horizon: 180}
}

func TestRecoversKnownTimeAndAmplitudeScaling(t *testing.T) {
	history, opts := synthetic(600, 1.1, .7)
	r, err := Analyze(history, opts)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r.Fit.Speed-1.1) > 1e-9 || math.Abs(r.Fit.Amplitude-.7) > 1e-9 || r.Fit.LogRMSE > 1e-9 {
		t.Fatalf("incorrect fit: %+v", r.Fit)
	}
	if r.ReferenceATH.Time >= r.TargetStart.Time {
		t.Fatal("reference includes target observations")
	}
	if r.TopForecast == nil || r.TopForecast.PriceCentral <= r.ObservedHigh.Value || r.TopForecast.PreviousCycleGain <= 0 {
		t.Fatal("missing Killa-style top forecast")
	}
	last := r.Actual[len(r.Actual)-1]
	for _, scenario := range r.Scenarios {
		if scenario.Points[0] != last || len(scenario.Points) != 181 {
			t.Fatalf("discontinuous or incomplete projection: name=%s len=%d first=%+v last=%+v expected=%+v", scenario.Name, len(scenario.Points), scenario.Points[0], scenario.Points[len(scenario.Points)-1], last)
		}
		for i, p := range scenario.Points {
			if p.Value <= 0 || math.IsInf(p.Value, 0) || p.Time != last.Time+int64(i)*day {
				t.Fatal("invalid projection")
			}
		}
	}
	for _, validation := range r.Validation {
		if validation.Samples == 0 || validation.ModelLogMAE > 1e-8 || validation.FlatLogMAE <= validation.ModelLogMAE {
			t.Fatalf("validation failed: %+v", validation)
		}
	}
}

func TestNoFutureTargetLeakage(t *testing.T) {
	history, opts := synthetic(800, 1, .8)
	opts.AsOf = opts.TargetStart + 400*day
	a, err := Analyze(history, opts)
	if err != nil {
		t.Fatal(err)
	}
	for i := range history {
		if history[i].Time > opts.AsOf {
			history[i].Value = math.NaN()
		}
	}
	b, err := Analyze(history, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("future prices affected fit, milestones, validation or projection")
	}
}

func TestStopsAtReferenceBoundary(t *testing.T) {
	history, opts := synthetic(1150, 1, .8)
	r, err := Analyze(history, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range r.Scenarios {
		if len(scenario.Points) < 181 {
			t.Fatal("hypothetical curve did not continue after historical reference")
		}
	}
	ref := history[600:1800]
	if _, ok := referenceLog(ref, 1200); ok {
		t.Fatal("extrapolated reference")
	}
}

func TestValidationAndDegenerateData(t *testing.T) {
	for _, tc := range []string{"short", "missing anchor", "gap", "zero", "duplicate", "invalid horizon", "reversed anchors"} {
		t.Run(tc, func(t *testing.T) {
			history, opts := synthetic(600, 1, .8)
			switch tc {
			case "short":
				opts.AsOf = opts.TargetStart + 100*day
			case "missing anchor":
				history = append(history[:600], history[601:]...)
			case "gap":
				history = append(history[:700], history[710:]...)
			case "zero":
				history[42].Value = 0
			case "duplicate":
				history = append(history, history[42])
			case "invalid horizon":
				opts.Horizon = 1000
			case "reversed anchors":
				opts.ReferenceStart = opts.TargetStart
			}
			if _, err := Analyze(history, opts); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestShortHistoryHasNoInventedValidation(t *testing.T) {
	history, opts := synthetic(181, 1, .7)
	r, err := Analyze(history, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range r.Validation {
		if v.Samples != 0 {
			t.Fatal("invented backtest")
		}
	}
}

func TestBottomRuleUsesCompletedSixMonthCandles(t *testing.T) {
	start, _ := Date("2018-01-01")
	var points []Point
	for d := 0; d < 4*365; d++ {
		period := d / 182
		value := 100.0 - float64(period)*10
		if period == 1 || period == 2 {
			value -= float64(d%182) * 0.05
		}
		if period >= 3 {
			value += float64(d - 3*182)
		}
		points = append(points, Point{Time: start + int64(d)*day, Value: value})
	}
	signal := analyzeBottom(points, points)
	if !signal.TwoRedSixMonthCandles || signal.Status == "Indisponible" {
		t.Fatalf("missing bottom rule: %+v", signal)
	}
	// An incomplete current half-year must never be treated as the second red candle.
	incomplete := append([]Point(nil), points...)
	incomplete = append(incomplete, Point{Time: start + 4*365*day + 30*day, Value: 20})
	if got := analyzeBottom(incomplete, incomplete); got.TwoRedSixMonthCandles && got.SecondRedClose > incomplete[len(incomplete)-2].Time {
		t.Fatal("used an incomplete six-month candle")
	}
}
