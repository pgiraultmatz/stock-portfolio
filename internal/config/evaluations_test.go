package config

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/models"
)

func TestSaveEvaluationsPatchesOnlyStockDataWithoutConditionalHeaders(t *testing.T) {
	t.Setenv("GIST_ID", "test-gist")
	t.Setenv("GH_TOKEN", "test-token")
	old := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = old })
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusForbidden, http.StatusPreconditionFailed} {
		calls := 0
		http.DefaultClient = &http.Client{Transport: financialTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Error("missing authentication")
			}
			if r.Method == http.MethodGet {
				return &http.Response{StatusCode: 200, Header: http.Header{"Etag": []string{`"v1"`}}, Body: io.NopCloser(strings.NewReader(`{"files":{"stock-data.json":{"content":"{\"stocks\":{\"A\":{\"peg_ratio\":1.2}},\"macro_events\":[{\"name\":\"CPI\"}]}"},"stock-config.json":{"content":"unchanged"}}}`))}, nil
			}
			if r.Method != http.MethodPatch || r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "" {
				t.Errorf("unexpected request: %s %v", r.Method, r.Header)
			}
			var payload struct {
				Files map[string]struct {
					Content string `json:"content"`
				} `json:"files"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Files) != 1 || !strings.Contains(payload.Files["stock-data.json"].Content, "peg_ratio") || !strings.Contains(payload.Files["stock-data.json"].Content, "CPI") {
				t.Fatal("patch lost data or touched other files")
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"message":"GitHub error explanation"}`))}, nil
		})}
		err := SaveEvaluations(context.Background(), map[string]models.Evaluation{"A": {Version: models.EvaluationVersion, EvaluatedAt: time.Now(), Eligible: true, Signal: "BUY"}})
		if (err != nil) != (status != 200) || calls != 2 {
			t.Fatalf("status=%d calls=%d err=%v", status, calls, err)
		}
		if status != http.StatusOK && !strings.Contains(err.Error(), "GitHub error explanation") {
			t.Fatalf("API error explanation missing: %v", err)
		}
	}
}

func TestEvaluationAPIErrorHandlesNonJSONResponse(t *testing.T) {
	response := &http.Response{StatusCode: 502, Body: io.NopCloser(strings.NewReader("<html>Bad Gateway</html>"))}
	if err := evaluationAPIError("saving evaluations", response); err.Error() != "saving evaluations: HTTP 502" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMergeEvaluationsPreservesOtherData(t *testing.T) {
	raw := []byte(`{"updated_at":"2026-10-20T12:00:00Z","macro_events":[{"name":"CPI"}],"future_field":{"keep":true},"stocks":{"AAA":{"peg_ratio":1.2,"unknown":"keep","signal":"STRONG SELL"},"BBB":{"signal":"HOLD"}}}`)
	e := models.Evaluation{Version: models.EvaluationVersion, EvaluatedAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), Eligible: true, Signal: "BUY", Warnings: []string{"Provisoire"}}
	out, err := mergeEvaluations(raw, map[string]models.Evaluation{"AAA": e})
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	json.Unmarshal(raw, &before)
	json.Unmarshal(out, &after)
	for _, key := range []string{"updated_at", "macro_events", "future_field"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Errorf("changed %s", key)
		}
	}
	b := before["stocks"].(map[string]any)
	a := after["stocks"].(map[string]any)
	if !reflect.DeepEqual(b["BBB"], a["BBB"]) {
		t.Fatal("unrelated stock changed")
	}
	fields := a["AAA"].(map[string]any)
	if fields["peg_ratio"] != 1.2 || fields["unknown"] != "keep" || fields["signal"] != "BUY" || fields["signal_note"] != "Provisoire" {
		t.Fatalf("merge lost data: %s", out)
	}
	var typed StockDataFile
	if err := json.Unmarshal(out, &typed); err != nil {
		t.Fatal(err)
	}
	if typed.Stocks["AAA"].Evaluation.Signal != "BUY" {
		t.Fatal("typed snapshot lost")
	}
	old := e
	old.EvaluatedAt = e.EvaluatedAt.Add(-time.Hour)
	old.Signal = "SELL"
	again, err := mergeEvaluations(out, map[string]models.Evaluation{"AAA": old})
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(out) {
		t.Fatal("older snapshot overwrote newer evaluation")
	}
}

func TestMergeEvaluationsRejectsMalformedData(t *testing.T) {
	for _, raw := range []string{`{`, `null`, `{"stocks":false}`, `{"stocks":{"A":null}}`} {
		_, err := mergeEvaluations([]byte(raw), map[string]models.Evaluation{"A": {Version: models.EvaluationVersion}})
		if err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
