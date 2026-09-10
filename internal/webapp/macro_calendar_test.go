package webapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStockDataProvidesFilteredCalendarWithoutMutatingCache(t *testing.T) {
	date := time.Now().Add(time.Hour)
	store := &GistStore{stockData: &StockDataFile{
		Stocks: map[string]StockFundamentals{"ORCL": {NextEarnings: "2026-12-10"}},
		MacroEvents: []MacroEvent{
			{Name: "Initial Jobless Clm", Country: "US", Date: date},
			{Name: "PPI Final Demand MM", Country: "US", Date: date},
			{Name: "PPI Final Demand YY", Country: "US", Date: date},
			{Name: "PPI exFood/Energy MM", Country: "US", Date: date},
			{Name: "PPI Final Demand MM", Country: "GB", Date: date},
		},
	}}
	s := &Server{store: store}
	w := httptest.NewRecorder()
	s.getStockData(w, httptest.NewRequest(http.MethodGet, "/api/stock-data", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		StockDataFile
		Highlights []MacroEvent `json:"macro_highlights"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Highlights) != 1 || response.Highlights[0].Name != "PPI US - MoM" || response.Highlights[0].Country != "US" {
		t.Fatalf("wrong highlights: %+v", response.Highlights)
	}
	if len(response.MacroEvents) != 5 || store.stockData.MacroEvents[1].Name != "PPI Final Demand MM" || response.Stocks["ORCL"].NextEarnings != "2026-12-10" {
		t.Fatal("raw events or earnings changed")
	}
	store.stockData.MacroEvents = nil
	w = httptest.NewRecorder()
	s.getStockData(w, httptest.NewRequest(http.MethodGet, "/api/stock-data", nil))
	if !strings.Contains(w.Body.String(), `"macro_highlights":[]`) {
		t.Fatal("missing explicit empty calendar")
	}
}

func TestMacroCalendarUsesHighlightsAndInvalidatesRawBrowserCache(t *testing.T) {
	content, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(content)
	if !strings.Contains(html, "'macroHighlightsCache-v2'") || !strings.Contains(html, "Array.isArray(data.macro_highlights)") || strings.Contains(html, "Array.isArray(data.macro_events)") {
		t.Fatal("calendar must use only highlights, with a new browser cache key")
	}
}
