//go:build route53 || all

package route53

import (
	"context"
	"errors"
	"testing"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	r53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

// fakeAPI implements the route53 API subset our Provider uses,
// keeping the entire RRset state in-memory so tests don't have to
// fight sigv4 / SDK middleware.
type fakeAPI struct {
	// records keyed by (name canonicalised lowercase, no trailing dot)
	records map[string][]string

	listCalls   int
	changeCalls int
	lastChange  *r53.ChangeResourceRecordSetsInput
}

func newFakeAPI() *fakeAPI { return &fakeAPI{records: map[string][]string{}} }

func (f *fakeAPI) ListResourceRecordSets(_ context.Context, in *r53.ListResourceRecordSetsInput, _ ...func(*r53.Options)) (*r53.ListResourceRecordSetsOutput, error) {
	f.listCalls++
	out := &r53.ListResourceRecordSetsOutput{}
	for name, vals := range f.records {
		recs := make([]r53types.ResourceRecord, 0, len(vals))
		for _, v := range vals {
			v := v
			recs = append(recs, r53types.ResourceRecord{Value: awsv2.String(v)})
		}
		out.ResourceRecordSets = append(out.ResourceRecordSets, r53types.ResourceRecordSet{
			Name:            awsv2.String(name + "."),
			Type:            r53types.RRTypeHttps,
			TTL:             awsv2.Int64(300),
			ResourceRecords: recs,
		})
	}
	return out, nil
}

func (f *fakeAPI) ChangeResourceRecordSets(_ context.Context, in *r53.ChangeResourceRecordSetsInput, _ ...func(*r53.Options)) (*r53.ChangeResourceRecordSetsOutput, error) {
	f.changeCalls++
	f.lastChange = in
	for _, ch := range in.ChangeBatch.Changes {
		rr := ch.ResourceRecordSet
		key := normName(awsv2.ToString(rr.Name))
		switch ch.Action {
		case r53types.ChangeActionUpsert:
			vals := make([]string, 0, len(rr.ResourceRecords))
			for _, x := range rr.ResourceRecords {
				vals = append(vals, awsv2.ToString(x.Value))
			}
			f.records[key] = vals
		case r53types.ChangeActionDelete:
			delete(f.records, key)
		}
	}
	return &r53.ChangeResourceRecordSetsOutput{}, nil
}

func normName(n string) string {
	for len(n) > 0 && n[len(n)-1] == '.' {
		n = n[:len(n)-1]
	}
	return n
}

// ----------------------------------------------------------------
// factory
// ----------------------------------------------------------------

func TestNew_RequiresHostedZoneID(t *testing.T) {
	if _, err := New(map[string]any{}); err == nil {
		t.Errorf("expected error on missing hosted_zone_id")
	}
}

func TestNew_StripsZonePrefix(t *testing.T) {
	p, err := New(map[string]any{
		"hosted_zone_id": "/hostedzone/Z123ABC",
		"region":         "us-east-1",
		"access_key_id":  "AKIA000000000000",
		"secret_access_key": "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.(*Provider).hostedZoneID; got != "Z123ABC" {
		t.Errorf("hostedZoneID = %q, want Z123ABC", got)
	}
}

func TestNew_RequiresBothCredentialFields(t *testing.T) {
	if _, err := New(map[string]any{
		"hosted_zone_id": "Z1",
		"access_key_id":  "AKIA",
	}); err == nil {
		t.Errorf("expected error on access_key_id without secret")
	}
}

// ----------------------------------------------------------------
// API surface (using direct injection of fakeAPI)
// ----------------------------------------------------------------

func newProvider(api API) *Provider {
	return &Provider{api: api, hostedZoneID: "Z123ABC"}
}

func TestGet_NotFound(t *testing.T) {
	p := newProvider(newFakeAPI())
	_, err := p.GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if !errors.Is(err, dns.ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound, got %v", err)
	}
}

func TestGet_ReturnsRDATA(t *testing.T) {
	api := newFakeAPI()
	api.records["hidden.example.com"] = []string{`1 . alpn="h2,h3" ech="AEX"`}
	got, err := newProvider(api).GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != `1 . alpn="h2,h3" ech="AEX"` {
		t.Errorf("got %v", got)
	}
}

func TestGet_FiltersOtherTypesAndOwners(t *testing.T) {
	api := newFakeAPI()
	// Wrong owner
	api.records["other.example.com"] = []string{`1 . ech="OTHER"`}
	// Right owner
	api.records["hidden.example.com"] = []string{`1 . ech="MINE"`}
	got, _ := newProvider(api).GetHTTPSRDATA(context.Background(), "example.com", "hidden")
	if len(got) != 1 || got[0] != `1 . ech="MINE"` {
		t.Errorf("got %v", got)
	}
}

func TestPut_UpsertReplacesAtomically(t *testing.T) {
	api := newFakeAPI()
	api.records["hidden.example.com"] = []string{`1 . ech="OLD"`}
	p := newProvider(api)
	if err := p.PutHTTPSRDATA(context.Background(), "example.com", "hidden", 600,
		[]string{`1 . ech="NEW1"`, `2 . ech="NEW2"`}); err != nil {
		t.Fatal(err)
	}
	if api.changeCalls != 1 {
		t.Errorf("expected exactly 1 ChangeResourceRecordSets call, got %d", api.changeCalls)
	}
	// Confirm the change batch was a single UPSERT.
	if api.lastChange == nil || len(api.lastChange.ChangeBatch.Changes) != 1 {
		t.Fatalf("missing or multi-change batch: %+v", api.lastChange)
	}
	ch := api.lastChange.ChangeBatch.Changes[0]
	if ch.Action != r53types.ChangeActionUpsert {
		t.Errorf("action = %v, want UPSERT", ch.Action)
	}
	if got := awsv2.ToInt64(ch.ResourceRecordSet.TTL); got != 600 {
		t.Errorf("TTL = %d, want 600", got)
	}
	if got := api.records["hidden.example.com"]; len(got) != 2 || got[1] != `2 . ech="NEW2"` {
		t.Errorf("post-state = %v", got)
	}
}

func TestPut_DefaultsTTL(t *testing.T) {
	api := newFakeAPI()
	_ = newProvider(api).PutHTTPSRDATA(context.Background(), "example.com", "hidden", 0,
		[]string{`1 . ech="X"`})
	if got := awsv2.ToInt64(api.lastChange.ChangeBatch.Changes[0].ResourceRecordSet.TTL); got != 300 {
		t.Errorf("default TTL = %d, want 300", got)
	}
}

func TestDelete_RemovesExistingRRset(t *testing.T) {
	api := newFakeAPI()
	api.records["hidden.example.com"] = []string{`1 . ech="X"`}
	if err := newProvider(api).DeleteHTTPSRDATA(context.Background(), "example.com", "hidden"); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.records["hidden.example.com"]; ok {
		t.Errorf("record was not deleted: %v", api.records)
	}
}

func TestDelete_Idempotent(t *testing.T) {
	api := newFakeAPI()
	if err := newProvider(api).DeleteHTTPSRDATA(context.Background(), "example.com", "absent"); err != nil {
		t.Errorf("expected nil for idempotent delete, got %v", err)
	}
	if api.changeCalls != 0 {
		t.Errorf("idempotent delete should not call Change*, got %d calls", api.changeCalls)
	}
}

func TestFQDN(t *testing.T) {
	cases := []struct{ name, zone, want string }{
		{"@", "example.com", "example.com"},
		{"", "example.com", "example.com"},
		{"foo", "example.com", "foo.example.com"},
		{"foo.example.com", "example.com", "foo.example.com"},
		{"FOO.EXAMPLE.COM", "example.com", "FOO.EXAMPLE.COM"},
	}
	for _, c := range cases {
		if got := fqdn(c.name, c.zone); got != c.want {
			t.Errorf("fqdn(%q,%q)=%q want %q", c.name, c.zone, got, c.want)
		}
	}
}

func TestStripZonePrefix(t *testing.T) {
	cases := map[string]string{
		"Z123":               "Z123",
		"/hostedzone/Z123":   "Z123",
		"  /hostedzone/Z456": "Z456",
	}
	for in, want := range cases {
		if got := stripZonePrefix(in); got != want {
			t.Errorf("stripZonePrefix(%q)=%q want %q", in, got, want)
		}
	}
}
