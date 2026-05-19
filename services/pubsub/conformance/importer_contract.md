# Pub/Sub importer-read contract

> Phase 9 sub-phase 9.2 — captured from a `terraform import aws_sns_topic` run against the shim's AWS SNS frontend.

## aws_sns_topic import — observed wire ops

awsQuery / XML — single endpoint, action selected via the `Action` form field.

| Action | Category | Status | Notes |
|---|---|---|---|
| `GetTopicAttributes` | 1 | ✅ | Returns full attribute map. |
| `ListTagsForResource` | 2 | ✅ (empty Tags) | Domain has no tag storage yet (BUG-12 family). |

## Real fidelity fixes surfaced by this test

Phase 9.5's pubsub import surfaced **three real bugs** that the audit alone wouldn't have caught — exactly the kind of finding the user expected the no-fakes rule to enforce:

1. **Double-nested `<Attributes><Attributes>`** in the SNS `GetTopicAttributes` response. The xml.Marshal of the wrapper struct put both the wrapper's XMLName and the field name as elements. The SDK couldn't deserialize and the provider concluded the topic didn't exist. Fixed by flattening to a single `<Attributes>` element.

2. **Missing `Policy` + `EffectiveDeliveryPolicy` JSON envelopes**. The provider parses these as JSON; an empty string yields `unexpected end of JSON input` and import fails. Fixed by returning the source cloud's actual default policy + delivery policy (category 2 — feature unset → AWS's canonical default).

3. **Missing `ListTagsForResource` dispatch**. Same family as queue's BUG-12; same fix (return honest-empty Tags).

4. **`DisplayName` returning the topic name** instead of empty string. Real SNS returns `""` when DisplayName isn't explicitly set; the shim was echoing the topic name, which the Terraform provider then proposed as a diff. Fixed.

**Result:** `terraform import aws_sns_topic.imported arn:aws:sns:us-east-1:000000000000:shim-imported-topic` succeeds. `terraform plan -detailed-exitcode` returns 0 — no diff.

This is the audit-then-test pattern doing what the user asked for: every endpoint in the intersection now does real work or returns a real envelope. Fakes don't survive the import test.
