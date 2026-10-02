// Package forecast estimates when a quota window will run out, based on the
// recent rate of use.
package forecast

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/runs"
)

type sample struct {
	at  time.Time
	pct float64
}

// Sampler keeps quota samples to project when they run out.
type Sampler struct {
	mu      sync.Mutex
	samples map[string][]sample
	dirty   bool
}

// New creates an empty Sampler.
func New() *Sampler {
	return &Sampler{
		samples: make(map[string][]sample),
	}
}

// Key builds the key that identifies a bar: agent + "/" + bar name.
func Key(agent, bar string) string {
	return agent + "/" + bar
}

// Dirty reports whether there was any new sample since the last Save.
func (m *Sampler) Dirty() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dirty
}

// fileName is the samples file, inside runs.Home() (~/.panal or PANAL_DATA).
const fileName = "samples.json"

// Path returns where the samples file lives: PANAL_DATA\samples.json if the
// variable is set; otherwise %USERPROFILE%\.panal\samples.json.
func Path() string {
	return filepath.Join(runs.Home(), fileName)
}

type sampleJSON struct {
	T   string  `json:"t"`
	Pct float64 `json:"pct"`
}

type samplesFile struct {
	Version int                     `json:"version"`
	Samples map[string][]sampleJSON `json:"samples,omitempty"`
}

// Save writes the samples to disk as JSON, atomically.
// It only holds the mutex to copy the data in memory.
func (m *Sampler) Save(path string) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	snapshot := make(map[string][]sampleJSON, len(m.samples))
	for key, ms := range m.samples {
		items := make([]sampleJSON, len(ms))
		for i, s := range ms {
			items[i] = sampleJSON{
				T:   s.at.Format(time.RFC3339),
				Pct: s.pct,
			}
		}
		snapshot[key] = items
	}
	m.mu.Unlock()

	b, err := json.Marshal(samplesFile{
		Version: 1,
		Samples: snapshot,
	})
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	m.mu.Lock()
	m.dirty = false
	m.mu.Unlock()

	return nil
}

// Load reads the samples file and keeps only those from the last 2 h before
// now (and none from the future). If it does not exist, is broken or is of
// another version, it is ignored without error (it starts empty).
func (m *Sampler) Load(path string, now time.Time) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.samples = make(map[string][]sample)
	m.dirty = false

	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var d samplesFile
	if err := json.Unmarshal(b, &d); err != nil {
		return nil
	}
	if d.Version != 1 {
		return nil
	}
	stored := d.Samples

	limit := now.Add(-2 * time.Hour)
	for key, ms := range stored {
		var valid []sample
		for _, raw := range ms {
			t, err := time.Parse(time.RFC3339Nano, raw.T)
			if err != nil {
				t, err = time.Parse(time.RFC3339, raw.T)
				if err != nil {
					continue
				}
			}
			if !t.Before(limit) && !t.After(now) {
				valid = append(valid, sample{at: t, pct: raw.Pct})
			}
		}
		if len(valid) > 0 {
			m.samples[key] = valid
		}
	}

	return nil
}

// Record stores a sample (time, percent used). It keeps the samples of the
// last 2 h. If the percent drops more than 5 points from the previous one,
// the window was reset and the earlier samples are cleared. It does not store
// it if it equals the previous one and less than 1 min has passed.
func (m *Sampler) Record(key string, now time.Time, pct float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.samples == nil {
		m.samples = make(map[string][]sample)
	}

	ms := m.samples[key]
	if len(ms) > 0 {
		prev := ms[len(ms)-1]
		if prev.pct-pct > 5.0 {
			// Window reset: clear the samples for that key.
			ms = nil
		} else if pct == prev.pct && now.Sub(prev.at) < time.Minute && now.Sub(prev.at) >= 0 {
			// Do not fill memory if the value does not change within a minute.
			return
		}
	}

	ms = append(ms, sample{at: now, pct: pct})

	// Keep only the samples of the last 2 hours.
	limit := now.Add(-2 * time.Hour)
	from := 0
	for from < len(ms) && ms[from].at.Before(limit) {
		from++
	}
	if from > 0 {
		ms = ms[from:]
	}

	m.samples[key] = ms
	m.dirty = true
}

// Project estimates when the bar will reach 100 % using a linear regression
// (least squares) over the samples of the last 60 min. It returns ok only if
// there are at least 3 samples spanning 10 min or more and the slope is
// positive. If current >= 100, it returns ok=false. Everything is relative
// to now.
func (m *Sampler) Project(key string, now time.Time, current float64) (runsOut time.Time, ok bool) {
	if m == nil || current >= 100 {
		return time.Time{}, false
	}

	m.mu.Lock()
	ms := m.samples[key]
	limit := now.Add(-60 * time.Minute)
	var usable []sample
	for _, s := range ms {
		if !s.at.Before(limit) && !s.at.After(now) {
			usable = append(usable, s)
		}
	}
	m.mu.Unlock()

	if len(usable) < 3 {
		return time.Time{}, false
	}

	minAt, maxAt := usable[0].at, usable[0].at
	for _, s := range usable[1:] {
		if s.at.Before(minAt) {
			minAt = s.at
		}
		if s.at.After(maxAt) {
			maxAt = s.at
		}
	}
	if maxAt.Sub(minAt) < 10*time.Minute {
		return time.Time{}, false
	}

	// Linear regression: x in seconds since minAt for numerical stability.
	n := float64(len(usable))
	var sumX, sumY float64
	xs := make([]float64, len(usable))
	for i, s := range usable {
		xs[i] = s.at.Sub(minAt).Seconds()
		sumX += xs[i]
		sumY += s.pct
	}
	meanX := sumX / n
	meanY := sumY / n

	var sxx, sxy float64
	for i, s := range usable {
		dx := xs[i] - meanX
		dy := s.pct - meanY
		sxx += dx * dx
		sxy += dx * dy
	}

	if sxx <= 0 {
		return time.Time{}, false
	}

	slope := sxy / sxx
	if slope <= 0 {
		return time.Time{}, false
	}

	x100 := meanX + (100-meanY)/slope
	runsOut = minAt.Add(time.Duration(math.Round(x100 * float64(time.Second))))
	if !runsOut.After(now) {
		return time.Time{}, false
	}

	return runsOut, true
}
