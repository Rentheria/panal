// Package feedback is the user's own verdict on a delegated run: good or bad,
// with an optional note. It lives in ~/.panal/feedback.json (PANAL_DATA moves
// it), which is panal's own data: `panal feedback` and the dashboard's
// History write it, and the router (internal/router) and history read it.
//
// A rating is what turns "done" into "done and right": the router counts a
// run rated bad as a failure even if the agent finished its turn.
package feedback

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AlbertoVasquezR/panal/internal/runs"
)

// Ratings.
const (
	Good = "good"
	Bad  = "bad"
)

// FileName is the file's name inside runs.Home() (~/.panal).
const FileName = "feedback.json"

// Path of the feedback file: ~/.panal/feedback.json (PANAL_DATA moves it).
func Path() string { return filepath.Join(runs.Home(), FileName) }

// Entry is the rating of one run.
type Entry struct {
	Rating string    `json:"rating"` // good or bad
	Note   string    `json:"note,omitempty"`
	At     time.Time `json:"at"`
}

// file is the JSON on disk.
type file struct {
	Version int              `json:"version"`
	Ratings map[string]Entry `json:"ratings"` // by Key
}

// Key of a run: its ID and agent, like its run file's name (<ID>-<agent>).
func Key(id, agent string) string { return id + "-" + agent }

// Store is the ratings, by Key.
type Store map[string]Entry

// Get returns the rating of a run ("" if it is not rated).
func (s Store) Get(id, agent string) Entry { return s[Key(id, agent)] }

// Load reads the file. A missing file is an empty store, not an error.
func Load(path string) (Store, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Store{}, nil
	}
	if err != nil {
		return Store{}, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return Store{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if f.Ratings == nil {
		return Store{}, nil
	}
	return Store(f.Ratings), nil
}

// cached keeps the last Load by path and modification time: history reads
// ratings on every dashboard refresh.
var (
	cacheMu sync.Mutex
	cache   = map[string]cachedStore{}
)

type cachedStore struct {
	mod   time.Time
	size  int64
	store Store
}

// Cached is Load, re-reading the file only when it changed. Errors give an
// empty store.
func Cached(path string) Store {
	st, err := os.Stat(path)
	if err != nil {
		return Store{}
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if c, ok := cache[path]; ok && c.mod.Equal(st.ModTime()) && c.size == st.Size() {
		return c.store
	}
	s, _ := Load(path)
	cache[path] = cachedStore{st.ModTime(), st.Size(), s}
	return s
}

// Set rates the run with key (Key(id, agent)) and writes the file
// atomically. rating "" removes the rating.
func Set(path, key, rating, note string, now time.Time) error {
	switch rating {
	case Good, Bad, "":
	default:
		return fmt.Errorf("rating must be %s or %s, not %q", Good, Bad, rating)
	}
	s, err := Load(path)
	if err != nil {
		return err
	}
	if rating == "" {
		delete(s, key)
	} else {
		s[key] = Entry{Rating: rating, Note: strings.TrimSpace(note), At: now.UTC().Truncate(time.Second)}
	}
	data, err := json.MarshalIndent(file{Version: 1, Ratings: s}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
