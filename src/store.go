package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

type NotifyMode string

const (
	NotifyChannel NotifyMode = "channel"
	NotifyDM      NotifyMode = "dm"
	NotifyBoth    NotifyMode = "both"
)

type Watch struct {
	UserID    string     `json:"userId"`
	ChannelID string     `json:"channelId"` // channel where the watch was created (ping target)
	Item      string     `json:"item"`      // as typed
	Exact     bool       `json:"exact"`
	Notify    NotifyMode `json:"notify"`
	MaxPrice  float64    `json:"maxPrice"` // 0 = no cap
	Private   bool       `json:"private"`  // hidden from /watch all; alerts go by DM
	Paused    bool       `json:"paused"`   // set after an alert fires; re-arm via button or /watch resume
	CreatedAt time.Time  `json:"createdAt"`
}

func (w *Watch) Matches(itemName string) bool {
	in := strings.ToLower(strings.TrimSpace(itemName))
	want := strings.ToLower(strings.TrimSpace(w.Item))
	if w.Exact {
		return in == want
	}
	return strings.Contains(in, want)
}

// BonusWatch pings a user when a specific zone has a bonus they care about.
type BonusWatch struct {
	UserID    string     `json:"userId"`
	ChannelID string     `json:"channelId"` // where the watch was created
	Zone      string     `json:"zone"`
	Labels    []string   `json:"labels"` // bonus labels to match; ["Any"] = any confirmed bonus
	Notify    NotifyMode `json:"notify"`
	Private   bool       `json:"private"` // ephemeral confirmations, DM-only pings
	CreatedAt time.Time  `json:"createdAt"`
}

// Store persists watches and runtime state as separate JSON files;
// the struct itself is never marshaled.
type Store struct {
	mu             sync.Mutex
	path           string
	bonusPath      string
	statePath      string
	Watches        []Watch
	BonusWatches   []BonusWatch
	LastSeenID     int64
	BoardMessageID string // bonus-board message, "" until first post
	BonusNotified  map[string]string
}

type persistedState struct {
	LastSeenID     int64             `json:"lastSeenId"`
	BoardMessageID string            `json:"bonusBoardMessageId,omitempty"`
	BonusNotified  map[string]string `json:"bonusNotified,omitempty"` // user|zone|label -> bonusDate
}

func NewStore(watchPath, bonusPath, statePath string) *Store {
	s := &Store{path: watchPath, bonusPath: bonusPath, statePath: statePath, BonusNotified: map[string]string{}}
	s.load()
	return s
}

func (s *Store) load() {
	if b, err := os.ReadFile(s.path); err == nil {
		var w []Watch
		if err := json.Unmarshal(b, &w); err == nil {
			s.Watches = w
		} else {
			log.Printf("WARN: %s is corrupt, starting with no item watches: %v", s.path, err)
		}
	}
	if b, err := os.ReadFile(s.bonusPath); err == nil {
		var w []BonusWatch
		if err := json.Unmarshal(b, &w); err == nil {
			s.BonusWatches = w
		} else {
			log.Printf("WARN: %s is corrupt, starting with no bonus watches: %v", s.bonusPath, err)
		}
	}
	if b, err := os.ReadFile(s.statePath); err == nil {
		var st persistedState
		if err := json.Unmarshal(b, &st); err == nil {
			s.LastSeenID = st.LastSeenID
			s.BoardMessageID = st.BoardMessageID
			if st.BonusNotified != nil {
				s.BonusNotified = st.BonusNotified
			}
		} else {
			log.Printf("WARN: %s is corrupt, poll cursor and board id reset: %v", s.statePath, err)
		}
	}
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) saveWatchesLocked() {
	b, _ := json.MarshalIndent(s.Watches, "", "  ")
	if err := atomicWrite(s.path, b); err != nil {
		log.Printf("WARN: could not save %s: %v", s.path, err)
	}
}

func (s *Store) saveStateLocked() {
	b, _ := json.Marshal(persistedState{LastSeenID: s.LastSeenID, BoardMessageID: s.BoardMessageID, BonusNotified: s.BonusNotified})
	if err := atomicWrite(s.statePath, b); err != nil {
		log.Printf("WARN: could not save %s: %v", s.statePath, err)
	}
}

func (s *Store) saveBonusWatchesLocked() {
	b, _ := json.MarshalIndent(s.BonusWatches, "", "  ")
	if err := atomicWrite(s.bonusPath, b); err != nil {
		log.Printf("WARN: could not save %s: %v", s.bonusPath, err)
	}
}

// AddBonusWatch replaces an existing watch on the same (user, zone) pair.
func (s *Store) AddBonusWatch(w BonusWatch) (replaced bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(w.Zone))
	for i := range s.BonusWatches {
		if s.BonusWatches[i].UserID == w.UserID && strings.ToLower(strings.TrimSpace(s.BonusWatches[i].Zone)) == key {
			s.BonusWatches[i] = w
			s.saveBonusWatchesLocked()
			return true
		}
	}
	s.BonusWatches = append(s.BonusWatches, w)
	s.saveBonusWatchesLocked()
	return false
}

func (s *Store) RemoveBonusWatch(userID, zone string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(zone))
	for i := range s.BonusWatches {
		if s.BonusWatches[i].UserID == userID && strings.ToLower(strings.TrimSpace(s.BonusWatches[i].Zone)) == key {
			s.BonusWatches = append(s.BonusWatches[:i], s.BonusWatches[i+1:]...)
			s.saveBonusWatchesLocked()
			return true
		}
	}
	return false
}

func (s *Store) RemoveAllBonusWatches(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []BonusWatch
	removed := 0
	for _, w := range s.BonusWatches {
		if w.UserID == userID {
			removed++
			continue
		}
		kept = append(kept, w)
	}
	if removed > 0 {
		s.BonusWatches = kept
		s.saveBonusWatchesLocked()
	}
	return removed
}

func (s *Store) UserBonusWatches(userID string) []BonusWatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []BonusWatch
	for _, w := range s.BonusWatches {
		if w.UserID == userID {
			out = append(out, w)
		}
	}
	return out
}

func (s *Store) AllBonusWatches() []BonusWatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BonusWatch, len(s.BonusWatches))
	copy(out, s.BonusWatches)
	return out
}

// WasBonusNotified / MarkBonusNotified dedupe zone-bonus pings per bonus day.
func (s *Store) WasBonusNotified(key, date string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.BonusNotified[key] == date
}

func (s *Store) MarkBonusNotified(key, date string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// prune stale days so the map never grows past the active watch set
	for k, v := range s.BonusNotified {
		if v != date {
			delete(s.BonusNotified, k)
		}
	}
	s.BonusNotified[key] = date
	s.saveStateLocked()
}

func (s *Store) SaveLastSeen(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id > s.LastSeenID {
		s.LastSeenID = id
		s.saveStateLocked()
	}
}

func (s *Store) SaveBoardMessageID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.BoardMessageID = id
	s.saveStateLocked()
}

func (s *Store) GetBoardMessageID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.BoardMessageID
}

func (s *Store) GetLastSeen() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastSeenID
}

// AddWatch replaces an existing watch on the same (user, item) pair.
func (s *Store) AddWatch(w Watch) (replaced bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(w.Item))
	for i := range s.Watches {
		if s.Watches[i].UserID == w.UserID && strings.ToLower(strings.TrimSpace(s.Watches[i].Item)) == key {
			s.Watches[i] = w
			s.saveWatchesLocked()
			return true
		}
	}
	s.Watches = append(s.Watches, w)
	s.saveWatchesLocked()
	return false
}

func (s *Store) RemoveWatch(userID, item string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(item))
	for i := range s.Watches {
		if s.Watches[i].UserID == userID && strings.ToLower(strings.TrimSpace(s.Watches[i].Item)) == key {
			s.Watches = append(s.Watches[:i], s.Watches[i+1:]...)
			s.saveWatchesLocked()
			return true
		}
	}
	return false
}

// SetPaused pauses or re-arms a user's watch; returns false if no such watch.
func (s *Store) SetPaused(userID, item string, paused bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(item))
	for i := range s.Watches {
		if s.Watches[i].UserID == userID && strings.ToLower(strings.TrimSpace(s.Watches[i].Item)) == key {
			s.Watches[i].Paused = paused
			s.saveWatchesLocked()
			return true
		}
	}
	return false
}

// RemoveAllWatches deletes every watch belonging to userID, returning the count.
func (s *Store) RemoveAllWatches(userID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []Watch
	removed := 0
	for _, w := range s.Watches {
		if w.UserID == userID {
			removed++
			continue
		}
		kept = append(kept, w)
	}
	if removed > 0 {
		s.Watches = kept
		s.saveWatchesLocked()
	}
	return removed
}

func (s *Store) UserWatches(userID string) []Watch {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Watch
	for _, w := range s.Watches {
		if w.UserID == userID {
			out = append(out, w)
		}
	}
	return out
}

func (s *Store) AllWatches() []Watch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Watch, len(s.Watches))
	copy(out, s.Watches)
	return out
}
