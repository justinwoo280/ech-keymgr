// Package mock is an in-memory dns.Provider that stores HTTPS RRsets
// in a map keyed by (zone, name). It exists for two purposes:
//
//  1. As a "hello world" template for community contributors writing
//     a brand-new provider. Copy this package, swap the in-memory map
//     for real API calls, and you have a working provider.
//
//  2. As a CI-friendly target for ech-keymgr's own end-to-end tests:
//     the rotator can be exercised against a mock without touching a
//     real DNS service.
//
// This provider is registered under the name "mock" and gated by the
// build tag `mock || community || all` so it's available by default.
//
// Configuration is empty: there are no credentials.
//
//	credentials:
//	  test:
//	    provider: mock
package mock
