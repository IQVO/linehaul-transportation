# ADR 0013: direct-lane advisory

## Status

Proposed (2026-11-09). Blocked on a missing input: the destination node of a labelled package is not in any data this service consumes
(see "Premise check"). Nothing beyond this record is implemented.

## Context

Public operators report that more direct facility-to-station lanes cut the number of touches a package gets. The planner needs a signal
where volume justifies a truckload lane between a fulfillment center and a delivery station that have no lane between them. The signal is
advisory only, like the placement advisory: this service decides nothing, opens no lane and changes no departure.

The intended design attributes each labelled package to an (origin node, destination node) pair, counts packages per pair per UTC day, and
raises an analytics event when a pair crosses a threshold over a trailing window and no lane connects the pair.

### Premise check

The contract required that `ExpectedVolumeView` (fed by `LabelIssued` and `LabelVoided`) and the lane graph read model (`LaneView`) let the
service attribute a labelled package to an (origin node, destination node) pair using only data already consumed. It does not.

- **Origin node: available.** `ExpectedVolume.NodeID()` is the `nodeId` of `FulfillmentReleasedToNode` / `FulfillmentLocked` for the
  assignment the label names (`LabelIssued.assignmentId`). A label that arrives before the release has no node yet (state UNKNOWN).
- **Destination node: not available.**
  - `LabelIssued` is consumed with `trackingNumber` and `assignmentId` only (`apis/asyncapi.yaml`, `LabelIssuedReceived`). The producer
    also sends `packageNo`, `orderId`, `tier`, `carrier`, `stopKey` and `destinationPostalPrefix`, which are not consumed. None of them is
    a node id; `destinationPostalPrefix` is a postal prefix, not a delivery station.
  - `FulfillmentReleasedToNode`, `FulfillmentLocked`, `FulfillmentReassigned` and `FulfillmentCancelled` carry no destination node.
  - `NodeView` (ADR 0003) holds type, operator, timezone, status and operating profile. It has no service area or postal coverage, so a
    postal prefix cannot be mapped to a delivery station from local data.
  - The only destination this service knows for an assignment is the destination of its pickup departure (`pickupDepartureId`), which is
    the destination of the origin's default outbound lane (`schedule.pickupSpec`, `DestinationNodeID: in.Lane.Destination`). That is the
    first leg, over a lane that exists by construction. Using it would attribute every package to a pair that already has a lane, so the
    suppression rule ("no lane declared directly between origin and destination") would suppress every suggestion, and when the first leg
    ends at a sortation center the pair is not a facility-to-station pair at all. It does not describe where the package finally goes.

Attributing the pair would need either a new field or a sibling call, both outside the rules of this campaign (additive consumed data
only after the producer publishes it; no synchronous call to a sibling, ADR 0003):

- **Missing field (preferred):** the final delivery station node id of the package, for example an optional `destinationNodeId` on
  `LabelIssued` (or on `FulfillmentReleasedToNode`), published by its owning context; or
- **Missing read model:** a node-to-postal-prefix coverage published by node-topology, which together with the consumed
  `destinationPostalPrefix` would let this service resolve the station locally.

## Decision

The design below is the contract fixed for this feature. It is recorded here and is NOT implemented until the missing field exists.

1. **Policy constants** (documented here, no persisted policy in this slice):
   - window = trailing 14 days (`directLaneWindowDays`);
   - threshold `directLaneThresholdPackages` = 700 packages over the window for one (origin, destination) pair. Reasoning: 700 packages in
     14 days is 50 packages a day, about one full trailer a day-and-a-half-class lane, the volume at which a dedicated truckload lane
     starts to beat consolidating the same packages through a sort center;
   - the pair must have NO lane declared directly between origin and destination in the lane graph read model (a lane already open or
     declared suppresses the suggestion);
   - origin is a FULFILLMENT_CENTER and destination a DELIVERY_STATION (the lane rules already allow it); other node-type pairs are ignored.
2. **Daily aggregation:** a small read model `PairVolumeDaily` (pair, UTC day, packages), written when a label is issued (+1) or voided
   (-1, floor at 0), pruned past 15 days. Migration: the next free number (0003).
3. **Event `DirectLaneSuggested`, analytics only** (the audience is a human planner): type
   `com.retailnetwork.logistics.linehaul-transportation.lane.DirectLaneSuggested`, topic `retailnetwork.linehaul-transportation.analytics`,
   dataschema `urn:retailnetwork:linehaul-transportation:analytics:DirectLaneSuggested:v1`, subject = `<originNodeId>:<destinationNodeId>`,
   data = `originNodeId`, `destinationNodeId`, `packages` (window total), `windowDays` (14), `thresholdPackages`, `suggestedAt`. It joins the
   type catalogue of ADR 0004 (table row and `type: ... stream: analytics` line) and `apis/asyncapi.yaml` when it is implemented.
4. **Suppression:** raised at most once per pair per 14 days unless the window total has at least doubled since the last suggestion. The
   last suggestion (pair, at, packages) is persisted. Evaluated by a pass of the existing clock-driven sweeper, with its interval and batch
   conventions, logged failures and idempotent.
5. **REST read:** `GET /direct-lane-suggestions` lists the current suggestions (pair, packages, suggestedAt), following ADR 0012. No MCP change.
6. **Metrics:** counter `linehaul_direct_lane_suggested_total`.

## Consequences

- No code, migration, event or REST route is added by this record. `make check` is unaffected.
- The feature stays blocked until one of the two inputs above exists. The unblocking change is in another context (the label's
  destination station, or a node coverage read model) and must be agreed there first; this service then consumes it additively and
  tolerantly (ADR 0003) and this ADR moves to Accepted with the contract above unchanged.
- Guessing the destination from the pickup departure, from the postal prefix with a hard-coded table, or by a synchronous sibling call
  was rejected: the first never fires, the second invents topology data this service does not own, the third breaks the no-sync-call rule.
