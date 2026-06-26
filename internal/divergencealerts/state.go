package divergencealerts

import (
	"encoding/json"
	"os"
	"time"
)

type State struct {
	Date string          `json:"date"`
	Sent map[string]bool `json:"sent"`
}

func LoadState(path string) (*State, error) {
	today := time.Now().Format("2006-01-02")
	state := &State{Date: today, Sent: make(map[string]bool)}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}
	if state.Date != today {
		state.Date = today
		state.Sent = make(map[string]bool)
	}
	if state.Sent == nil {
		state.Sent = make(map[string]bool)
	}
	return state, nil
}

func (s *State) Has(key string) bool {
	return s.Sent[key]
}

func (s *State) Mark(key string) {
	if s.Sent == nil {
		s.Sent = make(map[string]bool)
	}
	s.Sent[key] = true
}

func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
