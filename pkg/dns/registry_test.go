package dns

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// stubProvider is the smallest possible dns.Provider implementation,
// used to exercise the registry without dragging in a real driver.
type stubProvider struct{ name string }

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) GetHTTPSRDATA(_ context.Context, _, _ string) ([]string, error) {
	return nil, ErrRecordNotFound
}
func (s *stubProvider) PutHTTPSRDATA(_ context.Context, _, _ string, _ uint32, _ []string) error {
	return nil
}
func (s *stubProvider) DeleteHTTPSRDATA(_ context.Context, _, _ string) error { return nil }

// Register/Lookup/Build/Registered share a process-wide map. The
// tests below carefully use unique names to avoid stomping on each
// other and on init()-registered real providers.
func TestRegistry_RegisterAndLookup(t *testing.T) {
	const name = "registry_test_alpha"
	Register(name, func(map[string]any) (Provider, error) {
		return &stubProvider{name: name}, nil
	})

	got, ok := Lookup(name)
	if !ok || got == nil {
		t.Fatalf("Lookup(%q) = (%v,%v); want (factory,true)", name, got, ok)
	}

	prov, err := got(nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if prov.Name() != name {
		t.Errorf("provider name = %q, want %q", prov.Name(), name)
	}
}

func TestRegistry_LookupAbsentReturnsFalse(t *testing.T) {
	if got, ok := Lookup("definitely_not_registered_xyz"); ok || got != nil {
		t.Errorf("Lookup of absent provider = (%v,%v); want (nil,false)", got, ok)
	}
}

func TestRegistry_BuildUnknownReturnsError(t *testing.T) {
	_, err := Build("definitely_not_registered_xyz", nil)
	if err == nil {
		t.Fatal("Build with unknown provider: want error, got nil")
	}
	if !strings.Contains(err.Error(), "definitely_not_registered_xyz") {
		t.Errorf("error should mention the missing name, got %v", err)
	}
}

func TestRegistry_BuildSucceedsAfterRegister(t *testing.T) {
	const name = "registry_test_build"
	Register(name, func(map[string]any) (Provider, error) {
		return &stubProvider{name: name}, nil
	})
	got, err := Build(name, nil)
	if err != nil {
		t.Fatalf("Build(%q): %v", name, err)
	}
	if got.Name() != name {
		t.Errorf("provider.Name() = %q, want %q", got.Name(), name)
	}
}

func TestRegistry_RegisterDuplicatePanics(t *testing.T) {
	const name = "registry_test_dup"
	Register(name, func(map[string]any) (Provider, error) { return nil, nil })

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register of duplicate name: want panic, got none")
		}
	}()
	Register(name, func(map[string]any) (Provider, error) { return nil, nil })
}

func TestRegistry_RegisteredIncludesAddedNames(t *testing.T) {
	const name = "registry_test_listed"
	Register(name, func(map[string]any) (Provider, error) { return nil, nil })

	all := Registered()
	found := false
	for _, n := range all {
		if n == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Registered() = %v; want it to include %q", all, name)
	}
}

// TestRegistry_ConcurrentRegister confirms there is no obvious race;
// run with `go test -race`.
func TestRegistry_ConcurrentRegister(t *testing.T) {
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "registry_test_concurrent_" + string(rune('a'+i))
			Register(name, func(map[string]any) (Provider, error) { return nil, nil })
			_, _ = Lookup(name)
		}(i)
	}
	wg.Wait()
}

// TestErrRecordNotFound proves the sentinel is comparable with errors.Is
// so callers can use it for control flow.
func TestErrRecordNotFound(t *testing.T) {
	if !errors.Is(ErrRecordNotFound, ErrRecordNotFound) {
		t.Error("errors.Is should be reflexive")
	}
}
