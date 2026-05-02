package keystore

import (
	"errors"
	"fmt"
	"time"
)

// State describes the lifecycle phase of a stored key.
//
// Transitions (driven by the rotator state machine):
//
//	[generated] → Current → Previous → Grace → [deleted]
//
// "generated" and "deleted" are not states in the store: they only
// exist on disk while a key is one of Current/Previous/Grace.
type State string

const (
	// StateCurrent: the most recently rotated-in key. Listed first
	// in the DNS ECHConfigList. Always served preferentially by
	// participating clients.
	StateCurrent State = "current"

	// StatePrevious: still in the DNS ECHConfigList alongside
	// Current during the convergence overlap.
	StatePrevious State = "previous"

	// StateGrace: removed from DNS, but kept on disk so that
	// clients still using a stale DNS-cached ECHConfigList can
	// complete a handshake. Pruned once ScheduledDropAt is reached.
	StateGrace State = "grace"
)

// IsValid reports whether s is a recognised lifecycle state.
func (s State) IsValid() bool {
	switch s {
	case StateCurrent, StatePrevious, StateGrace:
		return true
	}
	return false
}

// Entry is one tracked key, as recorded in .meta.json.
//
// Filename is relative to the store directory; absolute paths never
// appear in the metadata so the directory can be safely moved.
type Entry struct {
	Filename        string    `json:"filename"`
	ConfigID        uint8     `json:"config_id"`
	State           State     `json:"state"`
	CreatedAt       time.Time `json:"created_at"`
	InDNSSince      time.Time `json:"in_dns_since,omitempty"`
	ScheduledDropAt time.Time `json:"scheduled_drop_at,omitempty"`
}

// MetaVersion is the on-disk schema version of .meta.json. Bumped if
// we ever break the JSON shape; the loader rejects unknown versions
// loudly so an operator notices a downgrade.
const MetaVersion = 1

// metaFileName is the constant name of the index file.
const metaFileName = ".meta.json"

// Meta is the on-disk shape of .meta.json (entire contents).
type Meta struct {
	Version    int       `json:"version"`
	RecordFQDN string    `json:"record_fqdn"`
	PublicName string    `json:"public_name"`
	UpdatedAt  time.Time `json:"updated_at"`
	Keys       []Entry   `json:"keys"`
}

// ErrEmpty is returned by Current() when the store has no keys yet
// (e.g. before the first rotation). Callers in the rotator typically
// treat this as "trigger a fresh rotation".
var ErrEmpty = errors.New("keystore: no keys present")

// ErrNotFound is returned by Lookup operations when no key matches.
var ErrNotFound = errors.New("keystore: entry not found")

// validateEntry enforces invariants we want before mutating .meta.json.
func validateEntry(e Entry) error {
	if e.Filename == "" {
		return errors.New("keystore: entry missing filename")
	}
	if !e.State.IsValid() {
		return fmt.Errorf("keystore: invalid state %q", e.State)
	}
	if e.CreatedAt.IsZero() {
		return errors.New("keystore: entry missing created_at")
	}
	return nil
}
