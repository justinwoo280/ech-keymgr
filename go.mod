module github.com/justinwoo280/ech-keymgr

go 1.25.0

// Pin a toolchain that includes the latest known stdlib vulnerability
// fixes. Bumped each time govulncheck calls us out:
//   1.25.7  GO-2026-4337  GO-2026-4340  (crypto/tls)
//   1.25.9  GO-2026-4602  GO-2026-4870  (crypto/x509)
//           GO-2026-4946  GO-2026-4947  (os.Root)
//   1.25.10 GO-2026-4918  (net/http)
//           GO-2026-4971  (net)
// Anyone building with an older 'go' command will have this
// toolchain auto-downloaded by the Go tooling.
toolchain go1.25.10

require (
	github.com/akamai/AkamaiOPEN-edgegrid-golang/v13 v13.1.0
	github.com/aws/aws-sdk-go-v2 v1.41.7
	github.com/aws/aws-sdk-go-v2/config v1.32.17
	github.com/aws/aws-sdk-go-v2/credentials v1.19.16
	github.com/aws/aws-sdk-go-v2/service/route53 v1.62.7
	github.com/cloudflare/circl v1.6.3
	github.com/spf13/cobra v1.10.2
	golang.org/x/crypto v0.51.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.24 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.42.1 // indirect
	github.com/aws/smithy-go v1.25.1 // indirect
	github.com/benbjohnson/clock v1.3.5 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	go.uber.org/ratelimit v0.3.1 // indirect
	golang.org/x/sys v0.44.0 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
)
