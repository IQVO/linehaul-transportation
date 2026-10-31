---
id: testing
title: Testing
sidebar_label: Testing
description: "The test pyramid of Linehaul Transportation: unit, godog, integration, contract, mutation and fitness tests, the make targets and the CI jobs."
---

# Testing

The quality gate is a set of `make` targets that mirror the jobs of `.github/workflows/ci.yml`, so everything CI does after a push can be run before it.
`make help` lists the targets. The tree has 96 `_test.go` files outside `web/` and `docs/`; 14 of them carry the `integration` build tag.

## The pyramid as it is

| Layer | Where | What it proves | Command |
| --- | --- | --- | --- |
| Unit | `internal/domain/*/*_test.go`, `internal/application/**` | Aggregate invariants (`departure`, `schedule`, `load`, `volume`, `network`, `shared`) and the use cases against the in-memory adapters | `make test` (`go test ./... -race`) |
| Handler and adapter | `internal/adapters/inbound/http`, `inbound/kafka`, `inbound/mcp`, `inbound/a2a`, `cmd/*/*_test.go` | Routing, problem types, the idempotency middleware, consumers with fake readers, MCP and A2A registries (the tool set is byte-pinned in a golden registry), boot configuration of each binary | `make test` |
| Acceptance (godog) | `features/*.feature`: `assignments` (13 scenarios), `booking` (8), `loads` (12), `pickups` (8), `schedules` (9): 5 files, 50 scenarios | Gherkin over the real HTTP API with in-memory repositories; steps in `features/steps_*_test.go`, runner `features/features_test.go` (`TestFeatures`) | `make bdd` |
| Integration | `*_integration_test.go` with `//go:build integration`: repositories, the relay, the idempotency middleware, the consumers and the real-wiring test `cmd/api/e2e_integration_test.go` | Real Postgres and Kafka started by testcontainers; no `DATABASE_URL`, no docker compose, no `t.Skip` gate | `make integration` (needs Docker) |
| Contract | `internal/adapters/outbound/repocontract` | One behavioural suite for every repository, run against the in-memory adapter (`memory_test.go`) and the Postgres adapter (`postgres_integration_test.go`) so the double cannot drift | part of `make test` and `make integration` |
| Coverage gate | `Makefile` `coverage` | 90 % statement coverage over `internal/domain/...` and `internal/application/...` | `make coverage` |
| Mutation | `.gremlins.yaml` | gremlins v0.6.0 over every `internal/domain/<pkg>`, one worker, timeout coefficient 30, efficacy and mutant-coverage thresholds both 90; the measured result in the file is 100 % killed for `shared`, `network`, `departure`, `schedule`, `load` and `volume` | `make mutation` (fast, per package), `make mutation-full` (exhaustive over `./internal/domain`) |
| Fitness | `internal/architecture/*_test.go` | The architecture as executable rules (below) | `make arch-test` |
| Vulnerabilities | `govulncheck` | Known CVEs in dependencies and the standard library | `make vuln` |
| Web | `web/` | `npm run lint`, `npx tsc -b`, `npm test` (vitest), `npm run build` for the `linehaul_mfe` console remote | CI job `web` |

## Fitness tests

`make arch-test` runs `go test ./internal/architecture/... -v`. The rules include:

- `architecture_test.go`: hexagonal dependencies (domain imports nothing from application or adapters; inbound and outbound adapters do not import each other).
- `runtime_no_memory_fitness_test.go`: `TestRuntimeBinaryHasNoInMemoryAdapters` (no `cmd/**` imports `adapters/outbound/memory`) and `repocontract_fitness_test.go` (no binary imports the repository contract), each with a "sensor bites" companion test that proves the rule fails on a bad fixture.
- `mcp_readonly_fitness_test.go` (`TestMCPServerIsReadOnly`), `a2a_fitness_test.go` (`TestA2AAdapterIsReadOnly`), `mcp_optional_inputs_test.go`: the agent surfaces hold only a read interface and optional tool inputs carry `omitempty`.
- `fitness_test.go`: `TestNoAuthMiddlewareReintroduced`, `TestKafkaConsumerGroupNeverHardcodedInline`, `TestNoEventEnvelopeToggleOrFlatEnvelope`, `TestKafkaIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationTestsUseTestcontainers`.
- `events_fitness_test.go` (`TestCloudEventsOnly`), `catalogue_fitness_test.go` (`TestEventCatalogueMatchesContract`: the published types equal `apis/asyncapi.yaml`).
- `kafka_writer_topic_fitness_test.go`, `kafka_writer_acks_fitness_test.go`, `kafka_dlq_shutdown_fitness_test.go`, `outbox_relay_cap_fitness_test.go`: writer topics and acks, dead-letter calls guarding cancellation, the attempt cap on every relay.
- `no_go_files_at_root_fitness_test.go`: no Go file at the repository root.

## Make targets

| Target | What it runs |
| --- | --- |
| `make build`, `make vet`, `make fmt`, `make fmt-check` | `go build ./...`, `go vet ./...`, `gofmt -w .`, `gofmt -l .` must be empty |
| `make lint` | `golangci-lint run --allow-parallel-runners ./...` (v2.14.0 pinned) |
| `make test` | `go test ./... -race` |
| `make coverage` | the CI coverage command and the 90 % gate over `./internal/domain/...,./internal/application/...` |
| `make bdd` | `go test ./... -run TestFeatures -v` |
| `make arch-test` | `go test ./internal/architecture/... -v` |
| `make integration` | `go test -tags=integration ./... -race -count=1` (Docker) |
| `make mutation`, `make mutation-full` | gremlins per domain package / over `./internal/domain` |
| `make vuln` | `govulncheck ./...` |
| `make check` | `fmt-check vet build lint test`: the pre-push gate (lefthook runs it on `git push`) |
| `make check-all` | `check` + `coverage` + `arch-test` + `bdd` |
| `make check-fast` | `fmt-check vet arch-test` plus the tests of the packages changed against HEAD (the agent Stop hook) |
| `make guide-lint`, `make harness-test` | lint the agent guides; unit-test the agent hooks |

Pre-commit (lefthook) runs `fmt-check`, `vet` and `lint`; install the hooks with `lefthook install`.

## CI jobs (`.github/workflows/ci.yml`)

The workflow runs on pushes and pull requests to `main` and `develop`, weekly (Monday 06:00 UTC) and on manual dispatch. "Push and pull request" below means those branches.

| Job | Runs | When |
| --- | --- | --- |
| `lint` | golangci-lint | push and pull request |
| `guide-lint` | `scripts/harness/guide_lint.py`, `test_hook.py` | push and pull request |
| `complexity` | gocyclo report (informational above 10, gate 15) | push and pull request |
| `test` | build, vet, `make coverage` (90 % gate, coverage artefact) | push and pull request |
| `bdd` | `go test ./... -run TestFeatures -v` | push and pull request |
| `integration` | `go test -tags=integration ./... -race -count=1` | push and pull request |
| `mutation-fast` | gremlins over every domain package | push and pull request |
| `mutation` | gremlins over `./internal/domain`, opens or closes a `harness:red` issue | weekly schedule and manual dispatch |
| `drift` | dead code, stale `go.mod` entries, unused frontend exports, coverage quality (advisory) | weekly schedule and manual dispatch |
| `api-lint` | Spectral on `apis/openapi.yaml` and `apis/asyncapi.yaml` | push and pull request |
| `vuln` | `govulncheck ./...` | push and pull request |
| `helm-lint` | `ct lint --charts charts/linehaul-transportation` and the selector test | pull request |
| `chart-tests` | `charts/linehaul-transportation/tests/render_test.sh`, `test_service_selectors.py` | push and pull request |
| `arch-test` | `go test ./internal/architecture/... -v` | push and pull request |
| `web` | `npm ci`, lint, `tsc -b`, vitest, build in `web/` | push and pull request |
| `docker-publish` | build and push the image to GHCR, cosign signature, SBOM | push to `main` |
| `release` | next version, retag the image, publish the chart, GitHub release | push to `main` |

The documentation site is built by its own workflow, `.github/workflows/docs.yml` (build on pull request, deploy on push to `main`).

## Writing tests here

- A new domain rule gets a unit test and must keep mutation efficacy above 90 %; a surviving mutant usually means a missing assertion, not a missing line.
- A new REST behaviour gets a scenario in the matching `features/*.feature` and, if it touches persistence, a case in the repository contract so both adapters are held to it.
- A new published event type must appear in `apis/asyncapi.yaml` or `TestEventCatalogueMatchesContract` fails.
- Integration tests must boot their own containers; a test that needs an environment variable or skips itself fails `TestPostgresIntegrationTestsUseTestcontainers`.
