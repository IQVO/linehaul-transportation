# ADR 0009: the console remote (linehaul_mfe) — a read-only screen over the existing REST

## Status

Accepted (2026-10-24).

## Context

The warehouse-console shell mounts per-context Module Federation remotes, and
operators walk each context's data through it. linehaul-transportation had no
remote: its only user-facing surface was the raw REST API. The context's REST
reads are exactly two (`GET /departures`, `GET /departures/{departureId}`), so
a remote here is small by construction — this ADR records why it exists and
the contract it must keep, per the fleet's remote recipe
(task-interleaving's web/ is the reference implementation).

## Decision

### The remote

`web/` is a Vite + React Module Federation remote, container `linehaul_mfe`,
exposing `./App` only (no props, relative routes), built on the fleet's
reference recipe rather than re-derived:

- PINNED host/remote contract: federation container `linehaul_mfe`, exposed
  module `./App`, gateway mount `/mfes/linehaul-transportation/`, dev/preview
  port **4015** — off the console's 5181–5197 ladder (every slot held) and
  clear of 4010/4011 (held by other remotes). `vite.config.ts` stays a STATIC
  object (never the callback form: a callback cannot be mergeConfig'd, which
  has killed test suites elsewhere in the fleet).
- The API base is resolved at RUNTIME from the console's `/config.json`
  (`window.__WAREHOUSE_CONFIG__.apiOrigin` →
  `${apiOrigin}/api/linehaul-transportation`); a production build throws when
  it is missing instead of falling back to a dev port. The remote calls ONLY
  its own context's API — never a sibling context, never the MCP server.
- Packaged as `nginxinc/nginx-unprivileged` (`web/Dockerfile`,
  `web/nginx.conf`), served under `/mfes/linehaul-transportation/` by
  warehouse-infra's Nginx web gateway. Kong never serves these bytes; no
  ingress/httproute; the chart Service stays ClusterIP.
- `@warehouse/ui-kit` is a sibling checkout consumed as a named BuildKit
  context (`uikit`); the image never installs from a foreign `node_modules`.

### The screens — existing REST only, read-only, no new endpoints

Two surfaces, exactly what `apis/openapi.yaml` serves today, and reads only:

1. **Departure horizon** — `GET /departures` with the origin, destination,
   cutoff-window (`from`/`to`, RFC 3339) and `limit` filters the API already
   takes; one row per departure with kind, lane, leg, cutoff, booked/remaining
   capacity and state.
2. **Departure detail** — `GET /departures/{departureId}`: the plan (lane,
   kind, leg), cutoff/departs/arrives, capacity sentence, state, and the
   cancel note (reason and instant) once a departure was cancelled.

Where a screen wanted data the API does not serve, the screen was cut, not the
API: there is no lane-schedule or load GET, so neither has a screen. Every
write (define/retire a lane schedule, cancel or re-capacitate a departure, the
load lifecycle) stays an API/MCP-free REST operation performed elsewhere —
this remote has no write action at all.

### The deployment surface

The Helm chart grows a `frontend` component (`frontend-deployment.yaml`,
`frontend-service.yaml`, `_frontend.tpl`), default **off**; the Service
selects `component=frontend` so no Service ever selects two Deployments, and
the frontend Deployment renders NO env — nothing about the remote is
configured in the cluster. CI grows a `web` job (checkout self + warehouse-ui-kit
sibling, then oxlint, `tsc -b`, vitest, production build).

## Consequences

- Operators read the horizon in the console without `curl`; the deep link
  `departures/:departureId` is shareable.
- The pinned contract (container name, mount path, port 4015) must change with
  warehouse-console, never alone.
- A future write action needs an ADR of its own — and a POST with a fresh
  `Idempotency-Key`, which the remote today cannot even send.
