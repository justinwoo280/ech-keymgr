# Security policy

## Supported versions

ech-keymgr is pre-1.0 software. Only the latest tagged release
receives security fixes. We strongly encourage operators to track
the latest release.

| Version    | Status                  |
| ---------- | ----------------------- |
| latest tag | ✅ supported             |
| `main`     | ✅ supported (unstable) |
| anything older | ❌ unsupported       |

## Reporting a vulnerability

**Please do not open a public GitHub issue for security bugs.**

Use [GitHub's private advisory form][advisory] to report a
vulnerability. The advisory form lets us collaborate on a fix in
private and coordinate disclosure.

[advisory]: https://github.com/justinwoo280/ech-keymgr/security/advisories/new

If GitHub is unreachable for you, email the project owner directly
(GitHub handle: `@justinwoo280`).

### What to include

- A clear description of the issue and its impact
- Affected version(s), commit hash, build flags
- Reproduction steps or proof-of-concept
- Your assessment of severity (CVSS optional but appreciated)

### What to expect

- We aim to acknowledge reports within **3 business days**.
- We will keep you informed of progress and ask clarifying questions
  in the same advisory thread.
- Once a fix is ready, we will coordinate a release and a public
  disclosure date with you.
- We will credit you in the advisory and release notes unless you
  prefer to remain anonymous.

## Threat model

ech-keymgr generates ECH HPKE private keys, writes them to disk, and
publishes the corresponding `ech=` parameter to a DNS HTTPS RR.
Particular concerns we treat as **in scope** for security reports:

- Key material leaking (insecure file modes, unintended copies, logs)
- Atomic-write breakage that produces half-written `.ech` files
- DNS provider auth credentials leaking in logs or error messages
- Build-tag-gated provider code being unintentionally compiled in
- A malicious `config.yaml` causing path traversal, command injection,
  or arbitrary writes outside the configured `keydir`
- Network-layer issues in our DNS clients (TLS, certificate handling)

**Out of scope** (these are not security bugs against ech-keymgr):

- Vulnerabilities in nginx, OpenSSL, or other downstream consumers
  of the `.ech` files
- Vulnerabilities in upstream Go dependencies — please report those
  to the upstream project. (We pick them up via `dependabot` and
  `govulncheck` runs.)
- Misconfigured `public_name` or missing X.509 certificate for it —
  this is explicitly out of ech-keymgr's responsibility boundary
  (see `docs/architecture.md`).
- Anything requiring local root access on a host already running
  ech-keymgr (root can read the keys directly).

## Hardening recommendations for operators

- Run ech-keymgr as a dedicated non-root user.
- Use a `keydir` mode of `0700` owned by that user.
- Use the included `examples/systemd/ech-keymgr.service` which
  applies `NoNewPrivileges`, `ProtectSystem=strict`, and a tight
  syscall filter.
- Rotate DNS provider credentials regularly.
- Enable DNSSEC on your zone if your registrar supports it.
