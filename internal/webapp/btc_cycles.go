package webapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"stock-portfolio/internal/btccycle"
)

func cycleOptions(r *http.Request, now time.Time) (btccycle.Options, error) {
	q := r.URL.Query()
	values := []string{q.Get("referenceStart"), q.Get("targetStart"), q.Get("asOf")}
	defaults := []string{"2018-12-15", "2022-11-21", now.UTC().AddDate(0, 0, -1).Format("2006-01-02")}
	var dates [3]int64
	for i, v := range values {
		if v == "" {
			v = defaults[i]
		}
		t, err := btccycle.Date(v)
		if err != nil {
			return btccycle.Options{}, fmt.Errorf("date invalide : format YYYY-MM-DD requis")
		}
		dates[i] = t
	}
	if dates[2] >= now.UTC().Truncate(24*time.Hour).Unix() {
		return btccycle.Options{}, fmt.Errorf("la date d'observation doit précéder aujourd'hui (clôtures UTC complètes)")
	}
	horizon := 180
	if raw := q.Get("horizon"); raw != "" {
		var err error
		horizon, err = strconv.Atoi(raw)
		if err != nil {
			return btccycle.Options{}, fmt.Errorf("horizon invalide")
		}
	}
	if dates[0] >= dates[1] || dates[1] >= dates[2] || horizon < 30 || horizon > 730 {
		return btccycle.Options{}, fmt.Errorf("ancrages non chronologiques ou horizon hors de 30 à 730 jours")
	}
	return btccycle.Options{ReferenceStart: dates[0], TargetStart: dates[1], AsOf: dates[2], Horizon: horizon}, nil
}

func loadBTCCycleHistory(ctx context.Context, refresh bool) (ChartResponse, error) {
	path := chartCachePath("BTC-USD", "max", "1d")
	ttl := 6 * time.Hour
	cached, hasCache := loadChartCache(path, ttl)
	if hasCache && !cached.Stale && !refresh && len(cached.Candles) >= 1000 {
		return cached, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Yahoo's `range=max` response is occasionally sparse. Fetch bounded windows
	// and merge them so cycle anchors are backed by complete daily candles.
	windows := [][2]time.Time{
		{time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2018, 12, 1, 0, 0, 0, 0, time.UTC), time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)},
		{time.Date(2022, 12, 1, 0, 0, 0, 0, time.UTC), time.Now().UTC().Add(48 * time.Hour)},
	}
	var data ChartResponse
	var err error
	for _, window := range windows {
		var part ChartResponse
		part, err = fetchYahooChartPeriod(ctx, "BTC-USD", window[0], window[1], "1d")
		if err != nil {
			break
		}
		if data.Symbol == "" {
			data = part
		} else {
			data.Candles = append(data.Candles, part.Candles...)
		}
	}
	if err != nil {
		if hasCache && len(cached.Candles) >= 1000 {
			cached.Stale = true
			return cached, nil
		}
		return ChartResponse{}, err
	}
	sort.Slice(data.Candles, func(i, j int) bool { return data.Candles[i].Time < data.Candles[j].Time })
	unique := data.Candles[:0]
	for _, candle := range data.Candles {
		if len(unique) == 0 || candle.Time != unique[len(unique)-1].Time {
			unique = append(unique, candle)
		}
	}
	data.Candles = unique
	enrichChartIndicators(&data)
	data.Range, data.Interval = "max", "1d"
	data.UpdatedAt, data.ValidUntil = time.Now(), time.Now().Add(ttl)
	saveChartCache(path, data)
	return data, nil
}

func (s *Server) getBTCCycles(w http.ResponseWriter, r *http.Request) {
	serveBTCCycles(w, r, time.Now(), loadBTCCycleHistory)
}

func serveBTCCycles(w http.ResponseWriter, r *http.Request, now time.Time, load func(context.Context, bool) (ChartResponse, error)) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	opts, err := cycleOptions(r, now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	history, err := load(r.Context(), parseBoolQuery(r.URL.Query().Get("refresh")))
	if err != nil {
		http.Error(w, "historique BTC indisponible", http.StatusBadGateway)
		return
	}
	points := make([]btccycle.Point, 0, len(history.Candles))
	for _, c := range history.Candles {
		date := time.Unix(c.Time, 0).UTC().Truncate(24 * time.Hour).Unix()
		if date <= opts.AsOf {
			points = append(points, btccycle.Point{Time: date, Value: c.Close})
		}
	}
	result, err := btccycle.Analyze(points, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if history.Stale {
		result.Warnings = append(result.Warnings, "Source Yahoo indisponible : historique en cache non actualisé.")
	}
	if result.AsOf < opts.AsOf {
		result.Warnings = append(result.Warnings, "La dernière clôture disponible précède la date demandée.")
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		*btccycle.Result
		UpdatedAt time.Time `json:"updatedAt"`
		Stale     bool      `json:"stale"`
	}{result, history.UpdatedAt, history.Stale})
}
