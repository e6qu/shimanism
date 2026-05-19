# Functions importer-read contract

> Phase 9 sub-phase 9.2 — captured from `terraform import aws_lambda_function` against the shim's AWS Lambda frontend.

## aws_lambda_function import — observed wire ops

restJson1; the importer Read path is one of the most read-heavy of any AWS resource — it touches **7+ subresources** even on a freshly-created function.

| HTTP method + path | Category | Status (before / after Phase 9 fixes) |
|---|---|---|
| `GET /2015-03-31/functions/<n>` | 1 | ✅ |
| `GET /2015-03-31/functions/<n>/versions?MaxItems=10000` | 2 (single `$LATEST`) | ❌ → ✅ (added in Phase 9.5) |
| `GET /2021-10-31/functions/<n>/url` (function URL config) | 2 | ❌ → ✅ (404 ResourceNotFoundException — feature unset) |
| `GET /2017-03-31/tags/<arn>` (ListTags) | 2 | ❌ → ✅ (honest-empty Tags) |
| `GET /2015-03-31/functions/<n>/policy` | 2 | ❌ → ✅ (404 — no resource policy) |
| `GET /2017-10-31/functions/<n>/concurrency` | 2 | ❌ → ✅ (200 empty body) |
| `GET /2016-09-25/functions/<n>/event-invoke-config` | 2 | ❌ → ✅ (404 — no config) |
| `GET /2020-06-30/functions/<n>/code-signing-config` | 2 | ❌ → ✅ (200 empty body) |

## Fidelity fixes surfaced

The initial import crashed on `ListVersionsByFunction` (404). Phase 9.5 added all seven Read-path subresource handlers; each returns the source cloud's canonical-unset envelope for a feature that's out of the Phase 7 intersection.

## Remaining plan diff (BUG-13 family)

After all subresources return correctly, `terraform plan` still proposes a diff for three attributes:

```
~ memory_size = 0 -> 128   (provider default 128 not yet returned)
+ publish     = false      (Publish field not in response)
+ role        = "arn:..."  (Role not stored in domain)
```

These are documented in **BUG-13** as follow-on Phase 9 work. They don't block import; they cause a no-op `apply` to propose changes. Migration users could `terraform apply -refresh-only` to reconcile.

## Why this matters for the no-fakes rule

The pre-fix shim returned 404 to the importer's `ListVersionsByFunction` call. Strictly speaking that's also category-2 (the feature was out of intersection), but it crashed the provider mid-Read. The fix wasn't to fake the response — it was to return the source cloud's **actual** "this function has one $LATEST version with no published numbered versions" response, which is what real Lambda emits for an unpublished function. That's real work, not a fake.
