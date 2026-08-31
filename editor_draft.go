package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Draft is a saved bill.
//
// Storage is a single JSON file for now. Accounts and per-user document
// browsing are a later revision (PLAN.md section 10); until then a draft is
// reachable by its unguessable ID and editable only with its secret.
type Draft struct {
	ID      string          `json:"id"`
	Title   string          `json:"title"`
	Doc     json.RawMessage `json:"doc"`
	Created time.Time       `json:"created"`
	Updated time.Time       `json:"updated"`

	// Never leaves the server in a read response.
	Secret string `json:"secret"`
}

// Public is the view of a draft safe to serve to anyone holding the ID.
func (d *Draft) Public() *Draft {
	c := *d
	c.Secret = ""
	return &c
}

type DraftStore struct {
	path   string
	mu     sync.RWMutex
	drafts map[string]*Draft
}

func NewDraftStore(path string) (*DraftStore, error) {
	s := &DraftStore{path: path, drafts: make(map[string]*Draft)}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &s.drafts); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func randomID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *DraftStore) Get(id string) (*Draft, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.drafts[id]
	return d, ok
}

// Save creates a draft when id is empty, and otherwise updates the existing
// draft if the secret matches.
func (s *DraftStore) Save(id, secret, title string, doc json.RawMessage) (*Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	d, ok := s.drafts[id]
	if id == "" || !ok {
		newID, err := randomID(8)
		if err != nil {
			return nil, err
		}
		newSecret, err := randomID(16)
		if err != nil {
			return nil, err
		}
		d = &Draft{ID: newID, Secret: newSecret, Created: now}
		s.drafts[d.ID] = d
	} else if subtle.ConstantTimeCompare([]byte(secret), []byte(d.Secret)) != 1 {
		return nil, errDraftForbidden
	}

	d.Title = title
	d.Doc = doc
	d.Updated = now

	if err := s.flushLocked(); err != nil {
		return nil, err
	}
	return d, nil
}

var errDraftForbidden = fmt.Errorf("draft: wrong edit token")

// flushLocked rewrites the whole store. Writing to a temporary file and
// renaming keeps the file readable at all times.
func (s *DraftStore) flushLocked() error {
	body, err := json.MarshalIndent(s.drafts, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".drafts-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
