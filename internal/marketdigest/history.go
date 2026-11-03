package marketdigest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"stock-portfolio/internal/models"
)

const historyVersion = 1

const (
	sectionBuys      = "buys"
	sectionSells     = "sells"
	sectionPullbacks = "pullbacks"
	sectionReversals = "reversals"
)

// SectionHistory records displayed tickers, not prices or the entire eligible universe.
type SectionHistory struct {
	Version  int                 `json:"version"`
	Scope    string              `json:"scope"`
	Sections map[string][]string `json:"sections"`
}

func HistoryScope(timeframe string, dailyEMA, weeklyEMA float64, limit int) string {
	return fmt.Sprintf("%s/%s/%g/%g/%d", models.EvaluationVersion, timeframe, dailyEMA, weeklyEMA, limit)
}

func SnapshotSections(options RankingOptions, scope string) *SectionHistory {
	buys, sells, _ := rankEvaluations(options)
	h := &SectionHistory{Version: historyVersion, Scope: scope, Sections: map[string][]string{}}
	for name, picks := range map[string][]evaluationPick{
		sectionBuys: buys, sectionSells: sells,
		sectionPullbacks: rankPullbacks(options), sectionReversals: rankReversals(options),
	} {
		h.Sections[name] = []string{}
		for _, pick := range picks {
			h.Sections[name] = append(h.Sections[name], pick.stock.Ticker)
		}
	}
	return h
}

func (h *SectionHistory) isNew(section, ticker string) bool {
	if h == nil || h.Version != historyVersion {
		return false
	}
	previous, ok := h.Sections[section]
	if !ok {
		return false
	}
	for _, existing := range previous {
		if existing == ticker {
			return false
		}
	}
	return true
}

func LoadHistory(path, scope string) (*SectionHistory, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h SectionHistory
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	if h.Version != historyVersion || h.Scope != scope {
		return nil, nil
	}
	for _, section := range []string{sectionBuys, sectionSells, sectionPullbacks, sectionReversals} {
		if _, ok := h.Sections[section]; !ok {
			return nil, fmt.Errorf("digest history is missing section %q", section)
		}
	}
	return &h, nil
}

// Save uses a replacement file so an interrupted write cannot corrupt the baseline.
func (h *SectionHistory) Save(path string) error {
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".digest-history-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
