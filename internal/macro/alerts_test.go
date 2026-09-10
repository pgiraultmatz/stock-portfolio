package macro

import (
	"strings"
	"testing"
	"time"
)

func TestNearbyAlertWindows(t *testing.T) {
	now := time.Date(2026, 9, 9, 7, 0, 0, 0, easternLocation())
	for _, tc := range []struct {
		name, category string
		days           int
		want           bool
	}{
		{"CPI MM, SA", "Economic", -1, false},
		{"CPI MM, SA", "Economic", 0, true},
		{"CPI MM, SA", "Economic", 1, true},
		{"CPI MM, SA", "Economic", 2, false},
		{"PPI Final Demand MM", "Economic", 1, true},
		{"PPI Final Demand MM", "Economic", 2, false},
		{"FOMC meeting", "FOMC", 0, true},
		{"FOMC meeting", "FOMC", 3, true},
		{"FOMC meeting", "FOMC", 4, false},
		{"GDP Final", "Economic", 0, false},
	} {
		date := now.AddDate(0, 0, tc.days).Add(90 * time.Minute)
		got := NearbyAlerts([]Event{{Name: tc.name, Category: tc.category, Country: "US", Date: date}}, now)
		if (len(got) == 1) != tc.want {
			t.Errorf("%s day %d: %+v", tc.name, tc.days, got)
		}
	}
}

func TestNearbyAlertsPassedTimeDoesNotClaimPublication(t *testing.T) {
	date := time.Date(2026, 9, 11, 8, 30, 0, 0, easternLocation())
	events := []Event{{Name: "CPI MM, SA", Country: "US", Date: date}}
	for _, now := range []time.Time{date, date.Add(time.Hour)} {
		got := NearbyAlerts(events, now)
		if len(got) != 1 || got[0].State != "elapsed" || !strings.Contains(got[0].Message, "publication et résultat non vérifiés") {
			t.Fatalf("incorrect elapsed message: %+v", got)
		}
	}
	if got := NearbyAlerts(events, date.AddDate(0, 0, 1)); len(got) != 0 {
		t.Fatal("yesterday's alert retained")
	}
}

func TestNearbyAlertsDeduplicateAndUseEasternCalendarDays(t *testing.T) {
	// Friday 01:00 UTC is still Thursday evening in Toronto.
	now := time.Date(2026, 9, 11, 1, 0, 0, 0, time.UTC)
	date := time.Date(2026, 9, 11, 12, 30, 0, 0, time.UTC)
	events := []Event{{Name: "CPI MM, SA", Country: "US", Date: date}, {Name: "Core CPI YY, NSA", Country: "US", Date: date}}
	got := NearbyAlerts(events, now)
	if len(got) != 1 || !strings.Contains(got[0].Title, "Demain") || !strings.Contains(got[0].Title, "08:30") {
		t.Fatalf("wrong timezone or duplicate: %+v", got)
	}
	// Three calendar days across the end of daylight saving take 73 hours.
	now = time.Date(2026, 10, 30, 0, 0, 0, 0, easternLocation())
	fomc := Event{Name: "FOMC meeting", Category: "FOMC", Date: time.Date(2026, 11, 2, 14, 0, 0, 0, easternLocation())}
	got = NearbyAlerts([]Event{fomc}, now)
	if len(got) != 1 || !strings.Contains(got[0].Title, "Dans 3 jours") || !strings.Contains(got[0].Title, "14:00") {
		t.Fatalf("DST changed alert window: %+v", got)
	}
}
