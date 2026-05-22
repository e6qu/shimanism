# Vendored Azure common-types

Azure ARM specs reference shared definitions (LRO `OperationStatusResult`, `TrackedResource`, `ErrorResponse`, the API-version / resource-group / subscription-id parameters, private-link envelopes, managed-identity envelopes, customer-managed-key envelopes, etc.) via relative `$ref`s into
`Azure/azure-rest-api-specs/specification/common-types/resource-management/<vN>/`.

These files are vendored verbatim so `cmd/azure-codegen` can resolve those `$ref`s
through its `ReadFromURIFunc` hook without ever touching the network. The loader
matches the basename of an external `$ref` against the files in the matching
`v<N>` directory.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `resource-management/v1/privatelinks.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v1/privatelinks.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v1/types.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v1/types.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v2/privatelinks.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v2/privatelinks.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v2/types.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v2/types.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v3/managedidentity.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v3/managedidentity.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v3/privatelinks.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v3/privatelinks.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v3/types.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v3/types.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v4/customermanagedkeys.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v4/customermanagedkeys.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v4/managedidentity.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v4/managedidentity.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v4/managedidentitywithdelegation.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v4/managedidentitywithdelegation.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v4/privatelinks.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v4/privatelinks.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v4/types.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v4/types.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v5/customermanagedkeys.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v5/customermanagedkeys.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v5/managedidentity.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v5/managedidentity.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v5/managedidentitywithdelegation.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v5/managedidentitywithdelegation.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v5/mobo.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v5/mobo.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v5/networksecurityperimeter.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v5/networksecurityperimeter.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v5/privatelinks.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v5/privatelinks.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v5/types.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v5/types.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v6/customermanagedkeys.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v6/customermanagedkeys.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v6/managedidentity.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v6/managedidentity.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v6/managedidentitywithdelegation.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v6/managedidentitywithdelegation.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v6/mobo.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v6/mobo.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v6/networksecurityperimeter.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v6/networksecurityperimeter.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v6/privatelinks.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v6/privatelinks.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |
| `resource-management/v6/types.json` | `Azure/azure-rest-api-specs` | `specification/common-types/resource-management/v6/types.json` | MIT | `b719f25b95dc1af117ac60708398c20eb8a3315f` | 2026-05-22T10:33:00Z |

All v1-v6 files vendored in a single batch (commit `98600ee`) from
the same upstream snapshot, so each row above shares the SHA + fetch
timestamp. Azure ARM specs pick the version they need; a single
common-types file can cross-version `$ref` a sibling (e.g.
`v6/privatelinks.json` → `v5/types.json`). Per-file refresh via
`git log` on this directory.

| Version | Files vendored |
|---|---|
| v1 | privatelinks.json, types.json |
| v2 | privatelinks.json, types.json |
| v3 | managedidentity.json, privatelinks.json, types.json |
| v4 | customermanagedkeys, managedidentity, managedidentitywithdelegation, privatelinks, types |
| v5 | customermanagedkeys, managedidentity, managedidentitywithdelegation, mobo, networksecurityperimeter, privatelinks, types |
| v6 | customermanagedkeys, managedidentity, managedidentitywithdelegation, mobo, networksecurityperimeter, privatelinks, types |

## License of vendored files

`Azure/azure-rest-api-specs` is MIT-licensed; vendored files retain their
upstream license. Generated code derived from these specs is permitted under
MIT's derivative-work clause and is licensed AGPL-3.0 alongside the rest of
shimanism (see [`../../doc/COMPATIBLE_LICENSES.md`](../../doc/COMPATIBLE_LICENSES.md)).
