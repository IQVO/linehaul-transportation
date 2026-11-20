# ADR 0015: every problem type has a replay class; a retryable refusal is not stored under its Idempotency-Key

## Status

Accepted (2026-11-20). Supersedes the sentence "a 4xx rolls them back to the savepoint but is stored (a repeat answers the same)" of
ADR 0006 point 5 and its Consequences ("A 4xx is replayed too"), the matching "a repeat of the same request replays the first
response" of the `Idempotency-Key` text of `apis/openapi.yaml` (the Decision text of those records is not rewritten: each carries
a dated pointer here). Adopts the fleet rule decided by the owner on 2026-11-18 (`.claude/rules/fleet/idempotency-and-outbox.md`,
`docs/idempotency-replay-classes.md` of warehouse-harness-template). No route, status code, problem slug, header, event or table
changes: a refusal that was replayed for 24 hours is, for seven of the 14 problem types, now answered again by running the request.
ADR 0013 is not used by this repository.

## Context

`RequireIdempotencyKey` (`internal/adapters/inbound/http/idempotency.go`) claims the key row inside the request transaction
(`claim`: purge rows older than 24 hours, then `INSERT ... ON CONFLICT (key) DO NOTHING`), runs the handler in a savepoint of that
transaction bound into the context (`TxBinder`, `pgtx.With`) and, until now, stored EVERY answer below 500: a 2xx with its writes,
a 4xx with the writes rolled back (`settle`), so the same key and body replayed the refusal for 24 hours. It guards the eight POST
routes registered through `Server.keyed` (`server.go`); `PUT /departures/{departureId}/capacity` is idempotent by method and
unkeyed (`r.Put`, ADR 0012), so its refusals never reach the capture. The use cases run in one `UnitOfWork.Do`, which JOINS the
transaction the middleware bound (`unit_of_work.go` 36-39): the middleware, not the use case, decides what commits.

That is wrong for a refusal the same request could pass a moment later: `409 concurrent-update` (a lost optimistic lock; the REST
path does not retry it, `MaxConflictRetries` is for consumers only, `service.go` 20 and 76-91), `404 not-found` for a lane the
node-topology consumer has not delivered yet, `409 package-already-loaded` while the other load is still OPEN or CLOSED. The fleet
decision: a retried request is answered from the store only when its first answer is final for that request. Each problem type carries
exactly one class, declared on its catalogue entry; the middleware reads the class and holds no list of slugs and no status range
beyond 2xx and 5xx.

## Decision

1. **The class is a field of the catalogue entry.** `problemCatalogue` (`internal/adapters/inbound/http/errors.go`) was a slice of
   `{err, p}`; it is now `{err, p, replay}`, `replay` being `replayFinal`, `replayOutcome` or `replayRetryable`
   (`replay_class.go`). The slice, its match order and every slug, title and status are unchanged. The zero value is
   `replayUnset`: there is no runtime default class. An entry that declares none, a 5xx entry that is not RETRYABLE or one slug
   declared with two classes fails `TestEveryProblemTypeDeclaresAReplayClass`; this ADR's table is held equal to the catalogue by
   `TestTheADRDeclaresTheClassOfEveryProblem` and the RETRYABLE list of the OpenAPI text by
   `TestTheSpecListsTheRetryableProblemTypes`. The adapter-driven fleet test (`idempotency_replay_classes_integration_test.go`, the
   harness template rendered for `/loads`) walks the catalogue on a real Postgres.
2. **The error writer marks the capture.** `writeError` (`response.go`) tells the middleware's capturing writer the class of the
   entry it answers with (`replayMarker`, `capture.MarkReplay`). A handler that answers without a catalogue problem leaves the class
   unset.
3. **The middleware acts on the verdict, nothing else** (`capture.verdict`, `replay_class.go`):

   | answer | verdict |
   | --- | --- |
   | 2xx | stored, the handler's writes commit with the key row and the stored answer (unchanged) |
   | 5xx and a panic | the whole transaction rolls back, key row included, nothing stored (unchanged) |
   | FINAL | the savepoint rolls back, the answer is stored and replayed (unchanged) |
   | OUTCOME | the savepoint COMMITS, the answer is stored in the same transaction (new, no entry uses it) |
   | RETRYABLE | released exactly as the 5xx branch: `tx.Rollback`, key row included, nothing stored (new) |
   | no class (anything else) | released like RETRYABLE: nothing may be stored without a class |

   The released answer is flushed to the client unchanged, after the rollback, and never carries `Idempotency-Replayed`. A 2xx, a 5xx
   and a FINAL refusal are byte-identical to before. The capture type moved from `idempotency.go` to `replay_class.go` (the file
   was at the 300-line limit); its code is unchanged.
4. **Releasing a RETRYABLE key means the request is forgotten.** The same body under the same key runs again; a different body under
   the same key is a NEW request, not `idempotency-key-reused`: nothing is left to compare against. The 24-hour purge of `claim` rolls
   back with the released transaction and runs again on the next keyed write.
5. **A concurrent duplicate while the first ends RETRYABLE.** The copy blocks on the first's `INSERT ... ON CONFLICT` (Postgres
   serialises on the primary-key unique index until the first transaction resolves). The first rolls back, the row is gone, the
   blocked insert succeeds: the copy is then a FRESH request and runs the use case itself. It never replays a released refusal and
   never runs alongside the first. Copies that were waiting behind it replay the answer of that second run
   (`TestACopyWaitingOnARetryableFirstRunsTheUseCaseItself`: 6 copies, 1 refusal, 1 fresh 200, 4 replayed 200s, two saves, never at
   once).
6. **No OUTCOME problem type exists in this service, and none can.** OUTCOME needs a refusal that COMMITTED state. Under this
   middleware a 4xx never commits anything but the key row: `settle` rolls the savepoint back for every refusal that is not OUTCOME
   and `releaseKey` rolls the whole transaction back, and `UnitOfWork.Do` only joins the middleware's transaction (`unit_of_work.go`
   37-39), so even a use case that saves before it refuses has its writes dropped: `CancelDeparture` saves the cancelled departure
   (`booking.go` 125-150, `b.save`) and may then refuse while moving bookings (`moveBookingsOff` 153-174), `withLoad` saves the load
   (`loads.go` 34) before `Departures.Save` 39 can lose the lock. Every other refusal comes before the first write:
   `DefineLaneSchedule` (`schedules.go` 45, 52-54, 63-65 before `Save` 67), `RetireLaneSchedule` (77-84 before 85), `OpenLoad`
   (`loads.go` 53-64 before `Save` 66), `AddPackage` (`loads.go` 75-83 before the saves of `withLoad`). The OUTCOME verdict is
   nevertheless implemented and proven against Postgres (`TestAnOutcomeRefusalCommitsItsWritesWithItsStoredAnswer`, the capture
   marked by hand), so the first refusal that does commit gets its writes kept instead of silently rolled back; its entry needs the
   cited lines of its use case.
7. **MCP and A2A write nothing** (ADR 0008 and 0011: read-only tools and skills, `.claude/rules/fleet/a2a.md`): no store of their
   own, nothing to classify. There is no outbound deterministic-key caller in this service (it calls no sibling at runtime,
   CLAUDE.md).
8. **Retention** is enforced as the rule asks: `claim` purges every row older than 24 hours on each keyed write
   (`TestKeysExpireAfter24HoursAndAreSweptOnTheNextKeyedWrite`).
9. **Writers that bypass `writeError`** (grep of `problem+json` and `WriteHeader` over `internal/` and `cmd/`): `keyFrom`
   (`idempotency-key-required`, `idempotency-key-invalid`), `replay` (`idempotency-key-reused`) and `rejectNUL` (`nul.go`,
   `resource-not-found` for a NUL in the path) call `writeProblem` BEFORE the handler or instead of it, on the real writer, never on
   the capture; `readiness.go` (`dependency-unavailable`, 503) and `cmd/mcp`, `cmd/a2a` are not keyed. None of them can be an OUTCOME
   and none is a catalogue entry.

### The classes

The question per entry: did the command commit state before refusing (OUTCOME)? Is the answer a function of the request alone
(FINAL)? Otherwise RETRYABLE. When unsure, RETRYABLE: the refusal rolled everything back, so running it again costs one transaction
and cannot pin a false answer for 24 hours. The status and the slug do not decide the class. Facts used below, read from the code
(`origin/develop` at c38171d): no statement deletes a departure, a lane schedule or a load (the only DELETEs are the key purge, the
dedupe of migration 0002 and `load_packages`, which `LoadRepo.Save` rewrites from the aggregate, `load_repository.go` 105); a load's
`capacity` is set once at `Open` (`load.go` 93-108) and its package list only grows (`AddPackage` 138 appends, nothing removes);
`cutoffAt` is set once at `Schedule`; the states of a departure, a load and a schedule only move forward.

| Problem type | Class | Status | Reason (code of `origin/develop` at c38171d) |
| --- | --- | --- | --- |
| `malformed-request` | FINAL | 400 | `decodeBody` (`response.go` 35-52), `errMalformedRequest` and the NUL errors (`nulError.Is`, `nul.go`; answered by `rejectNUL` before the middleware, listed because they go through the same entry): the body is not valid JSON for the operation; a function of the request |
| `request-body-too-large` | FINAL | 413 | `errTooLarge`: the middleware answers it itself before claiming the key (`idempotency.go` `ServeHTTP`), the handler's copy is unreachable; a function of the request |
| `invalid-departure-times` | FINAL | 422 | `Slot.validate` (`schedule.go` 59-73, line 71): the cutoff of a slot is after its departure, both in the request. `departure.Schedule` 74 is only reached by the horizon roll (`generate.go` 136), not by a keyed route |
| `overlapping-slots` | FINAL | 422 | `checkOverlap` (`schedule.go` 91-105) compares the slots OF THE REQUEST with each other and reads no stored schedule: a function of the request alone (J2) |
| `validation-failed` | FINAL | 422 | every source on a keyed route is a field of the request: `toSlots` (`schedules.go` 33), `Slot.validate` and `validateSlots` (`schedule.go` 59-86), `schedule.Define` 127, `load.AddPackage` 124 (tracking number), `Departure.Cancel` 202 (reason), `load.Open` 95 (a loadId this service just minted) |
| `load-over-capacity` | FINAL | 409 | `Load.AddPackage` (`load.go` 135-136): `len(packages)+1 > capacity` with the capacity snapshot of `Open` and a package list that only grows; a package already on the load is a no-op (129-131), and once the load leaves OPEN the answer is `illegal-transition` 127, checked first. Nothing makes the same request pass later (J1, `TestAFullLoadStaysFullForANewTrackingNumber`) |
| `load-already-exists` | FINAL | 409 | `OpenLoad` (`loads.go` 57-61, `load.Open` `load.go` 100-101) and the unique constraint `loads_departure_id_key` (`0001` line 58, `load_repository.go` 128-129): a departure has at most one load and a load is never deleted, so once it holds it holds (J3) |
| `not-found` | RETRYABLE | 404 | the lane (`Lanes.Get`, `schedules.go` 52), the lane's schedule (`Schedules.Get` 77), the departure (`Departures.Get`, `departures.go` 100, `loads.go` 24 and 53) or the load (`Loads.Get` `loads.go` 20) of the request. Departures and loads are minted here and never deleted, but the lane is a node-topology fact delivered by a Kafka consumer into `lane_views` and a schedule is created by another request, and the slug is shared: when unsure RETRYABLE (J4) |
| `illegal-transition` | RETRYABLE | 409 | the aggregate is not in the state the command needs: `Load.Dispatch` (`load.go` 157) needs CLOSED and `Receive` 171 needs DEPARTED, states another request reaches; `Open`, `AddPackage`, `Close`, `Departure.Cancel` 197 and `LaneSchedule.Redefine`/`Retire` (`schedule.go` 138, 151) are terminal-state refusals sharing the slug. One slug, the state-dependent source decides (J5) |
| `concurrent-update` | RETRYABLE | 409 | the optimistic lock lost the race (`concurrent`, `helpers.go` 57-59, in `departure_repository.go` 138/147, `load_repository.go` 134/141, `schedule_repository.go` 98/105); the REST path does not retry it (`service.go` 76-91 is the consumer loop), the next attempt reads the new version and passes |
| `departure-full` | RETRYABLE | 409 | `Departure.Book` (`departure.go` 116-117), reached by `AddPackage` on a LINEHAUL or AIR load (`loads.go` 85-89) and by the rebook of `CancelDeparture` (`booking.go` 114); the capacity is raised by `PUT /departures/{id}/capacity` (`ChangeCapacity` 150-157) and bookings are released by cancellations |
| `departure-closed` | RETRYABLE | 409 | `Departure.Book` 113-114 mixes two sources: the clock past `cutoffAt` on the departure of the request (irreversible, `AddPackage`) and, in `CancelDeparture`, the DEPARTURE PICKED BY `next` for a rebook (`booking.go` 104-117), which another request can cancel or close between the list and the read. One slug, one class: the second source can pass on a retry (J6) |
| `package-already-loaded` | RETRYABLE | 409 | `Load.AddPackage` 132-133 over `FindActiveByTracking` (`loads.go` 75) and the partial unique index `load_packages_active_tracking_key ... WHERE active` (`0001` line 72; `active` is OPEN or CLOSED, `load.go` 184, `load_repository.go` 110): dispatching the other load frees the tracking number, so the same request passes afterwards (J7, `TestPackageAlreadyLoadedIsReleasedWhenTheOtherLoadDeparts`) |
| `lane-closed` | RETRYABLE | 409 | `Departure.Schedule` 77-78 over the lane view's `Open` (`generate.go` 136): a lane reopens. No keyed route reaches it today (only the horizon roll does); it is classed because the catalogue maps it and the writer would mark it (J8) |

Problems the capture never holds (not catalogue entries, never stored): `idempotency-key-required` and `idempotency-key-invalid`
(400) and `idempotency-key-reused` (409) are written by the middleware itself, before or instead of the handler (`keyFrom`,
`replay`); `resource-not-found` (404, a NUL in the path) by `rejectNUL`; `internal-error` (500) and `dependency-unavailable` (503,
`/readyz`) are 5xx; `invalid-limit`, `invalid-cursor` and `unknown-query-parameter` (`problemError`, `departures_list.go`) belong to
the unkeyed `GET /departures`. 14 distinct slugs in 14 catalogue entries: 7 FINAL, 7 RETRYABLE, 0 OUTCOME.

### Judgement calls

- **J1 `load-over-capacity` is FINAL, against the first guess "capacity frees up".** The capacity is the snapshot of `OpenLoad`
  (`load.go` 105, `Capacity()` has no setter; `ChangeDepartureCapacity` changes the departure, not its load), the package list only
  grows and a load that leaves OPEN answers `illegal-transition` before the capacity is looked at. So the refusal of a NEW tracking
  number never ends. This is the most consequential FINAL: it rests on three facts that `load_over_capacity_test.go` pins; a change
  that breaks one (a remove-package command, a capacity edit on a load) must reclassify it.
- **J2 `overlapping-slots` is FINAL, against the first guess "depends on the schedule other requests edit".** `validateSlots` is
  called with the request's slots only (`schedule.go` 79 and 138, 127) and `checkOverlap` reads nothing stored; a schedule is
  replaced, never merged.
- **J3 `load-already-exists` is FINAL.** A load is never deleted and a departure's load never changes departure; the unique constraint
  is total (not partial). The only way the same `POST /loads` passes later is a different departure id, which is another request.
- **J4 `not-found` is RETRYABLE, not FINAL.** The owner's rule allows FINAL for a 404 on the command's own target when the ids are
  minted here and nothing is deleted. Both hold for loads and departures, but `DefineLaneSchedule` names a lane delivered by
  node-topology events (a referenced fact "not known yet", `lane_views`), `RetireLaneSchedule` names a schedule another request
  creates, and the slug is one: when unsure RETRYABLE. The cost is one extra transaction per retry of a mistyped id.
- **J5 `illegal-transition` is RETRYABLE.** The slug mixes terminal refusals (retire an already retired schedule) with
  not-yet-reached states (dispatch a load that another request has not closed yet). Splitting it into two problem types is a
  follow-up, not part of this PR.
- **J6 `departure-closed` is RETRYABLE** although its `AddPackage` source is irreversible (the clock only advances and `cutoffAt` is
  immutable): the `CancelDeparture` rebook source names a different departure that can change between two reads, and a slug that
  mixes request-only and state-dependent causes is RETRYABLE.
- **J7 `package-already-loaded` is RETRYABLE, against the first guess "terminal or already done".** The unique index is partial on
  `active` (OPEN or CLOSED): dispatching the other load frees the package.
- **J8 `lane-closed` is RETRYABLE although no keyed route can answer it today.** A refusal with no class would fail the unit test;
  the lane reopens by definition.
- **J9 No problem type is OUTCOME**, see decision 6.

## Consequences

- A retry under the same key after any of the seven RETRYABLE refusals runs the request again instead of replaying a refusal; the
  success is then stored. A different body under that key is a new request. A client that sent a deliberately reused key after one of
  them gets its retry executed, not the old refusal. The OpenAPI text lists the seven.
- A released refusal costs one rolled-back transaction per retry, and the refused request writes no key row, so the key table no
  longer grows with them.
- No caller of this service retries automatically and this PR adds no client-side or consumer-side retry: a person who gets a
  RETRYABLE refusal still sees it. The console (ADR 0009) is read-only. Releasing the key only lets whoever sent it send it again.
- A new problem type is classified by the PR that adds it: the unit test is red otherwise, and the table above, the OpenAPI
  RETRYABLE sentence and the catalogue move together.
- Known limits, read from the code and not changed here: (1) `lane-closed` has no keyed route and `departure-full` from
  `PUT /departures/{id}/capacity` is unkeyed; the classes still apply to every route the writer serves. (2) A unique violation
  inside the request transaction (`load-already-exists` and `package-already-loaded` raised by `Loads.Save`) aborts the
  savepoint; the typed refusals are answered correctly and rolled back, and no retry runs on the aborted unit.
  (3) `CLAUDE.md` and the REST rule said "all nine state-changing routes"; there are eight keyed POSTs and one unkeyed PUT (the REST
  rule is corrected here, `CLAUDE.md` is not edited).
