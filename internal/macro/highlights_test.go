package macro

import (
	"testing"
	"time"
)

func TestHighlightsIncludeInflationAndPreserveOfficialFOMC(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	ppi := time.Date(2026, 9, 10, 12, 30, 0, 0, time.UTC)
	cpi := ppi.AddDate(0, 0, 1)
	fomc := time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)
	official := []Event{{Name: "FOMC meeting + Summary of Economic Projections", Category: "FOMC", Source: "Federal Reserve", Date: fomc}}
	yahoo := []Event{{Name: "Mortgage Market Index", Country: "US", Date: now}, {Name: "PPI Final Demand MM", Country: "US", Date: ppi}, {Name: "PPI Final Demand YY", Country: "US", Date: ppi}, {Name: "CPI MM, SA", Country: "US", Date: cpi}, {Name: "Core CPI YY, NSA", Country: "US", Date: cpi}, {Name: "CPI MM", Country: "GB", Date: cpi}, {Name: "Fed Funds Tgt Rate", Country: "US", Date: fomc}}
	got := CalendarHighlights(Merge(official, yahoo), now, 21)
	if len(got) != 3 || got[0].Category != "PPI" || got[1].Category != "CPI" || got[2].Name != official[0].Name {
		t.Fatalf("wrong major releases: %+v", got)
	}
	if got[0].Date.Hour() != 8 || got[0].Date.Minute() != 30 {
		t.Fatalf("wrong Eastern time: %s", got[0].Date)
	}
}

func TestUpcomingUsesEasternDayAcrossUTCDateBoundary(t *testing.T) {
	now := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC) // Still September 9 in New York.
	events := []Event{{Date: time.Date(2026, 9, 9, 12, 30, 0, 0, time.UTC)}}
	if len(Upcoming(events, now, 21)) != 1 {
		t.Fatal("current Eastern day excluded by UTC boundary")
	}
}

func TestHighlightsKeepOnlyHeadlineMonthlyPPIFromNoisyDay(t *testing.T) {
	now := time.Date(2026, 9, 10, 7, 0, 0, 0, easternLocation())
	date := now.Add(90 * time.Minute)
	names := []string{
		"Initial Jobless Clm", "Cont Jobless Clm *", "Initial Jobless Clm *", "Jobless Clm 4Wk Avg *",
		"PPI Final Demand MM", "PPI Final Demand YY", "PPI ex Food/Energy/Tr MM", "PPI ex Food/Energy/Tr YY",
		"PPI exFood/Energy MM", "PPI exFood/Energy YY", "Core PPI MoM",
		"Exist. Home Sales % Chg", "Existing Home Sales", "Wholesale Invt(y), R MM", "Wholesale Sales MM",
		"EIA-Nat Gas Chg Bcf*", "EIA Weekly Crude Imports*", "EIA Wkly Crude Stk*", "EIA Wkly Refn Util*",
		"Mortgage Market Index", "ADP Employment Change", "ISM Manufacturing PMI", "Retail Sales MM", "JOLTS Job Openings",
	}
	var events []Event
	for _, name := range names {
		events = append(events, Event{Name: name, Date: date, Country: "US", Source: "Yahoo Finance", Category: "Economic"})
	}
	// Duplicate provider headline, plus another country's PPI, must not add rows.
	events = append(events, events[4], Event{Name: "PPI MM", Country: "GB", Date: date})
	got := CalendarHighlights(events, now, 21)
	if len(got) != 1 || got[0].Name != "PPI US - MoM" || !got[0].Date.Equal(date) {
		t.Fatalf("expected only headline monthly PPI: %+v", got)
	}
	if events[4].Name != "PPI Final Demand MM" {
		t.Fatal("raw cache was modified")
	}
}

func TestHighlightsDoNotRelabelAnnualOrCoreInflationAsHeadlineMoM(t *testing.T) {
	now := time.Date(2026, 9, 10, 7, 0, 0, 0, easternLocation())
	for _, name := range []string{"PPI Final Demand YY", "PPI exFood/Energy MM", "Core PPI MoM", "CPI YY, NSA", "Core CPI MM, SA", "PCE Price Index YoY"} {
		if got := CalendarHighlights([]Event{{Name: name, Country: "US", Date: now}}, now, 1); len(got) != 0 {
			t.Errorf("unexpected monthly headline for %q: %+v", name, got)
		}
	}
	for _, name := range []string{"PPI MoM", "PPI Final Demand MM", "Producer Price Index (MoM)", "CPI MM, SA", "Inflation Rate MoM", "PCE Price Index MM", "Core PCE Price Index MoM"} {
		if got := CalendarHighlights([]Event{{Name: name, Country: "US", Date: now}}, now, 1); len(got) != 1 {
			t.Errorf("missing monthly release %q", name)
		}
	}
}
