// Package svcb implements RFC 9460 SVCB / HTTPS resource record
// presentation-form parsing and serialization, plus a small helper
// to swap a single SvcParam (the `ech=` value used by ech-keymgr)
// while preserving every other field.
//
// This package is the SINGLE SOURCE OF TRUTH for ECH-aware HTTPS
// RR manipulation. DNS providers do not parse RDATA themselves; the
// ech-keymgr core uses this package to read RR strings from a
// provider, mutate the ech= field, and hand the strings back.
//
// We deliberately implement only the subset of RFC 9460 that
// ech-keymgr needs:
//
//   - ServiceMode and AliasMode are both parsed.
//   - SvcParam values are kept as raw, presentation-form text:
//     ech-keymgr only ever needs to read or replace the `ech=`
//     value as an opaque base64 string; it never inspects the
//     binary contents of e.g. ipv4hint or alpn from inside the
//     mutation path.
//   - RFC 9460 §A escape decoding ("\\NNN", "\\X") is NOT applied;
//     we treat values as opaque presentation-form runs of bytes
//     between the surrounding quotes (or up to the next whitespace).
//     This is sufficient because the only key we modify (`ech`) is
//     a base64 string with no characters that require escaping.
package svcb

import (
	"errors"
	"fmt"
	"strings"
)

// ECHParamKey is the canonical SvcParamKey name for ECH per RFC 9848.
const ECHParamKey = "ech"

// AliasModePriority is the SvcPriority value (0) that signals
// AliasMode per RFC 9460 §2.4.2.
const AliasModePriority = 0

// Record is a parsed view of a single HTTPS / SVCB RR in
// presentation form.
//
// In AliasMode (Priority == 0), Params is empty per RFC 9460 §2.4.2.
// In ServiceMode (Priority > 0), Params holds the SvcParam list
// preserving the order in which keys were observed; this is purely
// for round-trip stability and has no protocol meaning.
type Record struct {
	Priority uint16
	Target   string  // "." for "use the owner name"
	Params   []Param // omit/nil in AliasMode
}

// Param is a single SvcParamKey=SvcParamValue pair in presentation
// form. Value is stored exactly as written between the surrounding
// quotes (or as the bare token if unquoted), without RFC 9460 §A
// escape decoding.
type Param struct {
	Key   string
	Value string
}

// IsAliasMode reports whether this record is in AliasMode.
func (r *Record) IsAliasMode() bool { return r.Priority == AliasModePriority }

// GetParam returns the value of the first SvcParam matching key,
// and a boolean indicating whether it was present.
func (r *Record) GetParam(key string) (string, bool) {
	for _, p := range r.Params {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

// SetParam sets the value of key. If key is already present it is
// updated in place (preserving its position in the slice for stable
// round-trips); otherwise it is appended.
func (r *Record) SetParam(key, value string) {
	for i := range r.Params {
		if r.Params[i].Key == key {
			r.Params[i].Value = value
			return
		}
	}
	r.Params = append(r.Params, Param{Key: key, Value: value})
}

// DeleteParam removes the named SvcParam if present.
func (r *Record) DeleteParam(key string) {
	out := r.Params[:0]
	for _, p := range r.Params {
		if p.Key != key {
			out = append(out, p)
		}
	}
	r.Params = out
}

// Parse parses a single HTTPS / SVCB RR in RFC 9460 presentation
// form: `<priority> <target> [key=value ...]`.
//
// Whitespace separators are runs of ASCII space or tab. SvcParam
// values may be:
//   - bare:        key=value         (until next whitespace)
//   - quoted:      key="v with sp"   (between matching double quotes)
//   - keyless:     key               (treated as empty value)
//
// This parser is intentionally tolerant; a few vendor APIs return
// values with idiosyncratic whitespace. It is strict only about
// requiring a numeric priority, a non-empty target, and matched
// quotes.
func Parse(s string) (*Record, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("svcb: empty RDATA")
	}

	// Priority.
	priTok, rest := nextToken(s)
	if priTok == "" {
		return nil, errors.New("svcb: missing SvcPriority")
	}
	pri, err := parseUint16(priTok)
	if err != nil {
		return nil, fmt.Errorf("svcb: invalid SvcPriority %q: %w", priTok, err)
	}

	// Target.
	rest = strings.TrimLeft(rest, " \t")
	tgtTok, rest := nextToken(rest)
	if tgtTok == "" {
		return nil, errors.New("svcb: missing TargetName")
	}

	rec := &Record{
		Priority: pri,
		Target:   tgtTok,
	}

	// In AliasMode, RFC 9460 §2.4.2 says SvcParams MUST be ignored
	// by clients; we permit them syntactically but drop them.
	if pri == AliasModePriority {
		return rec, nil
	}

	// SvcParams.
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			break
		}
		var key, val string
		key, val, rest, err = nextParam(rest)
		if err != nil {
			return nil, err
		}
		rec.Params = append(rec.Params, Param{Key: key, Value: val})
	}
	return rec, nil
}

// String serializes the record back to RFC 9460 presentation form.
// Output is stable for a given input modulo whitespace runs.
//
// Values are always emitted enclosed in double quotes for
// determinism, even when not strictly required. This avoids
// ambiguity with vendor APIs that re-parse our output.
func (r *Record) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s", r.Priority, r.Target)
	if r.IsAliasMode() {
		return b.String()
	}
	for _, p := range r.Params {
		// Empty value: emit bare key per RFC 9460 §2.1
		// (`alpn` without a value is invalid, but for forward-
		//  compat we faithfully round-trip whatever we got).
		if p.Value == "" {
			fmt.Fprintf(&b, " %s", p.Key)
			continue
		}
		fmt.Fprintf(&b, " %s=%q", p.Key, p.Value)
	}
	return b.String()
}

// SetECH returns a copy of base with its `ech=` SvcParam set to
// echBase64. Every other SvcParam, the priority, and the target
// are preserved.
//
// If base is in AliasMode (priority 0), an error is returned: ECH
// is meaningful only for ServiceMode records.
func SetECH(base *Record, echBase64 string) (*Record, error) {
	if base.IsAliasMode() {
		return nil, errors.New("svcb: cannot set ech= on an AliasMode HTTPS record")
	}
	cp := *base
	cp.Params = append([]Param(nil), base.Params...)
	cp.SetParam(ECHParamKey, echBase64)
	return &cp, nil
}

// GetECH extracts the `ech=` SvcParam (the base64-encoded
// ECHConfigList) from r. Returns ("", false) if absent.
func GetECH(r *Record) (string, bool) { return r.GetParam(ECHParamKey) }

// ----------------------------------------------------------------
// internal tokenizer
// ----------------------------------------------------------------

func nextToken(s string) (tok, rest string) {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

func nextParam(s string) (key, val, rest string, err error) {
	// key
	eq := strings.IndexAny(s, "= \t")
	if eq == -1 {
		// keyless param
		return s, "", "", nil
	}
	if s[eq] != '=' {
		return s[:eq], "", s[eq:], nil
	}
	key = s[:eq]
	if key == "" {
		return "", "", "", errors.New("svcb: empty SvcParamKey")
	}
	rem := s[eq+1:]
	if rem == "" {
		return key, "", "", nil
	}
	if rem[0] == '"' {
		// quoted value
		end := strings.IndexByte(rem[1:], '"')
		if end == -1 {
			return "", "", "", fmt.Errorf("svcb: unterminated quoted value for %q", key)
		}
		val = rem[1 : 1+end]
		rest = rem[1+end+1:]
		return key, val, rest, nil
	}
	// bare value, terminated by whitespace
	tok, rest := nextToken(rem)
	return key, tok, rest, nil
}

func parseUint16(s string) (uint16, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	var v uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit %q", c)
		}
		v = v*10 + uint64(c-'0')
		if v > 0xFFFF {
			return 0, errors.New("value out of range for uint16")
		}
	}
	return uint16(v), nil
}
