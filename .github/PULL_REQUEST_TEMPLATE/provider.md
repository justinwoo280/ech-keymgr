## New DNS provider: `<name>`

**Provider name (registry key):** `__________`

**Vendor / API:** <!-- Cloudflare, Bunny.net, Hetzner DNS, ... -->
**Vendor docs URL:** <!-- link to the API reference -->

**Maintainer:** @<!-- your GitHub handle -->
**Real-world testing:** <!-- did you test against a live account? what happened? -->

---

### Required (gate-blocking)

- [ ] Implements all three methods of `pkg/dns.Provider`
      (`GetHTTPSRDATA`, `PutHTTPSRDATA`, `DeleteHTTPSRDATA`)
- [ ] `init()` calls `dns.Register("<name>", New)` to self-register
- [ ] Whole package gated by build tag
      `<name> || community || all`
      (one tag per file, written as a `//go:build` directive)
- [ ] Lives under `providers/community/<name>/`
- [ ] Imported (with `_`) from `providers/community/all.go`
- [ ] Includes `doc.go` with package-level docstring
- [ ] Includes `README.md` with:
  - [ ] Minimal YAML configuration example
  - [ ] How to obtain credentials (token / key)
  - [ ] Required vendor IAM/permissions (least-privilege)
  - [ ] Known caveats or rate limits

### Tests (gate-blocking)

- [ ] Unit tests exist (`<name>_test.go`)
- [ ] Returns `dns.ErrRecordNotFound` for absent records
- [ ] PUT-then-GET is idempotent
- [ ] DELETE on absent record returns `nil`
- [ ] Authorization header / query parameter is correct
- [ ] All tests pass with `go test -tags=<name> ./providers/community/<name>/...`

### Strongly recommended

- [ ] Tests use `httptest.NewServer` to avoid hitting the real API
- [ ] Errors wrap the vendor's structured error message
      (RFC 7807 `detail`, vendor `error_chain`, etc.)
- [ ] `Get` filters server responses to the exact owner name
      (not a prefix / suffix match)
- [ ] Operations honour `ctx.Done()` / `ctx.Deadline()`

---

### Maintenance commitment

By submitting this PR, you (the maintainer) acknowledge:

- The `ech-keymgr` core team will review for **interface conformance**
  and **safety**, but does **not** guarantee functional correctness
  against the vendor — that is your responsibility.
- The provider lives under `providers/community/` and is built into
  the default release. If it breaks (vendor API change, expired
  test account, etc.) and is not fixed within a reasonable window,
  it may be moved to `providers/community-archive/` or removed.

---

### Manual verification (optional but appreciated)

Paste the output of a real-world `rotate` cycle if you have one:

```text
$ ech-keymgr -c /tmp/<name>-test.yaml rotate <fqdn>
...
```
