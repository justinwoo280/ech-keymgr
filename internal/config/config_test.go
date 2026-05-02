package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleYAML = `
domains:
  - record_fqdn: hidden.example.com
    public_name: example.com
    keydir: /etc/echkeydir/hidden.example.com
    rotation:
      interval: 1h
      grace_period: 4h
    reload:
      strategy: signal
      pid_file: /run/nginx.pid
      signal: SIGHUP
    dns:
      provider: cloudflare
      zone: example.com
      credentials_ref: cf_main
      ttl: 600

verification:
  enabled: true
  delay_after_push: 10s

credentials:
  cf_main:
    provider: cloudflare
    api_token: ${TEST_TOKEN:-fallback-token}
`

func TestParseAndValidate_HappyPath(t *testing.T) {
	t.Setenv("TEST_TOKEN", "live-token")
	c, err := ParseAndValidate([]byte(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Domains) != 1 {
		t.Fatalf("domains len = %d", len(c.Domains))
	}
	d := c.Domains[0]
	if d.Rotation.Interval != time.Hour {
		t.Errorf("interval = %v", d.Rotation.Interval)
	}
	if d.DNS.Credentials == nil {
		t.Fatal("credentials not resolved")
	}
	if d.DNS.Credentials.Extra["api_token"] != "live-token" {
		t.Errorf("env not expanded: %v", d.DNS.Credentials.Extra["api_token"])
	}
}

func TestParseAndValidate_DefaultEnvFallback(t *testing.T) {
	os.Unsetenv("TEST_TOKEN")
	c, err := ParseAndValidate([]byte(sampleYAML))
	if err != nil {
		t.Fatal(err)
	}
	if c.Domains[0].DNS.Credentials.Extra["api_token"] != "fallback-token" {
		t.Errorf("default not applied: %v", c.Domains[0].DNS.Credentials.Extra["api_token"])
	}
}

func TestExpandEnv_Forms(t *testing.T) {
	t.Setenv("FOO", "bar")
	cases := map[string]string{
		"${FOO}":                       "bar",
		"$FOO":                         "bar",
		"${UNSET}":                     "",
		"${UNSET:-fallback}":           "fallback",
		"prefix-${FOO}-suffix":         "prefix-bar-suffix",
		"$":                            "$",
		"${BAD CHARS}":                 "${BAD CHARS}", // unmatched, left alone
	}
	for in, want := range cases {
		if got := ExpandEnv(in); got != want {
			t.Errorf("ExpandEnv(%q)=%q want %q", in, got, want)
		}
	}
}

func TestValidate_RejectsMissingFields(t *testing.T) {
	cases := map[string]string{
		"missing record_fqdn": `
domains:
  - public_name: x
    keydir: /tmp
    reload: { strategy: signal, pid_file: /tmp/p }
    dns: { provider: mock, zone: x }
`,
		"missing reload pid_file": `
domains:
  - record_fqdn: x.example
    public_name: x
    keydir: /tmp
    reload: { strategy: signal }
    dns: { provider: mock, zone: x }
`,
		"unknown reload strategy": `
domains:
  - record_fqdn: x.example
    public_name: x
    keydir: /tmp
    reload: { strategy: bogus }
    dns: { provider: mock, zone: x }
`,
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAndValidate([]byte(y)); err == nil {
				t.Errorf("expected error")
			}
		})
	}
}

func TestValidate_RejectsDuplicateFQDN(t *testing.T) {
	y := `
domains:
  - record_fqdn: dup.example.com
    public_name: x
    keydir: /tmp
    reload: { strategy: signal, pid_file: /run/p }
    dns: { provider: mock, zone: x }
  - record_fqdn: dup.example.com
    public_name: x
    keydir: /tmp
    reload: { strategy: signal, pid_file: /run/p }
    dns: { provider: mock, zone: x }
`
	if _, err := ParseAndValidate([]byte(y)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate error, got %v", err)
	}
}

func TestValidate_RejectsUnknownCredRef(t *testing.T) {
	y := `
domains:
  - record_fqdn: x.example
    public_name: x
    keydir: /tmp
    reload: { strategy: signal, pid_file: /run/p }
    dns: { provider: mock, zone: x, credentials_ref: ghost }
`
	if _, err := ParseAndValidate([]byte(y)); err == nil {
		t.Errorf("expected unknown credentials_ref error")
	}
}

func TestValidate_RejectsCredProviderMismatch(t *testing.T) {
	y := `
domains:
  - record_fqdn: x.example
    public_name: x
    keydir: /tmp
    reload: { strategy: signal, pid_file: /run/p }
    dns: { provider: cloudflare, zone: x, credentials_ref: foo }
credentials:
  foo:
    provider: route53
    aws_region: us-east-1
`
	if _, err := ParseAndValidate([]byte(y)); err == nil || !strings.Contains(err.Error(), "match") {
		t.Errorf("expected provider mismatch error, got %v", err)
	}
}

func TestLoad_FromDisk(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(p, []byte(sampleYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_TOKEN", "disk-token")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Domains[0].DNS.Credentials.Extra["api_token"] != "disk-token" {
		t.Errorf("disk load: env not expanded")
	}
}

func TestLoad_RejectsMissingFile(t *testing.T) {
	if _, err := Load("/no/such/file.yaml"); err == nil {
		t.Errorf("expected error")
	}
}

func TestDefaults_AppliedToOmittedFields(t *testing.T) {
	y := `
domains:
  - record_fqdn: x.example
    public_name: x
    keydir: /tmp
    reload: { strategy: signal, pid_file: /run/p }
    dns: { provider: mock, zone: x }
`
	c, err := ParseAndValidate([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	d := c.Domains[0]
	if d.Rotation.Interval != DefaultRotationInterval {
		t.Errorf("interval default not applied: %v", d.Rotation.Interval)
	}
	if d.Rotation.GracePeriod != DefaultGracePeriod {
		t.Errorf("grace default not applied: %v", d.Rotation.GracePeriod)
	}
	if d.DNS.TTL != DefaultDNSTTL {
		t.Errorf("ttl default not applied: %v", d.DNS.TTL)
	}
	if d.Reload.Signal != DefaultReloadSignal {
		t.Errorf("signal default not applied: %v", d.Reload.Signal)
	}
}
