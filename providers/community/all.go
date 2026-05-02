// Package community is a side-effect-only umbrella that imports every
// community-maintained DNS provider so their init() functions fire.
//
// Community providers are pulled in by the default `all` build tag,
// just like official providers, but they live under a separate
// directory tree to make their unofficial-maintenance status explicit
// to operators and reviewers.
package community

import (
	_ "github.com/justinwoo280/ech-keymgr/providers/community/mock"
)
