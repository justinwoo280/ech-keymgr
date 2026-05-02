package keystore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func tmpStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenOrInit(dir, "hidden.example.com", "example.com")
	if err != nil {
		t.Fatalf("OpenOrInit: %v", err)
	}
	return s
}

func TestOpenOrInit_CreatesMeta(t *testing.T) {
	s := tmpStore(t)
	if s.RecordFQDN() != "hidden.example.com" {
		t.Errorf("record_fqdn = %q", s.RecordFQDN())
	}
	if s.PublicName() != "example.com" {
		t.Errorf("public_name = %q", s.PublicName())
	}
	// Reopening must not overwrite identity fields.
	s2, err := OpenOrInit(s.Dir(), "OTHER", "OTHER")
	if err != nil {
		t.Fatal(err)
	}
	if s2.RecordFQDN() != "hidden.example.com" {
		t.Errorf("identity overwritten on re-open: %q", s2.RecordFQDN())
	}
}

func TestOpen_RejectsMissingMeta(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err == nil {
		t.Errorf("expected error opening empty dir")
	}
}

func TestAdd_DemotesPreviousCurrent(t *testing.T) {
	s := tmpStore(t)
	e1, err := s.Add([]byte("PEM1"), 0xA1)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := s.Add([]byte("PEM2"), 0xB2)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := s.Current()
	if err != nil {
		t.Fatal(err)
	}
	if cur.ConfigID != e2.ConfigID {
		t.Errorf("Current() should be the latest Add, got %02x", cur.ConfigID)
	}
	prev, err := s.Lookup(e1.ConfigID)
	if err != nil {
		t.Fatal(err)
	}
	if prev.State != StatePrevious {
		t.Errorf("first Add should be demoted, state = %q", prev.State)
	}
}

func TestAdd_AtomicWritesAndPerms(t *testing.T) {
	s := tmpStore(t)
	e, err := s.Add([]byte("PEMx"), 0x01)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(s.Dir(), e.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if perm := st.Mode().Perm(); perm != 0o600 {
			t.Errorf("file perm = %v, want 0600", perm)
		}
	}
	got, err := s.Read(e)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PEMx" {
		t.Errorf("read back = %q", got)
	}
}

func TestList_NewestFirst(t *testing.T) {
	s := tmpStore(t)
	for i := uint8(1); i <= 3; i++ {
		if _, err := s.Add([]byte{byte('A' + i - 1)}, i); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // ensure distinct CreatedAt
	}
	lst := s.List()
	if len(lst) != 3 {
		t.Fatalf("len=%d", len(lst))
	}
	if !(lst[0].CreatedAt.After(lst[1].CreatedAt) && lst[1].CreatedAt.After(lst[2].CreatedAt)) {
		t.Errorf("not sorted desc: %v", lst)
	}
}

func TestSetState_Grace(t *testing.T) {
	s := tmpStore(t)
	e, _ := s.Add([]byte("PEM"), 0x42)
	drop := time.Now().Add(2 * time.Hour)
	if err := s.SetState(e.ConfigID, StateGrace, time.Time{}, drop); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Lookup(e.ConfigID)
	if got.State != StateGrace {
		t.Errorf("state = %q", got.State)
	}
	if !got.ScheduledDropAt.Equal(drop.UTC()) {
		t.Errorf("ScheduledDropAt drift")
	}
}

func TestSetState_RejectsInvalid(t *testing.T) {
	s := tmpStore(t)
	e, _ := s.Add([]byte("PEM"), 0x42)
	if err := s.SetState(e.ConfigID, "bogus", time.Time{}, time.Time{}); err == nil {
		t.Errorf("expected invalid state error")
	}
}

func TestSetState_NotFound(t *testing.T) {
	s := tmpStore(t)
	if err := s.SetState(0xFF, StateGrace, time.Time{}, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete_RemovesFileAndEntry(t *testing.T) {
	s := tmpStore(t)
	e, _ := s.Add([]byte("PEM"), 0x42)
	path := filepath.Join(s.Dir(), e.Filename)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(e.ConfigID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file not removed: %v", err)
	}
	if _, err := s.Lookup(e.ConfigID); !errors.Is(err, ErrNotFound) {
		t.Errorf("entry not removed")
	}
}

func TestPruneExpired_OnlyExpiresGrace(t *testing.T) {
	s := tmpStore(t)
	keep, _ := s.Add([]byte("KEEP"), 0x10)         // remains current
	expire, _ := s.Add([]byte("EXPIRE"), 0x20)
	// Move expire into grace with a past drop time.
	if err := s.SetState(expire.ConfigID, StateGrace, time.Time{}, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	pruned, err := s.PruneExpired(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 || pruned[0] != expire.ConfigID {
		t.Errorf("pruned = %v", pruned)
	}
	if _, err := s.Lookup(expire.ConfigID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired key still present")
	}
	// Demoted "keep" still exists (was demoted to Previous after second Add).
	if _, err := s.Lookup(keep.ConfigID); err != nil {
		t.Errorf("keep removed: %v", err)
	}
}

func TestEmptyStore_Current(t *testing.T) {
	s := tmpStore(t)
	if _, err := s.Current(); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty store should report ErrEmpty, got %v", err)
	}
}

func TestUniqueFilename_OnCollision(t *testing.T) {
	s := tmpStore(t)
	// Force a collision by re-using the same time stamp + config_id
	// via a manual second Add immediately after the first.
	e1, _ := s.Add([]byte("X"), 0x55)
	e2, _ := s.Add([]byte("Y"), 0x55) // same configID
	if e1.Filename == e2.Filename {
		t.Errorf("filenames must be unique even with identical configID, got %q twice", e1.Filename)
	}
	if !strings.HasSuffix(e2.Filename, ".ech") {
		t.Errorf("collision-suffixed name lost .ech extension: %q", e2.Filename)
	}
}

func TestRoundTrip_Persistence(t *testing.T) {
	s := tmpStore(t)
	if _, err := s.Add([]byte("A"), 0x01); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add([]byte("B"), 0x02); err != nil {
		t.Fatal(err)
	}
	// Re-open from disk.
	s2, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.List()) != 2 {
		t.Errorf("expected 2 entries after re-open, got %d", len(s2.List()))
	}
	if s2.RecordFQDN() != "hidden.example.com" {
		t.Errorf("metadata not persisted")
	}
}
