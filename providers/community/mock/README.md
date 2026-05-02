# mock — in-memory ech-keymgr DNS provider

This is a fully in-memory implementation of `pkg/dns.Provider`. It has
two jobs:

1. **Template for community PRs.** Copy this directory, replace the
   in-memory map with real API calls, change the build tag and the
   provider name, and you have a working provider. The whole file is
   under 100 lines.

2. **Test target for ech-keymgr's own integration tests.** The
   rotator's state machine can be exercised against a `mock` provider
   without any network or external service.

Not suitable for any real deployment — every restart loses all
records.

## Configuration

```yaml
credentials:
  test:
    provider: mock
    # no fields
```

## Build tags

```bash
go build -tags=mock ./cmd/ech-keymgr        # only mock
go build -tags=community ./cmd/ech-keymgr   # all community providers
go build ./cmd/ech-keymgr                   # default `all`, includes mock
```

## Maintainers

Community-maintained as a reference / test target for the project.
