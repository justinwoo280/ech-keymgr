<!--
Thanks for contributing to ech-keymgr!

If you are submitting a brand-new DNS provider, please use the
provider PR template instead — copy the URL below to your address bar
and replace the existing query-string:

  ?template=provider.md

For everything else (bug fixes, features, docs), keep this template.
-->

## Summary

<!-- One paragraph: what does this PR change, and why does it matter? -->

## Type of change

- [ ] Bug fix (non-breaking)
- [ ] New feature (non-breaking)
- [ ] Breaking change (behaviour or API)
- [ ] Documentation only
- [ ] CI / build / repo tooling
- [ ] Refactor (no behaviour change)

## Testing

<!-- How did you verify this change? Tick all that apply, and add notes. -->

- [ ] `go test -tags=all ./...` passes locally
- [ ] Added new unit tests
- [ ] Manually exercised the affected code path
- [ ] Tested against a real DNS provider (which one? __________ )
- [ ] Tested against a real nginx with ECH enabled

## Risk & rollback

<!-- What could go wrong if this PR is merged? How would an operator roll back? -->

## Documentation

- [ ] Updated `README.md`
- [ ] Updated `docs/architecture.md`
- [ ] Updated provider `README.md`
- [ ] No docs changes needed

## Checklist

- [ ] My code follows the project's existing patterns
- [ ] I have run `gofmt` / `go vet -tags=all ./...`
- [ ] I have added or updated tests where appropriate
- [ ] I have added comments to non-obvious code
- [ ] PR title is in the form `<scope>: <imperative>` (e.g. `rotator: handle clock skew`)

## Related issues

<!-- e.g. Fixes #123, Refs #456 -->
