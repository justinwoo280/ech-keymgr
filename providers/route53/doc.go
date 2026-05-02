// Package route53 implements the ech-keymgr DNS Provider interface
// against AWS Route 53 using the official aws-sdk-go-v2.
//
// Route 53's HTTPS / SVCB record support landed in 2023 and is fully
// available through the standard ChangeResourceRecordSets API. This
// provider relies on:
//
//   - github.com/aws/aws-sdk-go-v2/service/route53  (the typed client)
//   - github.com/aws/aws-sdk-go-v2/config           (default credential
//     chain: env vars,
//     shared profile,
//     EC2/ECS/IRSA, …)
//
// We use the SDK's UPSERT semantics so a single call covers both
// "create new record" and "replace existing record" — same as our
// PowerDNS provider, the cleanest of the four.
//
// Configuration shape (under credentials.<ref> in config.yaml):
//
//	provider:           route53
//	hosted_zone_id:     Z2FDTNDATAQYW2          # required
//	region:             us-east-1               # optional; default
//	                                              the SDK's chain
//	# Optional credential overrides; otherwise the standard
//	# AWS credential chain is used (env vars, ~/.aws/credentials,
//	# instance profile, IRSA token, etc.).
//	access_key_id:      AKIA...                 # optional
//	secret_access_key:  ...                     # optional
//	profile:            production              # optional
//
// Note that the YAML's `dns.zone` field (the public zone name like
// "example.com") and `hosted_zone_id` (the AWS internal Zxxxx ID)
// are BOTH required: AWS APIs need the ID, but pkg/svcb still needs
// the zone name to build owner-relative names. The provider does
// not auto-discover the ID from the name to avoid the IAM
// permissions ListHostedZonesByName demands.
//
// Required IAM permissions:
//
//	route53:ChangeResourceRecordSets
//	route53:ListResourceRecordSets
//
// Both can be scoped to your hosted zone ARN
// (`arn:aws:route53:::hostedzone/<id>`).
//
// AWS API documentation:
//
//	https://docs.aws.amazon.com/Route53/latest/APIReference/
package route53
