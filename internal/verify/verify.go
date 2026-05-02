package verify

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/justinwoo280/ech-keymgr/internal/echconfig"
	"github.com/justinwoo280/ech-keymgr/internal/keystore"
	"github.com/justinwoo280/ech-keymgr/pkg/dns"
	"github.com/justinwoo280/ech-keymgr/pkg/svcb"
)

// Request bundles everything one verify pass needs.
type Request struct {
	// RecordFQDN is the absolute name we expect HTTPS RR(s) under.
	RecordFQDN string

	// DNSZone is the zone passed to the Source (so it can route
	// the lookup; meaning is identical to rotator.Config.DNSZone).
	DNSZone string

	// OwnerRel is the owner name relative to the zone, as the
	// Source expects it. Use "@" for the apex. Computed by the
	// caller (typically via rotator's relName helper).
	OwnerRel string

	// Source is where the published RDATA is read from.
	Source Source

	// Store is consulted to decide which config_ids should /
	// should not appear in DNS.
	Store *keystore.Store
}

// Verify performs one reconciliation pass and returns a Report. It
// NEVER returns an error — every issue is encoded as a SeverityWarn
// Finding inside the Report. The error return is reserved for
// catastrophic mis-wiring (nil Source, nil Store) the caller
// should treat as a programmer bug, not a runtime warning.
func Verify(ctx context.Context, req Request) (*Report, error) {
	if req.Source == nil {
		return nil, errors.New("verify: Source is required")
	}
	if req.Store == nil {
		return nil, errors.New("verify: Store is required")
	}
	rep := &Report{
		RecordFQDN: req.RecordFQDN,
		Source:     req.Source.Name(),
	}

	// 1. Read the published HTTPS RR(s).
	rdata, err := req.Source.GetHTTPSRDATA(ctx, req.DNSZone, req.OwnerRel)
	if errors.Is(err, dns.ErrRecordNotFound) {
		rep.Add(SeverityWarn, CodeRRMissing,
			fmt.Sprintf("no HTTPS RR found at %s — run `ech-keymgr init` if this domain is fresh", req.RecordFQDN))
		return rep, nil
	}
	if err != nil {
		rep.Add(SeverityWarn, CodeRRMissing,
			fmt.Sprintf("source %s could not return RDATA: %v", req.Source.Name(), err))
		return rep, nil
	}
	if len(rdata) == 0 {
		rep.Add(SeverityWarn, CodeRRMissing, "source returned an empty record set")
		return rep, nil
	}
	rep.Add(SeverityOK, CodeRRPresent,
		fmt.Sprintf("found %d HTTPS RR(s) at %s", len(rdata), req.RecordFQDN))
	if len(rdata) > 1 {
		rep.Add(SeverityWarn, CodeRRMultiple,
			fmt.Sprintf("multiple HTTPS RRs at owner; only the first one's `ech=` is checked (%d total)", len(rdata)))
	}

	// 2. Parse the first record's `ech=` SvcParam.
	rec, err := svcb.Parse(rdata[0])
	if err != nil {
		rep.Add(SeverityWarn, CodeRRPresent,
			fmt.Sprintf("HTTPS RR did not parse as RFC 9460 presentation form: %v", err))
		return rep, nil
	}
	echB64, ok := svcb.GetECH(rec)
	if !ok || echB64 == "" {
		rep.Add(SeverityWarn, CodeECHParamMissing,
			"HTTPS RR has no `ech=` SvcParam — clients can't use ECH against this domain")
		return rep, nil
	}
	listBytes, err := base64.StdEncoding.DecodeString(echB64)
	if err != nil {
		rep.Add(SeverityWarn, CodeECHBadBase64,
			fmt.Sprintf("`ech=` value is not valid base64: %v", err))
		return rep, nil
	}

	// 3. Decode the ECHConfigList.
	list, err := echconfig.UnmarshalList(listBytes)
	if err != nil {
		rep.Add(SeverityWarn, CodeECHListBadFormat,
			fmt.Sprintf("`ech=` does not decode as RFC 9849 ECHConfigList: %v", err))
		return rep, nil
	}
	rep.Add(SeverityOK, CodeECHListEntryCount,
		fmt.Sprintf("ECHConfigList contains %d ECHConfig entry/entries (and %d unknown-version entries)",
			len(list.Configs), len(list.Unknown)))

	// 4. Cross-reference DNS list ↔ keystore.
	dnsIDs := map[uint8]bool{}
	for _, c := range list.Configs {
		dnsIDs[c.ConfigID] = true
	}
	storeByID := map[uint8]keystore.Entry{}
	for _, e := range req.Store.List() {
		storeByID[e.ConfigID] = e
	}

	// 4a. Every DNS-published config_id should map to a known key.
	for id := range dnsIDs {
		if _, found := storeByID[id]; found {
			rep.Add(SeverityOK, CodeKeyInDNSAndStore,
				fmt.Sprintf("config_id 0x%02x is in DNS and in the local keystore", id))
		} else {
			rep.Add(SeverityWarn, CodeKeyInDNSNotStore,
				fmt.Sprintf("config_id 0x%02x is published in DNS but missing from local keystore — was it deleted manually?", id))
		}
	}

	// 4b. Every Current/Previous keystore entry should be in DNS.
	//     Grace entries are intentionally NOT expected.
	for id, e := range storeByID {
		if dnsIDs[id] {
			continue
		}
		switch e.State {
		case keystore.StateCurrent, keystore.StatePrevious:
			rep.Add(SeverityWarn, CodeKeyExpectedNotInDNS,
				fmt.Sprintf("config_id 0x%02x is %s in the keystore but not in DNS — propagation in progress, or DNS was changed externally?",
					id, e.State))
		case keystore.StateGrace:
			// Expected to be absent from DNS; record OK only if
			// asked for verbosity. We surface it at OK level so
			// the operator can see why a grace key is still on disk.
			rep.Add(SeverityOK, CodeKeyInStoreNotDNS,
				fmt.Sprintf("config_id 0x%02x is in grace; not in DNS as expected (drop_at=%s)",
					id, e.ScheduledDropAt.Format("2006-01-02T15:04:05Z07:00")))
		}
	}
	return rep, nil
}
