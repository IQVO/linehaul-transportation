---
name: architecture-review
description: Bounded-context boundary and ADR-compliance review of a change (expensive, post-integration): hexagonal direction, cross-context coupling, contradicted ADRs. Invoke explicitly: /architecture-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---


Perform a bounded-context boundary and ADR-compliance review of the
current changes (or `$ARGUMENTS` if given, e.g. a branch/PR diff range).

This is EXPENSIVE relative to `/code-review` — it reasons about
cross-repo/cross-context implications, not just this diff's local
correctness. Use it post-integration (before merging a PR that touches
architecture, not on every small commit) or when asked explicitly to
check a design decision against this fleet's standing architecture.

## What to check, in priority order

1. **Hexagonal dependency direction.** Domain depends on nothing;
   application depends on domain+ports; adapters depend on
   application+domain; only `cmd/` wires every layer together. Run
   `go test ./internal/architecture/... -v` first — if it's already red,
   report that and stop; don't hand-review what a fitness test already
   caught.
2. **Context boundary.** linehaul-transportation consumes exactly the thirteen types of ADR 0003 (node-topology, order-sourcing,
   shipping-and-tracking) and calls no sibling (model `sync_calls: []`). Any other consumer or outbound call is a new architectural
   decision that needs its own ADR in `docs/adr/` (superseding or extending ADR 0003), not a code change slipped in silently. Sibling
   data is never read live; it arrives as events into the local read models (`NodeView`, `LaneView`, `ExpectedVolume`).
3. **Runtime rule.** `cmd/**` must not import `internal/adapters/outbound/memory` or `repocontract`
   (`TestRuntimeBinaryHasNoInMemoryAdapters`); the relay is built with `WithMaxAttempts` (`TestOutboxRelaysPassAttemptCap`); `/readyz`
   keeps its empty-topic Kafka metadata check. This repo has no MCP adapter and no analytics read side.
4. **Consumers.** One consumer per producer topic under its own mandatory `*_CONSUMER_GROUP` variable, inbox-claimed in the same unit of
   work as the effect, dead-lettered to `<topic>.dlq` (ADR 0007).
5. **Kafka consumer-group pattern correctness.** Every consumer here is an event-fed local copy that must see each message once per group:
   its group id is a stable, service-prefixed value from a mandatory variable, never a literal and never derived from a pod name. Flag any
   new consumer whose pattern doesn't match its actual behaviour (see `.claude/rules/fleet/kafka-testing-and-consumers.md` and
   `.claude/skills/how-to-add-an-integration-event/SKILL.md`).
6. **A new bounded-context integration with no companion documentation.**
   If this change introduces or changes a cross-repo contract (a new
   REST call, a new Kafka topic subscription, a new MCP tool consumed by
   a sibling), check whether an ADR documents the decision — and whether
   a companion ADR should exist in the OTHER repo too, per this fleet's
   companion-ADR convention (see `.claude/skills/how-to-write-an-adr/SKILL.md`).
7. **Auth-reintroduction and sibling-call bans**, same as `/code-review`
   items 6-8, but reasoned about more thoroughly here — check not just
   "is there a Bearer literal" but "does this change's INTENT require
   re-litigating the fleet-wide auth-removal decision," which would need
   its own ADR, not a silent code change.

## Output format

State clearly: PASS (no architectural concerns), CONCERNS (list them,
each tied to the specific rule/ADR it would violate), or NEEDS-ADR (the
change is architecturally sound but undocumented — name what the ADR
should cover). Cite the specific file/rule/ADR for every finding; a
finding with no citation is not actionable.

This is advisory. It never blocks a merge on its own, and it never
modifies files. If a finding conflicts with a decision explicitly stated
in this repo's own AGENTS.md/CLAUDE.md, defer to that document and say
so — this command reasons about the fleet's general conventions, not a
higher authority than the repo's own explicit guidance.
