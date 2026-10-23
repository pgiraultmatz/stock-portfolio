package webapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"stock-portfolio/internal/models"
)

func TestMetricsUseSharedSnapshotAndInvalidateLegacyBadge(t *testing.T) {
	e := &models.Evaluation{Version: models.EvaluationVersion, EvaluatedAt: time.Now().UTC(), Eligible: true, Signal: "ACCUMULATE", Score: 2, Warnings: []string{"Provisoire"}}
	store := &GistStore{stockData: &StockDataFile{Stocks: map[string]StockFundamentals{
		"NEW": {Signal: "STRONG SELL", Evaluation: e}, "OLD": {Signal: "STRONG BUY"},
	}}}
	s := &Server{store: store}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/metrics", nil).WithContext(context.WithValue(context.Background(), ctxUserID, "test"))
	s.getMetrics(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var got map[string]StockMetrics
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["NEW"].Signal != "ACCUMULATE" || got["NEW"].SignalNote != "Provisoire" || !reflect.DeepEqual(got["NEW"].Evaluation, e) {
		t.Fatalf("shared badge changed: %+v", got["NEW"])
	}
	if got["OLD"].Signal != "À recalculer" || got["OLD"].Evaluation != nil {
		t.Fatal("legacy badge still displayed")
	}
}
