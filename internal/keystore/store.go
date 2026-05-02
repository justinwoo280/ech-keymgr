package keystore

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store is the directory-backed key store for a single managed
// domain. Construct with Open or OpenOrInit.
//
// All mutating operations are serialized by an internal mutex so a
// concurrent rotation cycle and a manual `ech-keymgr keygen` can't
// corrupt the metadata.
type Store struct {
	dir  string
	mu   sync.Mutex
	meta Meta
}

// Open loads the metadata file from dir. If dir or .meta.json is
// missing, returns an error — use OpenOrInit when first-run setup
// is acceptable.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("keystore: empty directory")
	}
	s := &Store{dir: dir}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// OpenOrInit is like Open but creates dir (mode 0700) and an empty
// .meta.json (mode 0600) if they don't yet exist. recordFQDN and
// publicName are captured into the new metadata.
//
// If the directory already exists with a populated .meta.json, the
// existing record_fqdn / public_name fields are preserved (we never
// silently overwrite identity-shaped metadata).
func OpenOrInit(dir, recordFQDN, publicName string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("keystore: empty directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("keystore: mkdir %s: %w", dir, err)
	}
	s := &Store{dir: dir}
	if err := s.load(); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		// Initialise a fresh metadata file.
		s.meta = Meta{
			Version:    MetaVersion,
			RecordFQDN: recordFQDN,
			PublicName: publicName,
			UpdatedAt:  time.Now().UTC(),
		}
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Dir returns the absolute (or whatever was passed) directory path.
func (s *Store) Dir() string { return s.dir }

// RecordFQDN returns the managed domain captured in the metadata.
func (s *Store) RecordFQDN() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.RecordFQDN
}

// PublicName returns the public_name captured in the metadata.
func (s *Store) PublicName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.PublicName
}

// List returns a snapshot of all entries, sorted by CreatedAt
// descending (newest first). Safe to mutate; callers cannot affect
// the underlying store through the returned slice.
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.meta.Keys))
	copy(out, s.meta.Keys)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// Current returns the entry whose State == StateCurrent. If none
// exists (fresh store, or rotator killed mid-cycle), returns
// ErrEmpty.
func (s *Store) Current() (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.meta.Keys {
		if e.State == StateCurrent {
			return e, nil
		}
	}
	return Entry{}, ErrEmpty
}

// Lookup returns the entry whose ConfigID == id, or ErrNotFound.
func (s *Store) Lookup(id uint8) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.meta.Keys {
		if e.ConfigID == id {
			return e, nil
		}
	}
	return Entry{}, ErrNotFound
}

// Read returns the raw .ech file contents for the given entry.
func (s *Store) Read(e Entry) ([]byte, error) {
	if e.Filename == "" {
		return nil, errors.New("keystore: empty entry filename")
	}
	return os.ReadFile(filepath.Join(s.dir, e.Filename))
}

// Add atomically writes a new .ech file (mode 0600) and inserts a
// matching metadata entry with State = StateCurrent. The previously-
// current key is demoted to StatePrevious in the same atomic
// metadata update.
//
// pemBytes must be the marshaled output of internal/pemfile.Marshal.
//
// The returned Entry has its Filename populated.
func (s *Store) Add(pemBytes []byte, configID uint8) (Entry, error) {
	if len(pemBytes) == 0 {
		return Entry{}, errors.New("keystore: empty PEM bytes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	name := fmt.Sprintf("%s-%02x.ech", now.Format("20060102T150405Z"), configID)
	if err := s.uniqueName(&name, configID); err != nil {
		return Entry{}, err
	}
	if err := atomicWrite(filepath.Join(s.dir, name), pemBytes, 0o600); err != nil {
		return Entry{}, err
	}

	// Demote the previous Current (if any).
	for i := range s.meta.Keys {
		if s.meta.Keys[i].State == StateCurrent {
			s.meta.Keys[i].State = StatePrevious
		}
	}

	e := Entry{
		Filename:  name,
		ConfigID:  configID,
		State:     StateCurrent,
		CreatedAt: now,
	}
	if err := validateEntry(e); err != nil {
		// Roll back the file write so we don't leave an
		// orphan that the next List would surface.
		_ = os.Remove(filepath.Join(s.dir, name))
		return Entry{}, err
	}
	s.meta.Keys = append(s.meta.Keys, e)
	if err := s.persistLocked(); err != nil {
		_ = os.Remove(filepath.Join(s.dir, name))
		return Entry{}, err
	}
	return e, nil
}

// SetState updates the lifecycle state of the entry whose ConfigID
// matches id. When transitioning into StateGrace, scheduledDrop is
// the wall-clock time at which the entry should be deleted (the
// rotator periodically calls PruneExpired to enforce it).
//
// inDNS is recorded as InDNSSince when the new state is Current or
// Previous; ignored otherwise.
func (s *Store) SetState(id uint8, state State, inDNS, scheduledDrop time.Time) error {
	if !state.IsValid() {
		return fmt.Errorf("keystore: invalid state %q", state)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.meta.Keys {
		if s.meta.Keys[i].ConfigID != id {
			continue
		}
		s.meta.Keys[i].State = state
		if state == StateCurrent || state == StatePrevious {
			if !inDNS.IsZero() {
				s.meta.Keys[i].InDNSSince = inDNS.UTC()
			}
		}
		if state == StateGrace {
			s.meta.Keys[i].ScheduledDropAt = scheduledDrop.UTC()
		}
		return s.persistLocked()
	}
	return ErrNotFound
}

// Delete removes the .ech file and its metadata entry.
//
// Deleting the StateCurrent entry is permitted but should normally
// be avoided by the rotator — there is a brief window during which
// the store has no Current key.
func (s *Store) Delete(id uint8) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.meta.Keys {
		if e.ConfigID != id {
			continue
		}
		path := filepath.Join(s.dir, e.Filename)
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("keystore: remove %s: %w", path, err)
		}
		s.meta.Keys = append(s.meta.Keys[:i], s.meta.Keys[i+1:]...)
		return s.persistLocked()
	}
	return ErrNotFound
}

// PruneExpired deletes every StateGrace entry whose ScheduledDropAt
// is in the past relative to `now`. Returns the IDs that were
// pruned, in the order pruned.
//
// Safe to call from a periodic timer; idempotent if no entries are
// expired (returns an empty slice).
func (s *Store) PruneExpired(now time.Time) ([]uint8, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pruned []uint8
	out := s.meta.Keys[:0]
	for _, e := range s.meta.Keys {
		if e.State == StateGrace && !e.ScheduledDropAt.IsZero() && !e.ScheduledDropAt.After(now) {
			path := filepath.Join(s.dir, e.Filename)
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				// Restore previously-iterated entries on error.
				s.meta.Keys = append(out, s.meta.Keys[len(out)+len(pruned):]...)
				return pruned, fmt.Errorf("keystore: remove %s: %w", path, err)
			}
			pruned = append(pruned, e.ConfigID)
			continue
		}
		out = append(out, e)
	}
	s.meta.Keys = out
	if len(pruned) == 0 {
		return nil, nil
	}
	if err := s.persistLocked(); err != nil {
		return pruned, err
	}
	return pruned, nil
}

// uniqueName ensures `name` does not collide with anything already
// in the store. If it does, suffixes a short random tag. Caller must
// hold s.mu.
func (s *Store) uniqueName(name *string, configID uint8) error {
	for _, e := range s.meta.Keys {
		if e.Filename == *name {
			suffix, err := randomSuffix()
			if err != nil {
				return err
			}
			*name = strings.TrimSuffix(*name, ".ech") + "-" + suffix + ".ech"
			break
		}
	}
	return nil
}

func randomSuffix() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 6)
	for i, x := range b {
		out[2*i] = hex[x>>4]
		out[2*i+1] = hex[x&0x0F]
	}
	return string(out), nil
}

// load reads .meta.json and populates s.meta. fs.ErrNotExist is
// returned if the file isn't there (so the caller can distinguish
// "fresh dir" from "I/O error").
func (s *Store) load() error {
	raw, err := os.ReadFile(filepath.Join(s.dir, metaFileName))
	if err != nil {
		return err
	}
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("keystore: parse meta: %w", err)
	}
	if m.Version != MetaVersion {
		return fmt.Errorf("keystore: unsupported meta version %d (want %d)", m.Version, MetaVersion)
	}
	s.meta = m
	return nil
}

// persistLocked atomically rewrites .meta.json. Caller must hold s.mu.
func (s *Store) persistLocked() error {
	s.meta.Version = MetaVersion
	s.meta.UpdatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(s.meta, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, metaFileName), raw, 0o600)
}
