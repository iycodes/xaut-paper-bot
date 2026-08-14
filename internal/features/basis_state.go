package features

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"xaut-paper-bot/internal/series"
)

const (
	basisStateVersion = 1
	BasisSourceLive   = "live_book"
	BasisSourceREST   = "rest_1m_candle"
)

type BasisSample struct {
	Time   time.Time `json:"time"`
	Value  float64   `json:"value"`
	Source string    `json:"source"`
}

type persistedBasisState struct {
	Version int           `json:"version"`
	SavedAt time.Time     `json:"saved_at"`
	Samples []BasisSample `json:"samples"`
}

// SeedBasis replaces the rolling basis window with valid, recent samples.
// Duplicate minute buckets are resolved in favor of the last supplied sample.
func (e *Engine) SeedBasis(samples []BasisSample, now time.Time) int {
	minute := now.UTC().Truncate(time.Minute)
	oldest := minute.Add(-time.Duration(e.cfg.BasisWindow) * time.Minute)
	byMinute := make(map[time.Time]BasisSample, len(samples))
	for _, sample := range samples {
		at := sample.Time.UTC().Truncate(time.Minute)
		if at.IsZero() || at.Before(oldest) || at.After(minute) || math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) {
			continue
		}
		sample.Time = at
		if sample.Source == "" {
			sample.Source = BasisSourceLive
		}
		byMinute[at] = sample
	}
	clean := make([]BasisSample, 0, len(byMinute))
	for _, sample := range byMinute {
		clean = append(clean, sample)
	}
	sort.Slice(clean, func(i, j int) bool { return clean[i].Time.Before(clean[j].Time) })
	if len(clean) > e.cfg.BasisWindow {
		clean = clean[len(clean)-e.cfg.BasisWindow:]
	}

	e.basis = series.New(e.cfg.BasisWindow)
	e.basisTimes = e.basisTimes[:0]
	e.basisSource = e.basisSource[:0]
	e.basisBucket = time.Time{}
	for _, sample := range clean {
		e.basis.Add(sample.Value)
		e.basisTimes = append(e.basisTimes, sample.Time)
		e.basisSource = append(e.basisSource, sample.Source)
		e.basisBucket = sample.Time
	}
	return e.basis.Len()
}

func (e *Engine) BasisSamples() []BasisSample {
	values := e.basis.Values()
	n := len(values)
	if len(e.basisTimes) < n {
		n = len(e.basisTimes)
	}
	out := make([]BasisSample, 0, n)
	for i := 0; i < n; i++ {
		source := BasisSourceLive
		if i < len(e.basisSource) && e.basisSource[i] != "" {
			source = e.basisSource[i]
		}
		out = append(out, BasisSample{Time: e.basisTimes[i], Value: values[i], Source: source})
	}
	return out
}

func (e *Engine) LatestBasisTime() time.Time {
	if len(e.basisTimes) == 0 {
		return time.Time{}
	}
	return e.basisTimes[len(e.basisTimes)-1]
}

func LoadBasisState(path string) ([]BasisSample, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state persistedBasisState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode basis state: %w", err)
	}
	if state.Version != basisStateVersion {
		return nil, fmt.Errorf("unsupported basis state version %d", state.Version)
	}
	return state.Samples, nil
}

func SaveBasisState(path string, samples []BasisSample) error {
	state := persistedBasisState{Version: basisStateVersion, SavedAt: time.Now().UTC(), Samples: samples}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appendCappedTime(values []time.Time, value time.Time, capacity int) []time.Time {
	values = append(values, value)
	if len(values) > capacity {
		copy(values, values[len(values)-capacity:])
		values = values[:capacity]
	}
	return values
}

func appendCappedString(values []string, value string, capacity int) []string {
	values = append(values, value)
	if len(values) > capacity {
		copy(values, values[len(values)-capacity:])
		values = values[:capacity]
	}
	return values
}
