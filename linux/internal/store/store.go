// Package store keeps the daemon's persistent state: the active profile (private key encrypted)
// and the network steps to undo after a crash.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Xtratter/yggtunnel/linux/internal/profile"
)

// Store is a state directory, root-owned and private.
type Store struct{ Dir string }

// Step is one applied network change; Kind and Args are enough to undo it.
type Step struct {
	Kind string            `json:"kind"`
	Args map[string]string `json:"args,omitempty"`
}

// PrevState lists the steps applied so far, in order.
type PrevState struct {
	Steps []Step `json:"steps"`
}

// Open creates the directory (mode 0700) when missing.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) path(name string) string { return filepath.Join(s.Dir, name) }

// SaveProfile stores the profile as the active one; the private key is encrypted.
func (s *Store) SaveProfile(p profile.Profile) error {
	sealed, err := s.seal(p.PrivateKey)
	if err != nil {
		return err
	}
	p.PrivateKey = sealed
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeAtomic(s.path("profile.json"), b)
}

// Profile returns the active profile with the private key decrypted.
func (s *Store) Profile() (profile.Profile, error) {
	var p profile.Profile
	b, err := os.ReadFile(s.path("profile.json"))
	if os.IsNotExist(err) {
		return p, errors.New("no profile imported")
	} else if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return profile.Profile{}, errors.New("profile file is damaged")
	}
	if p.PrivateKey, err = s.open(p.PrivateKey); err != nil {
		return profile.Profile{}, err
	}
	return p, nil
}

// MaskedProfile is the active profile for display: the private key shows only its last 4 characters.
// Empty when there is no profile.
func (s *Store) MaskedProfile() profile.Profile {
	p, err := s.Profile()
	if err != nil {
		return profile.Profile{}
	}
	if r := []rune(p.PrivateKey); len(r) > 4 {
		p.PrivateKey = "…" + string(r[len(r)-4:])
	} else {
		p.PrivateKey = "…"
	}
	return p
}

// SavePrev records the applied steps.
func (s *Store) SavePrev(v PrevState) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeAtomic(s.path("prev.json"), b)
}

// Prev returns the recorded steps; false when nothing is recorded.
func (s *Store) Prev() (PrevState, bool, error) {
	var v PrevState
	b, err := os.ReadFile(s.path("prev.json"))
	if os.IsNotExist(err) {
		return v, false, nil
	} else if err != nil {
		return v, false, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return PrevState{}, false, errors.New("prev.json is damaged")
	}
	return v, true, nil
}

// ClearPrev forgets the recorded steps.
func (s *Store) ClearPrev() error {
	if err := os.Remove(s.path("prev.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
