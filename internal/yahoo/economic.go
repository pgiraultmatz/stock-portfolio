package yahoo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"
)

const economicCalendarURL = "https://query1.finance.yahoo.com/v1/finance/visualization"

type EconomicEvent struct {
	Name    string
	Date    time.Time
	Source  string
	Country string
}

type calendarQuery struct {
	Operator string `json:"operator"`
	Operands []any  `json:"operands"`
}

type calendarDocument struct {
	Columns []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Label string `json:"label"`
	} `json:"columns"`
	Rows [][]json.RawMessage `json:"rows"`
}

type economicCalendarResponse struct {
	Finance struct {
		Result []struct {
			Documents []calendarDocument `json:"documents"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"finance"`
}

// GetEconomicEvents retrieves the full upcoming US calendar, not the default
// day shown on Yahoo's HTML page. The internal JSON endpoint may change.
func (c *Client) GetEconomicEvents(ctx context.Context) ([]EconomicEvent, error) {
	return c.GetEconomicEventsWindow(ctx, time.Now(), 21)
}

func (c *Client) GetEconomicEventsWindow(ctx context.Context, now time.Time, days int) ([]EconomicEvent, error) {
	if days <= 0 {
		days = 21
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, err
	}
	now = now.In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, days+1)
	if err := c.initCrumb(ctx); err != nil {
		return nil, fmt.Errorf("calendar session: %w", err)
	}
	var events []EconomicEvent
	seen := make(map[string]bool)
	for offset := 0; offset < 5000; offset += 100 {
		// Query a padded UTC range, then apply exact Eastern calendar boundaries.
		payload := struct {
			SortType      string        `json:"sortType"`
			EntityIDType  string        `json:"entityIdType"`
			SortField     string        `json:"sortField"`
			IncludeFields []string      `json:"includeFields"`
			Size          int           `json:"size"`
			Offset        int           `json:"offset"`
			Query         calendarQuery `json:"query"`
		}{"ASC", "economic_event", "startdatetime", []string{"econ_release", "country_code", "startdatetime"}, 100, offset,
			calendarQuery{"AND", []any{
				calendarQuery{"GTE", []any{"startdatetime", start.AddDate(0, 0, -1).Format("2006-01-02")}},
				calendarQuery{"LTE", []any{"startdatetime", end.AddDate(0, 0, 1).Format("2006-01-02")}},
				calendarQuery{"EQ", []any{"country_code", "US"}},
			}}}
		body, err := json.Marshal(payload)
		if err != nil {
			return events, err
		}
		endpoint := economicCalendarURL + "?" + url.Values{"lang": {"en-US"}, "region": {"US"}, "crumb": {c.crumb}}.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return events, err
		}
		req.Header.Set("User-Agent", c.config.UserAgent)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return events, fmt.Errorf("fetching economic calendar: %w", err)
		}
		var result economicCalendarResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return events, fmt.Errorf("economic calendar HTTP %d", resp.StatusCode)
		}
		if decodeErr != nil {
			return events, fmt.Errorf("decoding economic calendar: %w", decodeErr)
		}
		if result.Finance.Error != nil {
			return events, fmt.Errorf("economic calendar API: %s", result.Finance.Error.Code)
		}
		if len(result.Finance.Result) == 0 || len(result.Finance.Result[0].Documents) == 0 {
			return events, fmt.Errorf("economic calendar missing documents")
		}
		rows := 0
		added := 0
		var pageErr error
		for _, doc := range result.Finance.Result[0].Documents {
			parsed, err := parseEconomicDocument(doc, loc)
			pageErr = errors.Join(pageErr, err)
			rows += len(doc.Rows)
			for _, event := range parsed {
				if event.Country != "US" || event.Date.Before(start) || !event.Date.Before(end) {
					continue
				}
				key := strings.ToLower(event.Name) + "|" + event.Date.Format(time.RFC3339)
				if seen[key] {
					continue
				}
				seen[key] = true
				events = append(events, event)
				added++
			}
		}
		sort.Slice(events, func(i, j int) bool {
			if events[i].Date.Equal(events[j].Date) {
				return events[i].Name < events[j].Name
			}
			return events[i].Date.Before(events[j].Date)
		})
		if pageErr != nil {
			return events, pageErr
		}
		if rows < 100 {
			return events, nil
		}
		if added == 0 && offset > 0 {
			return events, fmt.Errorf("economic calendar pagination made no progress")
		}
	}
	return events, fmt.Errorf("economic calendar pagination limit reached")
}

func parseEconomicDocument(doc calendarDocument, loc *time.Location) ([]EconomicEvent, error) {
	if len(doc.Rows) == 0 {
		return nil, nil
	}
	indices := map[string]int{}
	for i, col := range doc.Columns {
		for _, key := range []string{col.ID, col.Name, col.Label} {
			if key != "" {
				indices[strings.ToLower(key)] = i
			}
		}
	}
	find := func(keys ...string) int {
		for _, key := range keys {
			if i, ok := indices[key]; ok {
				return i
			}
		}
		return -1
	}
	nameIndex, countryIndex, dateIndex := find("econ_release", "event"), find("country_code", "country code", "region"), find("startdatetime", "event time")
	if nameIndex < 0 || countryIndex < 0 || dateIndex < 0 {
		return nil, fmt.Errorf("economic calendar required columns missing")
	}
	var events []EconomicEvent
	var invalid bool
	for _, row := range doc.Rows {
		if len(row) <= max(nameIndex, max(countryIndex, dateIndex)) {
			invalid = true
			continue
		}
		var name, country, dateText string
		if json.Unmarshal(row[nameIndex], &name) != nil || json.Unmarshal(row[countryIndex], &country) != nil {
			invalid = true
			continue
		}
		country = strings.ToUpper(strings.TrimSpace(country))
		if country != "US" {
			continue
		}
		if json.Unmarshal(row[dateIndex], &dateText) != nil {
			invalid = true
			continue
		}
		at, err := time.Parse(time.RFC3339, dateText)
		if err != nil || strings.TrimSpace(name) == "" {
			invalid = true
			continue
		}
		events = append(events, EconomicEvent{Name: strings.TrimSpace(name), Country: country, Date: at.In(loc), Source: "Yahoo Finance"})
	}
	if invalid {
		return events, fmt.Errorf("economic calendar contains malformed event rows")
	}
	return events, nil
}
