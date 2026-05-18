# shimanism — What We Did

Status [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md).

> Reverse chronological. One section per phase. The *why*, the surprises, the root causes — not per-PR detail. For commit-level history, `git log`. For per-bug detail, [BUGS.md](BUGS.md).

## Pre-phase — Repo bootstrap and philosophy (PR #1, merged 2026-05-18)

### Why this came first

Before any code, we wanted the project's premise written down in a form that survives team handoffs and agent compactions. The philosophy doc is what tells a fresh agent *what we will and will not build* without re-deriving it from the README's prose. The README is the plain version; PHILOSOPHY.md is the literary one (koans + Bierce-style terminology) and is the artifact agents are expected to internalize.

### What landed

- **Repo `e6qu/shimanism` created** on GitHub as a public user-owned repo, AGPL-3.0, with:
  - Branch ruleset on `main`: linear history required, no force-push, no deletion, PR required before merge, allowed merge methods restricted to **squash + rebase** (no merge commits), `delete_branch_on_merge` enabled, "Update branch" enabled.
  - Repo admin (`e6qu`) listed as bypass actor so the user retains escape-hatch for emergencies (mirrors sockerless's `enforce_admins=false`).
  - Issues + Projects enabled; Wiki + Discussions disabled.
- **PHILOSOPHY.md** went through several rounds of revision in one PR:
  - Initial: structured doc with headings (What is a shim / What it is not / What we shim / Why this works).
  - Rewritten as 7 koans in the style of the AI/Hacker Koans (Sussman, Knight, Master Foo).
  - Sharpened to be less explicit (lesson never stated; masters strike / dismiss / act rather than explain).
  - Added a recurring "blind master" figure as a vibe-coder/AI-paired allegory (three koans: Blind Master / Vibe / Slop).
  - Added "The Saddle" koan for the part-as-handle principle (slice → pizza, saddle → horse, nose-ring → ox).
  - Codex-CLI editorial review (two passes): suggested drops (rejected — user wanted vibe/slop content); suggested the K8s peer was missing from "The Circle" metaphor (accepted: now four circles, "three lords and one village"); suggested a new "The Signpost" koan for the endpoint-override beat (accepted, lightly polished); flagged README internal contradiction (IAM listed under managed-RDBMS control plane while also being a non-goal — fixed to "access bindings where required by the managed-service API").
  - Final tightening pass collapsed master's explanatory speech to single-word cryptic replies ("Tax." / "Door." / "Cartilage." / "Guests." / "Next." / "One thing." / "Also."). Net: ~117 lines removed from koan section while preserving all 12 koans.
- **README.md** rewritten from placeholder to a straightforward Goals / Non-goals / Mechanism / MVP-service-matrix document. Eight planned services with AWS / GCP / Azure / Kubernetes peer columns. Vibe/Slop koans consolidated into one koan ("Slop") to address codex's length concern while honoring the user's explicit ask to include both themes.

### Surprises / things worth remembering

- **The user wants the koan content to survive multiple aesthetic constraints simultaneously** (funny, cryptic, absurd, bodily-comic, metaphorically encoding a real philosophy beat, not too long). The successful template ended up being: master acts more than speaks; punchlines are one-word; bodily-comedy is slapstick not sadistic; each koan must map to a stated philosophy beat.
- **Codex CLI is a useful editorial second-opinion** but applies its judgment narrowly — it doesn't see the user's prior conversation. Its suggestions to drop the Vibe and Slop koans were technically reasonable on grounds of philosophy-mapping, but ignored that the user had explicitly asked for those themes.
- **The shimanism philosophy converged on**: shim = protocol-translation proxy, not emulator and not neutral SDK. Front door is the cloud's own API; back door is a real comparable service somewhere else (cloud / K8s operator / self-hosted); nothing in between. Existing SDKs / CLIs / Terraform providers point at the shim via endpoint-override. Intersection-only scope. K8s as a first-class fourth backend.
- **The conformance approach is locked in early**: every shimmed operation must be exercised in the same commit by the cloud's official SDK + CLI + Terraform provider against every backend in scope. This is what makes "never lie" enforceable in CI rather than aspirational.
