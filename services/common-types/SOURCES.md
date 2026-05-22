# Vendored Azure common-types

Azure ARM specs reference shared definitions (LRO `OperationStatusResult`, `TrackedResource`, `ErrorResponse`, the API-version / resource-group / subscription-id parameters, private-link envelopes, managed-identity envelopes, customer-managed-key envelopes, etc.) via relative `$ref`s into
`Azure/azure-rest-api-specs/specification/common-types/resource-management/<vN>/`.

These files are vendored verbatim so `cmd/azure-codegen` can resolve those `$ref`s
through its `ReadFromURIFunc` hook without ever touching the network. The loader
matches the basename of an external `$ref` against the files in the matching
`v<N>` directory.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `resource-management/v4/types.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v4/types.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v4/privatelinks.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v4/privatelinks.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v4/managedidentity.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v4/managedidentity.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v4/managedidentitywithdelegation.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v4/managedidentitywithdelegation.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v4/customermanagedkeys.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v4/customermanagedkeys.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |

## License of vendored files

`Azure/azure-rest-api-specs` is MIT-licensed; vendored files retain their
upstream license. Generated code derived from these specs is permitted under
MIT's derivative-work clause and is licensed AGPL-3.0 alongside the rest of
shimanism (see [`../../doc/COMPATIBLE_LICENSES.md`](../../doc/COMPATIBLE_LICENSES.md)).
