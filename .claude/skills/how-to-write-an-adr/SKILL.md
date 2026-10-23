---
name: how-to-write-an-adr
description: Write an Architecture Decision Record in this repo's numbering and format, including companion ADRs for cross-repo changes. Use when a design decision should be recorded or a change contradicts an existing ADR.
---

# How to write an ADR

Use when a change is architecturally significant — a new bounded-context
integration, a reversal of a prior decision, a cross-repo contract change,
or anything a future reader would otherwise have to reverse-engineer from
the diff. Not every change needs one: a bug fix or a routine feature
addition inside an already-decided architecture doesn't.

## Numbering and location

`docs/adr/NNNN-kebab-case-title.md`, four-digit zero-padded, sequential: check the highest existing number in `docs/adr/` and take the next
integer, never reuse or guess. Add a row to `docs/adr/index.md` in the same commit. The CloudEvents type catalogue ADR
(`0004-cloudevents-envelope-and-type-catalogue.md`) must change in the SAME commit as `apis/asyncapi.yaml` (`TestEventCatalogueMatchesContract`).

## Format

```markdown
# ADR NNNN: title (a short noun phrase)

## Status

Accepted (YYYY-MM-DD).   <!-- or Superseded by ADR MMMM -->

## Context

The forces: what the model or contract says, what is unclear, what constraint applies.

## Decision

Numbered, specific, naming files, types, variables and error names, so a reader can find each decision in the code.

## Consequences

What becomes easier, what becomes harder, and what future work this creates or forecloses. Be honest about the downsides: an ADR that
only lists benefits reads as marketing, not a decision record.
```

The `## Decision` section is the part worth the most editing effort: see ADR 0007 (`docs/adr/0007-kafka-consumers-inbox-and-dead-letters.md`)
for a model example. It states the exact mechanism, names the files and variables, and is specific enough that the how-to-add-an-integration-event
skill can point straight at it. Where the retail-network model is silent, record the reading as a numbered assumption in ADR 0002 (A31, ...)
and list it under `model_gaps`.

## Superseding an earlier ADR

Don't edit the old ADR's Decision section. Add a `## Status` line noting
`Superseded by ADR-XXXX` on the OLD one (a one-line patch), and open the
new ADR referencing it: `**Accepted.** <date>. Supersedes [NN. Old title](./NNNN-old-slug.md).`
— see ADR-0015 (`0015-remove-rest-identity-layer.md`) for the exact
wording pattern superseding ADR-0014.

## Cross-repo decisions: use a companion ADR, not one repo's private opinion

When a decision genuinely spans two bounded-context repos (e.g.
facility-layout's functional-location roles enabling wes-work-planning's
travel-graph feature), write ONE ADR per repo, each referencing the other
explicitly as "the companion ADR" with a one-line description of the
split of responsibility — see facility-layout's ADR-0016/0017 pair. Don't
write the decision once in one repo and expect the other repo's readers
to find it; each bounded context's docs site is read independently.

## After writing: verify

```bash
python3 scripts/harness/repo_lint.py && python3 scripts/harness/guide_lint.py
go test ./internal/architecture/ -count=1    # the catalogue sensor reads ADR 0004
```

Every path an ADR or guide cites must exist: the guide lint fails on a stale reference.
