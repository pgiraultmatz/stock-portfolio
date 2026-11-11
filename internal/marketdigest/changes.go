package marketdigest

import (
	"math"
	"stock-portfolio/internal/chartcalc"
	"time"
)

// RollingChanges uses the latest daily closes and the exchange calendar date.
func RollingChanges(c []chartcalc.Candle, timezone string, now time.Time) ChangeSummary {
	var result ChangeSummary
	if len(c) < 2 || now.IsZero() {
		return result
	}
	valid := func(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
	for i, b := range c {
		if b.Time <= 0 || !valid(b.Close) || time.Unix(b.Time, 0).After(now.Add(5*time.Minute)) || i > 0 && b.Time <= c[i-1].Time {
			return result
		}
	}
	last := c[len(c)-1]
	if time.Unix(last.Time, 0).Before(now.AddDate(0, 0, -7)) {
		return result
	}
	result.DailyChange = (last.Close/c[len(c)-2].Close - 1) * 100
	result.HasDailyChange = true
	loc, err := time.LoadLocation(timezone)
	if err != nil || timezone == "" {
		return result
	}
	d := time.Unix(last.Time, 0).In(loc)
	target := time.Date(d.Year(), d.Month(), d.Day()-7, 0, 0, 0, 0, loc)
	for i := len(c) - 2; i >= 0; i-- {
		at := time.Unix(c[i].Time, 0).In(loc)
		day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
		if !day.After(target) {
			if day.Before(target.AddDate(0, 0, -7)) {
				break
			}
			result.WeeklyChange = (last.Close/c[i].Close - 1) * 100
			result.HasWeeklyChange = true
			break
		}
	}
	return result
}
