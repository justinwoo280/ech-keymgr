// Package config loads and validates the YAML configuration file
// that drives ech-keymgr.
//
// One file describes:
//
//   - A list of managed domains, each with its DNS provider /
//     reload strategy / rotation timing.
//   - A keyed pool of credential blocks, referenced by `credentials_ref`
//     from each domain. Splitting credentials out lets multiple
//     domains share a single Cloudflare token without duplication.
//   - A small `verification` block tuning the soft DNS reconciliation.
//
// Environment variable expansion is performed on string-typed
// values whose content matches `${VAR}` or `$VAR`. This is the
// standard idiom for keeping API tokens out of YAML.
//
// Example:
//
//	domains:
//	  - record_fqdn: hidden.example.com
//	    public_name: example.com
//	    keydir:      /etc/echkeydir/hidden.example.com
//	    rotation:
//	      interval:     3h
//	      grace_period: 6h
//	    cipher_suites:
//	      - aes128gcm-sha256
//	      - chacha20poly1305-sha256
//	    reload:
//	      strategy: signal
//	      pid_file: /run/nginx.pid
//	      signal:   SIGHUP
//	    dns:
//	      provider:        cloudflare
//	      zone:            example.com
//	      credentials_ref: cf_main
//	      ttl:             300
//
//	verification:
//	  enabled:          true
//	  delay_after_push: 30s
//	  on_mismatch:      warn
//
//	credentials:
//	  cf_main:
//	    provider:  cloudflare
//	    api_token: ${CF_API_TOKEN}
//
// The package returns a fully-validated `*Config` with all
// references resolved (each Domain.DNS.Credentials is the actual
// Credential struct, not just its key).
package config
