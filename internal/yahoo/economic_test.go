package yahoo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/config"
)

func calendarFixture(rows [][]any) string {
	value := map[string]any{"finance": map[string]any{"result": []any{map[string]any{"documents": []any{map[string]any{"columns": []any{map[string]string{"id": "country_code"}, map[string]string{"id": "startdatetime"}, map[string]string{"id": "econ_release"}}, "rows": rows}}}}}}
	data, _ := json.Marshal(value)
	return string(data)
}

func TestEconomicCalendarPaginatesAndFiltersUSWindow(t *testing.T) {
	c := NewClient(config.YahooAPIConfig{})
	c.crumbOnce.Do(func() { c.crumb = "test" })
	calls := 0
	c.httpClient.Transport = valuationTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/v1/finance/visualization" {
			t.Fatalf("wrong API: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Offset       int
			Size         int
			EntityIDType string
			Query        calendarQuery
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Offset != calls*100 || body.Size != 100 || body.EntityIDType != "economic_event" {
			t.Fatalf("wrong pagination: %+v", body)
		}
		query, _ := json.Marshal(body.Query)
		for _, want := range []string{"country_code", "US", "startdatetime", "GTE", "LTE"} {
			if !strings.Contains(string(query), want) {
				t.Errorf("missing filter %s", want)
			}
		}
		calls++
		var rows [][]any
		if calls == 1 {
			for i := 0; i < 100; i++ {
				rows = append(rows, []any{"US", "2026-09-10T12:30:00Z", fmt.Sprintf("PPI variant %d", i)})
			}
		} else {
			rows = [][]any{{"US", "2026-09-11T12:30:00Z", "CPI MM, SA"}, {"US", "2026-09-10T12:30:00Z", "PPI variant 0"}, {"GB", "2026-09-11T12:30:00Z", "CPI MM, SA"}, {"US", "2026-09-08T12:30:00Z", "Old event"}, {"US", "2026-10-01T12:30:00Z", "Outside window"}}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(calendarFixture(rows)))}, nil
	})
	events, err := c.GetEconomicEventsWindow(context.Background(), time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC), 21)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(events) != 101 || events[100].Name != "CPI MM, SA" || events[100].Date.Hour() != 8 {
		t.Fatalf("missing second-page inflation event: calls=%d events=%d", calls, len(events))
	}
}

func TestEconomicCalendarRejectsMalformedResponses(t *testing.T) {
	for _, body := range []string{`<html>blocked</html>`, `{}`, `{"finance":{"error":{"code":"Unauthorized"}}}`, `{"finance":{"result":[{"documents":[{"columns":[],"rows":[["US"]]}]}]}}`} {
		c := NewClient(config.YahooAPIConfig{})
		c.crumbOnce.Do(func() {})
		c.httpClient.Transport = valuationTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if _, err := c.GetEconomicEvents(context.Background()); err == nil {
			t.Errorf("accepted malformed response: %s", body)
		}
	}
}

func TestEconomicDocumentPreservesValidRowsAndDST(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	var response economicCalendarResponse
	json.Unmarshal([]byte(calendarFixture([][]any{{"US", "2026-09-10T12:30:00Z", "PPI"}, {"US", "2026-12-10T13:30:00Z", "CPI"}, {"US", "TBD", "Unknown date"}, {"US"}})), &response)
	events, err := parseEconomicDocument(response.Finance.Result[0].Documents[0], loc)
	if err == nil || len(events) != 2 {
		t.Fatal("malformed rows silently ignored or valid rows lost")
	}
	for _, e := range events {
		if e.Date.Hour() != 8 || e.Date.Minute() != 30 {
			t.Errorf("wrong Eastern time: %s", e.Date)
		}
	}
}

func TestLiveEconomicCalendar(t *testing.T) {
	if os.Getenv("STOCK_TEST_LIVE_CALENDAR") != "1" {
		t.Skip("live Yahoo check is opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := NewClient(config.YahooAPIConfig{Timeout: 15, UserAgent: "Mozilla/5.0"})
	events, err := c.GetEconomicEventsWindow(ctx, time.Now(), 21)
	for _, event := range events {
		t.Logf("%s | %s | %s", event.Country, event.Date.Format(time.RFC3339), event.Name)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("empty live calendar")
	}
}
