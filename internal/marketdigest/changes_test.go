package marketdigest

import (
	"math"
	"stock-portfolio/internal/chartcalc"
	"testing"
	"time"
)

func TestRollingChangesHolidayAndTimezone(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	day := func(d int, v float64) chartcalc.Candle {
		return chartcalc.Candle{Time: time.Date(2026, 11, d, 9, 30, 0, 0, loc).Unix(), Close: v}
	}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, loc)
	c := []chartcalc.Candle{day(2, 100), day(4, 105), day(9, 110), day(10, 120)}
	r := RollingChanges(c, loc.String(), now)
	if !r.HasDailyChange || !r.HasWeeklyChange || math.Abs(r.DailyChange-9.090909) > 0.001 || math.Abs(r.WeeklyChange-20) > 0.001 {
		t.Fatalf("unexpected %+v", r)
	}
	if RollingChanges(c, "", now).HasWeeklyChange {
		t.Fatal("missing timezone must not infer weekly date")
	}
	if RollingChanges(c[1:], loc.String(), now).HasWeeklyChange {
		t.Fatal("missing comparison must be unavailable")
	}
}
