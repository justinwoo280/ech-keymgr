// Package community is a side-effect-only umbrella that imports every
// community-maintained DNS provider so their init() functions fire.
//
// Community providers are pulled in by the default `all` build tag,
// just like official providers, but they live under a separate
// directory tree to make their unofficial-maintenance status explicit
// to operators and reviewers.
package community

import (
	// mock is the in-memory reference provider; importing it here
	// triggers its init() so dns.Lookup("mock") works without users
	// needing to import the package directly.
	_ "github.com/justinwoo280/ech-keymgr/providers/community/mock"
)
