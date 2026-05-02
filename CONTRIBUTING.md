# Contributing to ech-keymgr

Thank you for your interest! This project is small but tries to be
**operationally serious** — keys for live web traffic depend on it
not breaking. The bar for changes reflects that.

## TL;DR

1. Open an issue first for non-trivial changes
2. Fork → branch → PR
3. `go test -tags=all ./...` must pass
4. `gofmt -s` clean, `go vet -tags=all ./...` clean,
   `golangci-lint run --build-tags=all ./...` clean
5. Add tests for any new behaviour
6. Use the appropriate PR template (the provider one for new DNS providers)

## Development setup

You need:

- **Go 1.25 or newer** (CI pins 1.25.x to pick up the latest stdlib security patches)
- That's it. **No CGO, no system libraries, no Docker required.**

```bash
git clone https://github.com/justinwoo280/ech-keymgr.git
cd ech-keymgr

# Build everything (all official providers)
go build -tags=all ./cmd/ech-keymgr

# Run all tests
go test -tags=all -race ./...

# Run a single package's tests
go test -v -tags=all ./internal/rotator/...
```

### Optional tools

```bash
# Linter (matches CI)
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
golangci-lint run --build-tags=all ./...

# Vulnerability scan
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck -tags=all ./...
```

## Project boundaries (please read before proposing features)

ech-keymgr deliberately does **one thing**: manage the lifecycle of
ECH HPKE keys and publish their public form to DNS. See
[`docs/architecture.md`](docs/architecture.md) for the full
in-scope / out-of-scope list.

Examples of things that are **out of scope** and will be politely
declined:

- ACME / Let's Encrypt integration (use certbot/lego/acme.sh)
- nginx config generation
- Validating that `public_name` resolves or has a certificate
- Generic SVCB / HTTPS RR management for non-ECH parameters
- Split-mode CFS routing logic

Examples of things that **are very welcome**:

- New DNS providers (community-maintained — see below)
- Hardening of existing code paths (atomic writes, fsync, permission
  modes, error handling)
- Better observability (structured logging, metrics, status output)
- Documentation improvements
- Test-vector-driven correctness improvements to `internal/echconfig`

## Commit messages

We don't enforce conventional commits, but the project's git history
follows a `<scope>: <imperative>` pattern that's easy to scan:

```
rotator: avoid clock skew when computing grace deadline
providers/cloudflare: surface the API error chain in PUT failures
docs: add quickstart for AWS Route 53
ci: pin golangci-lint version
```

For PRs that touch many areas, a single descriptive title is fine —
just make the body of the commit explain *why*.

## Adding a new DNS provider (most common contribution)

This is the most welcomed PR type. We follow [acme.sh][acme]'s model:

- **Officially maintained**: Cloudflare, Route 53, Akamai, PowerDNS.
  Lives under `providers/<name>/`. Maintained by the core team.
- **Community-maintained**: anything else. Lives under
  `providers/community/<name>/`. **Maintained by you.**

[acme]: https://github.com/acmesh-official/acme.sh

Steps:

1. Read the contract: [`pkg/dns/provider.go`](pkg/dns/provider.go).
   It is **deliberately tiny** — three methods, RFC 9460 strings only.
2. Read the reference implementations under `providers/cloudflare/`
   (single-record API style) and `providers/powerdns/` (atomic-RRset
   API style). Pick whichever matches your vendor's shape.
3. Use `providers/community/mock/` as a copy-paste template.
4. Use the [provider PR template][prov-template] when you open the PR.

[prov-template]: .github/PULL_REQUEST_TEMPLATE/provider.md

### Provider naming

- Lowercase, no separator: `cloudflare`, `route53`, `powerdns`,
  `bunny`, `desec`. Matches the build tag.
- The build tag for each provider package must be
  `//go:build <name> || community || all`
  (or `<name> || all` for officially maintained ones).
- Self-register in `init()` with `dns.Register("<name>", New)`.

## Testing guidance

| What you changed                  | Tests you should add                 |
|-----------------------------------|--------------------------------------|
| `internal/echconfig` parser       | A binary test vector + roundtrip     |
| `internal/hpke`                   | Generate / validate a real key pair  |
| `internal/keystore`               | Atomic write + concurrent access     |
| `internal/rotator` state machine  | A scenario test with mock provider   |
| A DNS provider                    | `httptest`-mocked vendor API         |
| CLI subcommand                    | A smoke test under `cmd/ech-keymgr/` |

We do **not** require 100% coverage — we require that the *risky
paths* are tested. PRs that lower test coverage on risky paths will
be asked to add tests before merge.

## Code review

- One reviewer is enough for non-trivial PRs (you'll be auto-assigned
  via [CODEOWNERS](.github/CODEOWNERS))
- Drive-by reviews from anyone are welcome and appreciated
- The review bar is "is this safe to deploy on someone's production
  TLS terminator?" Not "is this elegant?"

## Licensing

By contributing, you agree that your contributions will be licensed
under the [Apache License 2.0](LICENSE), the same as the rest of the
project. We don't require a CLA — Apache 2.0's contribution clause is
sufficient.

## Code of conduct

Be kind, be patient, focus on the work. We don't have a separate
code-of-conduct document yet; if a situation arises that needs one,
we'll borrow the [Contributor Covenant][cc] and add it.

[cc]: https://www.contributor-covenant.org/

---

If you're stuck or unsure, **open a draft PR or a Discussion** rather
than waiting on a "perfect" submission. We'd rather help you across
the finish line than have you abandon a half-done good idea.
