# Writing a DNS Provider for ech-keymgr

Adding support for a new DNS hosting provider takes about 100 lines of Go.
This guide walks you through it.

## Interface

Implement [`pkg/dns.Provider`](../pkg/dns/provider.go):

```go
type Provider interface {
    Name() string
    UpsertHTTPSRecord(ctx context.Context, req UpsertRequest) error
    GetHTTPSRecord(ctx context.Context, zone, name string) (*HTTPSRecord, error)
    DeleteHTTPSRecord(ctx context.Context, zone, name string) error
}
```

### `UpsertHTTPSRecord` rules

**You MUST preserve every SvcParam on the existing record except `ech=`.**
Implementations should:

1. `GetHTTPSRecord(ctx, zone, name)` to read the current record.
2. If it exists, merge: keep its priority, target, and all SvcParams,
   then set `ech=<req.ECHBase64>`. (`req.ECHBase64` is already
   base64-encoded ECHConfigList bytes.)
3. If it does not exist, return [`dns.ErrRecordNotFound`]. The caller
   will surface this to the operator with the suggestion to run
   `ech-keymgr init`.

Do **not** create a new HTTPS record from `Upsert`. New records are
created only by the explicit `init` subcommand path.

## Registration

Use `init()` in your package to self-register:

```go
package myprovider

import "github.com/justinwoo280/ech-keymgr/pkg/dns"

func init() {
    dns.Register("myprovider", New)
}

func New(cfg map[string]any) (dns.Provider, error) { /* ... */ }
```

Then add a build tag at the top of every file in your package:

```go
//go:build myprovider || all
```

This lets users build slim binaries with `go build -tags cloudflare,route53`.
The default build uses `-tags all` and includes every officially
maintained provider.

## Where to put the code

- **Officially maintained provider** (you commit to long-term maintenance):
  `providers/<name>/`. Will be covered by project CI. PRs require code
  review from a project maintainer.

- **Community provider**: `providers/community/<name>/`. Add a `README.md`
  in your directory listing maintainers and required credentials. CI runs
  `go vet` and unit tests, but does not test against a live API by default.

## Tests

Add unit tests using a mock HTTP server (`httptest.NewServer`). Do not
require live API credentials in CI.

A reference implementation lives in [`providers/cloudflare/`](../providers/cloudflare/).
Copy its layout when starting a new provider.

## Checklist for a PR

- [ ] Implements all four `Provider` methods
- [ ] Preserves non-`ech=` SvcParams in `Upsert`
- [ ] Returns `dns.ErrRecordNotFound` when the record is absent
- [ ] Self-registers in `init()` with a unique provider name
- [ ] Build tag on every file (`<name> || all` for official, `<name> || community` otherwise)
- [ ] Unit tests using `httptest`
- [ ] `README.md` in the package directory
- [ ] Added to `providers/all.go` or `providers/community/all.go`
