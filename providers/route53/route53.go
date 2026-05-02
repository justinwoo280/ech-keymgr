//go:build route53 || all

package route53

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	r53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/justinwoo280/ech-keymgr/pkg/dns"
)

func init() {
	dns.Register("route53", New)
}

// API is the subset of route53.Client we actually call. We extract
// it as an interface so tests can supply a fake without spinning
// up an httptest server (sigv4 + the SDK's middleware chain are
// easier to bypass than to mock).
type API interface {
	ChangeResourceRecordSets(ctx context.Context, in *r53.ChangeResourceRecordSetsInput, opts ...func(*r53.Options)) (*r53.ChangeResourceRecordSetsOutput, error)
	ListResourceRecordSets(ctx context.Context, in *r53.ListResourceRecordSetsInput, opts ...func(*r53.Options)) (*r53.ListResourceRecordSetsOutput, error)
}

// Provider is the Route 53 implementation of dns.Provider.
type Provider struct {
	api          API
	hostedZoneID string
}

var _ dns.Provider = (*Provider)(nil)

// New is the Factory consumed by pkg/dns.Build.
//
// Required cfg keys:
//
//	hosted_zone_id: string  (the Zxxxx ID, NOT the zone name)
//
// Optional cfg keys (otherwise the standard AWS credential chain
// is used: env vars, ~/.aws/credentials, instance profile, IRSA):
//
//	region:            string  (defaults to whatever the chain provides)
//	profile:           string  (named profile in shared credentials file)
//	access_key_id:     string  (paired with secret_access_key)
//	secret_access_key: string
//	session_token:     string  (optional, with the above)
func New(raw map[string]any) (dns.Provider, error) {
	hostedZoneID, _ := raw["hosted_zone_id"].(string)
	if strings.TrimSpace(hostedZoneID) == "" {
		return nil, errors.New("route53: hosted_zone_id is required")
	}

	cfgOpts, err := buildLoadOptions(raw)
	if err != nil {
		return nil, err
	}
	awsCfg, err := config.LoadDefaultConfig(context.Background(), cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("route53: load AWS config: %w", err)
	}

	return &Provider{
		api:          r53.NewFromConfig(awsCfg),
		hostedZoneID: stripZonePrefix(hostedZoneID),
	}, nil
}

// stripZonePrefix accepts both "Z2FDTNDATAQYW2" and the API's full
// "/hostedzone/Z2FDTNDATAQYW2" form, normalising to the bare ID.
func stripZonePrefix(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/hostedzone/")
	return s
}

func buildLoadOptions(raw map[string]any) ([]func(*config.LoadOptions) error, error) {
	getStr := func(k string) string {
		if v, ok := raw[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	var opts []func(*config.LoadOptions) error

	if region := getStr("region"); region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	if profile := getStr("profile"); profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	akid := getStr("access_key_id")
	secret := getStr("secret_access_key")
	if akid != "" || secret != "" {
		if akid == "" || secret == "" {
			return nil, errors.New("route53: access_key_id and secret_access_key must be set together")
		}
		token := getStr("session_token")
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(akid, secret, token),
		))
	}
	return opts, nil
}

// Name implements dns.Provider.
func (p *Provider) Name() string { return "route53" }

// GetHTTPSRDATA implements dns.Provider.
//
// Route 53's ListResourceRecordSets returns records sorted by name,
// type, and set identifier. We narrow with StartRecordName + StartRecordType
// and then filter the response to the exact (name, type) we care about.
func (p *Provider) GetHTTPSRDATA(ctx context.Context, zone, name string) ([]string, error) {
	owner := fqdn(name, zone)
	in := &r53.ListResourceRecordSetsInput{
		HostedZoneId:    awsv2.String(p.hostedZoneID),
		StartRecordName: awsv2.String(owner + "."),
		StartRecordType: r53types.RRTypeHttps,
		MaxItems:        awsv2.Int32(10),
	}
	out, err := p.api.ListResourceRecordSets(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("route53: ListResourceRecordSets: %w", err)
	}
	for _, rr := range out.ResourceRecordSets {
		if rr.Type != r53types.RRTypeHttps {
			continue
		}
		if !nameEqual(awsv2.ToString(rr.Name), owner) {
			continue
		}
		if len(rr.ResourceRecords) == 0 {
			return nil, dns.ErrRecordNotFound
		}
		results := make([]string, 0, len(rr.ResourceRecords))
		for _, x := range rr.ResourceRecords {
			results = append(results, strings.TrimSpace(awsv2.ToString(x.Value)))
		}
		return results, nil
	}
	return nil, dns.ErrRecordNotFound
}

// PutHTTPSRDATA implements dns.Provider via a single UPSERT change.
// Route 53 atomically replaces the entire RRset, so there is no
// "in-between" state the way Cloudflare's create-then-delete has.
func (p *Provider) PutHTTPSRDATA(ctx context.Context, zone, name string, ttl uint32, rdata []string) error {
	if ttl == 0 {
		ttl = 300
	}
	owner := fqdn(name, zone)
	records := make([]r53types.ResourceRecord, 0, len(rdata))
	for _, line := range rdata {
		v := line
		records = append(records, r53types.ResourceRecord{Value: awsv2.String(v)})
	}
	in := &r53.ChangeResourceRecordSetsInput{
		HostedZoneId: awsv2.String(p.hostedZoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Comment: awsv2.String("ech-keymgr UPSERT"),
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionUpsert,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            awsv2.String(owner),
					Type:            r53types.RRTypeHttps,
					TTL:             awsv2.Int64(int64(ttl)),
					ResourceRecords: records,
				},
			}},
		},
	}
	if _, err := p.api.ChangeResourceRecordSets(ctx, in); err != nil {
		return fmt.Errorf("route53: ChangeResourceRecordSets UPSERT: %w", err)
	}
	return nil
}

// DeleteHTTPSRDATA implements dns.Provider.
//
// Route 53's DELETE action requires the EXACT current contents of
// the RRset; specifying a wrong TTL or value yields InvalidChangeBatch.
// We therefore List first, then submit a DELETE with the literal
// values we just read back. If no record exists, we return nil
// (idempotent contract).
func (p *Provider) DeleteHTTPSRDATA(ctx context.Context, zone, name string) error {
	owner := fqdn(name, zone)
	in := &r53.ListResourceRecordSetsInput{
		HostedZoneId:    awsv2.String(p.hostedZoneID),
		StartRecordName: awsv2.String(owner + "."),
		StartRecordType: r53types.RRTypeHttps,
		MaxItems:        awsv2.Int32(10),
	}
	out, err := p.api.ListResourceRecordSets(ctx, in)
	if err != nil {
		return fmt.Errorf("route53: ListResourceRecordSets: %w", err)
	}
	for _, rr := range out.ResourceRecordSets {
		if rr.Type != r53types.RRTypeHttps || !nameEqual(awsv2.ToString(rr.Name), owner) {
			continue
		}
		// Found it — DELETE with the exact values.
		_, err := p.api.ChangeResourceRecordSets(ctx, &r53.ChangeResourceRecordSetsInput{
			HostedZoneId: awsv2.String(p.hostedZoneID),
			ChangeBatch: &r53types.ChangeBatch{
				Comment: awsv2.String("ech-keymgr DELETE"),
				Changes: []r53types.Change{{
					Action:            r53types.ChangeActionDelete,
					ResourceRecordSet: &rr,
				}},
			},
		})
		if err != nil {
			return fmt.Errorf("route53: ChangeResourceRecordSets DELETE: %w", err)
		}
		return nil
	}
	return nil // idempotent
}

// ----------------------------------------------------------------
// name helpers
// ----------------------------------------------------------------

// fqdn converts an owner relative to zone into the absolute name
// Route 53 expects.
//
//	("@",   "example.com")    → "example.com"
//	("foo", "example.com")    → "foo.example.com"
//
// We deliberately do NOT add the trailing dot; the Route 53 API
// canonicalises both forms identically. Returned values from
// ListResourceRecordSets DO contain the trailing dot, which is why
// nameEqual below normalises both sides.
func fqdn(name, zone string) string {
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	name = strings.TrimSuffix(name, ".")
	if name == "" || name == "@" {
		return zone
	}
	if strings.EqualFold(name, zone) || strings.HasSuffix(strings.ToLower(name), "."+zone) {
		return name
	}
	return name + "." + zone
}

// nameEqual case-insensitively compares two DNS names ignoring any
// trailing dots. Route 53 returns names with a trailing dot; our
// fqdn() doesn't add one.
func nameEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}
