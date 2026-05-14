---
id: ADR-0002
status: accepted
date: 2026-05-14
tags:
  - adr
  - onboarding
  - control-plane
  - router-agent
---

# ADR-0002 Panel-Owned Automated Router Onboarding

## Context

New customer routers currently require an operator or Codex session to sequence
import approval, hostname changes, subscription import, route baseline selection,
PassWall runtime repair, smoke checks, and final live-import approval. Recent
live onboardings showed the same failure classes recurring across devices:

- node ids change after subscription refresh, so route intent must be semantic;
- the selected `_shunt` node extras are the LuCI-visible route target source;
- low-overlay Cudy routers may need a minimal PassWall/Xray contour instead of
  the full stack;
- Xray can fail on missing geodata while dnsmasq can fail on missing nftset
  support;
- final readiness requires live runtime proof, not only a successful apply job.

Keeping this sequence in a human/Codex checklist makes every new router slower
and makes success depend on memory of previous incidents.

## Decision

Implement automated onboarding as a panel-owned state machine backed by durable
profiles/runs and existing router jobs. The panel should:

- attach an onboarding profile containing hostname, subscription secret, baseline
  type, runtime policy, and verification policy;
- advance the workflow on router register, check-in, job-result, and a small
  retry poller;
- use typed jobs and existing apply/import primitives before any terminal
  fallback;
- resolve fleet route targets by semantic intent and live health, not by stale
  node ids;
- stop on explicit safety gates instead of forcing repair;
- mark completion only after service/resource checks, route smoke, and final
  live-import approval.

The implementation workflow is documented in
`ai_docs/develop/features/router-automated-onboarding-workflow.md`.

## Consequences

- Operators can prepare a profile and let a new router converge without a Codex
  session.
- Router detail/fleet UI can show one progress lane instead of scattered import,
  apply, refresh, and terminal state.
- Controller work now includes typed verification and known runtime repair jobs;
  production enablement still requires a supervised pilot with the feature flag.
- The first release should be feature-flagged and limited to pilot/certified
  non-`hh` routers.
- Manual takeover remains necessary for unsupported boards, unknown runtime
  errors, low resources, or conflicting live imports.
