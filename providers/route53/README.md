# AWS Route 53 DNS provider

Talks to AWS Route 53 using the official
[`aws-sdk-go-v2`](https://github.com/aws/aws-sdk-go-v2). HTTPS / SVCB
record types are first-class in Route 53 since 2023, so no special
encoding is needed.

## Configuration

```yaml
domains:
  - record_fqdn: hidden.example.com
    public_name: example.com
    keydir: /etc/echkeydir/hidden.example.com
    rotation:
      interval: 3h
      grace_period: 6h
    dns:
      provider: route53
      zone: example.com
      credentials_ref: r53_main
      ttl: 300

credentials:
  r53_main:
    provider:        route53
    hosted_zone_id:  Z2FDTNDATAQYW2     # required
    region:          us-east-1          # optional; SDK chain otherwise
    # Optional credentials. If omitted, the standard AWS credential chain
    # is used: env vars (AWS_ACCESS_KEY_ID, ...), ~/.aws/credentials,
    # EC2 instance profile, ECS task role, IRSA, etc.
    # access_key_id:     AKIA...
    # secret_access_key: ...
    # session_token:     ...            # optional, with the above
    # profile:           production     # use a named profile
```

`hosted_zone_id` accepts both the bare ID (`Z2FDTNDATAQYW2`) and the
ARN-style prefix (`/hostedzone/Z2FDTNDATAQYW2`).

## IAM permissions

A minimal policy scoped to your hosted zone:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "route53:ChangeResourceRecordSets",
        "route53:ListResourceRecordSets"
      ],
      "Resource": "arn:aws:route53:::hostedzone/Z2FDTNDATAQYW2"
    }
  ]
}
```

## What this provider does

- **GET**: `ListResourceRecordSets` filtered with `StartRecordName` +
  `StartRecordType=HTTPS`, then re-filtered client-side to the exact
  owner. Returns each record's `Value` field verbatim — already in
  RFC 9460 presentation form, no translation needed.
- **PUT**: a single `ChangeResourceRecordSets` with action `UPSERT`.
  Route 53 atomically replaces the entire RRset; there is no
  intermediate state where the record is empty.
- **DELETE**: lists the existing record, then submits a `DELETE`
  change containing the exact values it just read (Route 53 rejects
  DELETE requests where `Value` doesn't match the live record).
  If the record is absent, returns `nil` (idempotent contract).

## Build tags

```bash
go build ./cmd/ech-keymgr                 # all official providers (default)
go build -tags=route53 ./cmd/ech-keymgr   # only Route 53
```

## Notes

- `hosted_zone_id` is REQUIRED — we do not call
  `ListHostedZonesByName` to auto-resolve it, because that requires
  broader IAM permissions than most operators want to grant.
- The provider works seamlessly with **IRSA** on EKS / **instance
  profiles** on EC2 / **ECS task roles** — leave the credential
  fields blank and the SDK's default chain handles the rest.
- Route 53 hosted zones can be either `Public` or `Private`; the
  same provider handles both. The zone type only affects DNS
  resolution behaviour, not the API surface.

## Maintainers

Officially maintained by the ech-keymgr project.
