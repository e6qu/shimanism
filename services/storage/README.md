# services/storage

Object-storage shim. Phase 1 of [PLAN.md](../../PLAN.md).

## Layout

```
services/storage/
├── README.md                       # this file
├── spec/                           # vendored API specs
│   ├── aws-s3.smithy.json          # AWS S3 Smithy 2.0 model (vendored from aws/aws-sdk-go-v2)
│   └── SOURCES.md                  # upstream URL + pinned commit SHA per spec
├── gen/                            # generated server stubs (Phase 1.3+)
├── translate/                      # hand-written per-backend translation tables (Phase 1.5+)
└── conformance/                    # SDK / CLI / Terraform driver tests (Phase 1.4+)
```

## Refreshing the spec

```
make fetch-specs                                    # all services, current pins
scripts/fetch-aws-spec.sh s3 services/storage       # just S3, track main
scripts/fetch-aws-spec.sh s3 services/storage v1.31 # pin to a specific aws-sdk-go-v2 ref
```

Re-running overwrites the JSON + the SOURCES.md row. Review the diff, commit normally, open a PR. The change in pinned SHA is the audit trail.

## Why vendored

Reproducible builds. CI runs against the committed spec; no network dependency. Updates are explicit (a deliberate PR with a SHA bump) rather than transparent (silent drift on upstream master).

## Source

Spec: AWS S3, [Smithy 2.0](https://smithy.io/) model from [`aws/aws-sdk-go-v2`](https://github.com/aws/aws-sdk-go-v2/tree/main/codegen/sdk-codegen/aws-models). 107 operations across 787 shapes (at the currently-pinned snapshot — see [`spec/SOURCES.md`](spec/SOURCES.md) for the exact SHA).
