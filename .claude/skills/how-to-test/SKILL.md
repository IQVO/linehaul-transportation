---
name: how-to-test
description: Write or review tests and diagnose a failing coverage, mutation, bdd or integration CI job: the four test layers, the 90% gate, gremlins threshold semantics, the testcontainers rule. Use when adding tests, killing a surviving mutant, or fixing a red check.
---

# How to test

Use when writing or reviewing tests in this repo, or diagnosing a failing
`coverage`/`mutation-fast`/`bdd`/`integration` CI job. This fleet's quality
bar is layered — passing `go test` is necessary but is the WEAKEST signal
of the four; mutation testing exists specifically because green tests can
assert nothing.

## The layers, in order of what they prove

1. **Domain and use-case unit tests** (`go test ./...`): table-driven, one row per invariant and per legal and illegal transition
   (`internal/domain/*/*_test.go`, `internal/application/usecases/*_test.go`). Use cases run over the memory test doubles in
   `internal/adapters/outbound/memory`; never a real network or database call.
2. **Coverage** (`make coverage`, 90% gate over `./internal/domain/...` and `./internal/application/...`): proves lines ran, not that anything
   was asserted.
3. **Mutation** (`make mutation`, gremlins, one run per `internal/domain/<pkg>`): proves the tests assert. A survivor (LIVED) is a bug no test
   would catch.
4. **Behaviour** (`make bdd`, godog): `features/*.feature` with steps in `features/steps_*_test.go`; the runner is `features/features_test.go`
   (never at the repo root). The scenarios drive the use cases and name refusals by problem slug.
5. **Adapter tests**: the HTTP adapter with `httptest` (`internal/adapters/inbound/http/handlers_test.go`), the encoders with golden exact JSON
   (`internal/adapters/kafka/wire/wire_test.go`), the consumers' handlers and run loop (`internal/adapters/inbound/kafka/*_test.go`), the relay
   over a fake store (`internal/adapters/outbound/outbox/relay_test.go`).
6. **Integration** (`make integration`, build tag `integration`, needs Docker): the same repository suite
   (`internal/adapters/outbound/repocontract`) on Postgres, the relay against Postgres + Kafka, the Idempotency middleware, the consumers, and
   the real-wiring test `cmd/api/e2e_integration_test.go`.

## Mutation testing: `<=` fails, not `>=`

`.gremlins.yaml` sets `efficacy-threshold`/`mutant-coverage-threshold` as
a floor gremlins fails on if the MEASURED score is `<=` the threshold —
read the comment at the top of this repo's `.gremlins.yaml` for the exact
current values and when they were last re-baselined. When you deliberately
lower coverage of a package (rare, but happens when removing dead code),
you may need to lower the threshold in the SAME PR with a dated comment
explaining why — never silently; a future reader needs to know the drop
was intentional, not a regression that slipped through.

## Three real pitfalls that have each cost a real CI failure in this fleet

### 1. Zero/origin-value fixtures hide arithmetic mutants

A test built around zero-valued operands (e.g. a point at the origin
`(0,0,0)`) makes `a - b` and `a + b` produce the same result, so a mutant
flipping `-` to `+` survives even though coverage looks complete. Any new
value object with real arithmetic needs fixture values where EVERY
operand and every per-axis/per-field delta is distinct and non-zero, and
the test must assert the exact expected value, not just "no error".

### 2. Boundary guards need the boundary value itself

A test for `if x < 0 { return err }` that only tries `-1` (clearly
invalid) and `42` (clearly valid) never exercises `0` — so a
`CONDITIONALS_BOUNDARY` mutant rewriting `<` to `<=` survives silently.
Every `< 0`/`> 0`/`<= 0` guard needs an explicit test for the boundary
value itself (e.g. the guard's exact threshold must succeed, not error,
if that's the intended behavior at the boundary).

### 3. Tie-break / near-equivalent mutants: know when NOT to chase them

A shortest-path relaxation (`if candidate < dist[node]`) or a priority
queue's `Less` has a `<` -> `<=` mutant that is undetectable by ANY test
whose edge weights are all distinct — the mutation only diverges on an
exact tie. Do NOT force an artificial tied-weight fixture just to kill
this; that pins an arbitrary, currently-unspecified tie-break order as if
it were a real invariant, which is worse than an accepted near-equivalent
survivor. Document it in the repo's `MUTATION.md` triage section instead,
and move on. The same applies to a boundary guard whose boundary is
structurally unreachable (e.g. `len(x) >= 1` always holds, so the `< 0`
side of a derived guard can never fire) — add a test for the reachable
edge case, but don't chase the mutant on the unreachable side.

## Diagnosing a `mutation-fast` CI failure: diff against develop, don't chase every LIVED line

```bash
gremlins unleash ./internal/domain          # on your branch
git stash && git checkout origin/develop -- . && gremlins unleash ./internal/domain   # baseline
```

Only entries NEW on your branch are your regression. Most repos in this
fleet already carry a small permanent baseline of accepted survivors
(documented in `MUTATION.md`) — confirming the survivor SET is unchanged
from `origin/develop`, not just that the percentage cleared the
`.gremlins.yaml` gate, is the real proof a fix didn't just get lucky on
the threshold.

## Kafka/Postgres integration tests: testcontainers, never a skip-gate

A `-tags=integration` test touching Kafka or Postgres MUST start its own
container via `testcontainers-go` (`modules/postgres` / `modules/kafka`).
Never gate on `os.Getenv("KAFKA_BROKERS")` or `os.Getenv("DATABASE_URL")`
(or `ANALYTICS_DATABASE_URL`) + `t.Skip(...)`, and never hardcode
`localhost:9092`. This fleet's CI `integration` job provisions NO service
containers (no Postgres, no Kafka; `make integration` needs only Docker) — a
skip-gated test silently skips in CI and proves nothing there, while
testcontainers actually exercises the assertions on the runner. A fitness
test per technology enforces it (`TestKafkaIntegrationTestsUseTestcontainers`,
`TestPostgresIntegrationTestsUseTestcontainers`). Share one container per
package through a helper (e.g. an `outboxDB(t)` that runs
`tcpostgres.Run` -> `ConnectionString` -> migrate -> pool -> `t.Cleanup`). See
`internal/adapters/inbound/kafka/consumers_integration_test.go`
for the working recipe (unique topic per test, one shared container per
package, explicit `CreateTopics` + poll for the partition leader before
the first read/write).

## Verify before opening the PR

```bash
make check-all   # check + coverage + arch-test + bdd (the full local gate)
```

If `check-all` doesn't include `mutation-fast`/`vuln` locally, run them
explicitly too — CI runs them even when the local gate doesn't, so a PR
can pass your local check and still go red in CI otherwise.
