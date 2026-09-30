package bugreport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// State is the reporter's memory between runs, a few hundred bytes on flash.
type State struct {
	BootID      string               `json:"bootId,omitempty"`
	DmesgAt     float64              `json:"dmesgAt,omitempty"`
	Sent        map[string]time.Time `json:"sent,omitempty"`
	HourStart   time.Time            `json:"hourStart,omitempty"`
	HourCount   int                  `json:"hourCount,omitempty"`
	DayStart    time.Time            `json:"dayStart,omitempty"`
	DayCount    int                  `json:"dayCount,omitempty"`
	XrayMissing int                  `json:"xrayMissing,omitempty"`
	Dropped     int                  `json:"dropped,omitempty"`
	LastError   string               `json:"lastError,omitempty"`
}

// Sends allowed: an hour's and a day's.
const (
	MaxPerHour = 12
	MaxPerDay  = 100
)

// LoadState reads path; a missing or broken file is a fresh state.
func LoadState(path string) State {
	var st State
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

// Save writes st whole or not at all.
func (st State) Save(path string) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Allow counts a send when the hour's and the day's limits leave room.
func (st *State) Allow(now time.Time) bool {
	if now.Sub(st.HourStart) >= time.Hour || now.Before(st.HourStart) {
		st.HourStart, st.HourCount = now, 0
	}
	if now.Sub(st.DayStart) >= 24*time.Hour || now.Before(st.DayStart) {
		st.DayStart, st.DayCount = now, 0
	}
	if st.HourCount >= MaxPerHour || st.DayCount >= MaxPerDay {
		return false
	}
	st.HourCount++
	st.DayCount++
	return true
}
