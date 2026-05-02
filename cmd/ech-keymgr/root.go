package main

import (
	"github.com/spf13/cobra"

	// Side-effect: register every officially-maintained DNS provider.
	_ "github.com/justinwoo280/ech-keymgr/providers"
)

// globalFlags are visible on every subcommand. We deliberately keep
// the surface tiny: one `--config` path is enough — every detail
// lives inside the YAML.
type globalFlags struct {
	configPath string
	verbose    bool
}

var gflags globalFlags

// newRootCmd builds the cobra command tree.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ech-keymgr",
		Short: "Automated HPKE key & ECHConfig rotation for TLS Encrypted Client Hello",
		Long: `ech-keymgr generates ECH HPKE key pairs (RFC 9849), publishes the resulting
ECHConfigList into a single HTTPS DNS resource record (RFC 9460/9848),
and rotates everything on a schedule without dropping in-flight TLS
handshakes.

Configuration lives in a YAML file (default: /etc/ech-keymgr/config.yaml).
The same file describes every managed domain, the DNS provider creds,
and the web-server reload strategy.

See https://github.com/justinwoo280/ech-keymgr for documentation.`,
		Version:       Version,
		SilenceUsage:  true, // do not dump full help on every error
		SilenceErrors: false,
	}
	root.PersistentFlags().StringVarP(&gflags.configPath, "config", "c",
		"/etc/ech-keymgr/config.yaml", "path to YAML configuration file")
	root.PersistentFlags().BoolVarP(&gflags.verbose, "verbose", "v", false,
		"enable verbose (debug) logging")

	root.AddCommand(
		newRotateCmd(),
		newVerifyCmd(),
		newStatusCmd(),
		newInitCmd(),
		newDaemonCmd(),
		newKeygenCmd(),
	)
	return root
}
