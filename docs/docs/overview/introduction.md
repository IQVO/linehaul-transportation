---
id: introduction
title: Introduction
sidebar_label: Introduction
description: "What Linehaul Transportation owns and does not own, its subdomain class, capabilities and where to read next."
---

# Linehaul Transportation

Linehaul Transportation is the middle-mile bounded context of the retail network. It turns the static lanes of node-topology into a rolling
14-day horizon of scheduled departures, keeps each departure's booked and remaining package capacity honest, books locked fulfillment assignments onto
pickup departures, and records the loads of packages that are closed, dispatched and received. It is a **Supporting** subdomain of the **Network**
area (CloudEvents tier `logistics`), built as a domain-driven design study: nothing in it is a real company's system.

## The problem it solves

A package leaves a vendor's fulfillment centre, is trucked to a sortation centre, rides another truck (or a plane through an air hub) and arrives at
the delivery station that serves the customer. Everything between the vendor's dock and the delivery station is the middle mile.

`node-topology` knows the shape of that network: which nodes exist and which **lanes** connect them, with a nominal transit time. A lane is static: it
does not say when a truck leaves, how many packages it can carry, or whether tomorrow's 18:00 trip is still running. A delivery promise cannot rest on
a nominal number; it needs concrete trips with a cutoff, a departure, an arrival and remaining room. This context supplies those trips.

A vendor declares, in its node's operational profile, the local times by which packages are ready (its **cutoffs**). A declaration alone moves nothing:
the network has to send a truck. The policy `PickupFromCutoffs` makes that link, generating one `PICKUP` departure for every cutoff of the horizon and
cancelling the future ones when the node is suspended or retired. The pickup departure is what other contexts book against.

## What it owns

| Concept | Meaning |
| --- | --- |
| **LaneSchedule** | The weekly template of one lane: slots with day, local cutoff, departs and arrives time and package capacity. ACTIVE or RETIRED. |
| **Departure** | One concrete trip: kind `PICKUP`, `LINEHAUL` or `AIR`, cutoff, departs, arrives, capacity and booked count; state SCHEDULED, CLOSED, DEPARTED, ARRIVED or CANCELLED. Invariant: `booked <= capacity`. |
| **Load** | The packages (tracking numbers) consolidated on one departure at its origin node: OPEN, CLOSED, DEPARTED, ARRIVED. A package is on at most one OPEN or CLOSED load. |
| **ExpectedVolume** | The local record of one fulfillment assignment and where it is booked (UNKNOWN, RELEASED, BOOKED, FAILED, CANCELLED). |
| Local read models | `node_views` and `lane_views`, copies of node-topology facts fed by events. |

## What it does NOT own

- **The network shape.** Nodes, lanes, node types and operational profiles belong to `node-topology`; this context only reacts to `NodeRegistered`,
  `NodeOperationalProfileDeclared`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `LaneOpened` and `LaneClosed`.
- **Which node or path fulfils an order.** That is `order-sourcing`; this context books the capacity of the pickup departure it names.
- **Parcel labels and the customer's tracking timeline.** `shipping-and-tracking` owns them; this context learns tracking numbers from `LabelIssued`
  and reports load movements.
- **The delivery date promised to a customer** (`delivery-promise`) and **delivery waves and routes** (`last-mile-delivery`).
- **Carrier procurement, trailers, drivers, freight cost and settlement.** Not modelled. Route optimisation is not modelled either.
- **Anything inside a vendor's building.** The warehouse-systems fleet never talks to this context; the only channel between the organisations is the
  vendor-connectivity Vendor API.

## Subdomain class and area

Supporting, tier `logistics`: other contexts consume its schedules and capacities, but nothing here decides a promise or a route. Details and the
neighbours are on [Subdomain classification](/docs/ddd/subdomain-classification).

## Capabilities

- Define and retire the weekly schedule of a lane (REST `POST /lane-schedules`, `POST /lane-schedules/{laneId}:retire`).
- Generate departures for the next 14 days (horizon roll), including a pickup departure at every cutoff of an ACTIVE vendor node.
- Cancel a departure or change its capacity; assignments booked on a cancelled departure move to the next pickup or fail.
- Book capacity from `FulfillmentLocked`, keep the expected volume of each assignment, and add tracking numbers from `LabelIssued`.
- Open, fill, close, dispatch and receive loads; dispatch and receive record the departure and the arrival of the trip.
- Publish every change as a CloudEvents 1.0 event through a transactional outbox, with analytics-only events on a second topic.
- Serve the departure reads over REST, a read-only MCP server and a read-only A2A agent.

## Decisions it makes

1. Every declared cutoff of an ACTIVE vendor fulfillment node gets one pickup departure per operating day.
2. A departure's booked packages never exceed its capacity (`departure-full`).
3. A package is on at most one OPEN or CLOSED load (`package-already-loaded`).
4. A cancelled departure is announced and its bookings are moved or failed in the same transaction, so promises can be revised.

## Where to read next

| You want to | Read |
| --- | --- |
| Run it | [Quickstart](/docs/overview/quickstart), [Configuration](/docs/operations/configuration) |
| Understand the layout | [Architecture](/docs/overview/architecture), [Runtime](/docs/overview/runtime) |
| Operate it | [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability), [Troubleshooting](/docs/operations/troubleshooting) |
| Test it | [Testing](/docs/development/testing) |
| See the domain | [Use cases](/docs/ddd/use-cases), [Ubiquitous language](/docs/ddd/ubiquitous-language), [DDD artifacts](/docs/ddd/ddd-artifacts) |
| Integrate | [API overview](/docs/api-reference), [Events](/docs/api-reference/events), [Integration](/docs/ecosystem/integration), [MCP tools](/docs/mcp/tools) |
| Know why | [ADR index](/docs/adr) |

The network-level view of this context (its neighbours and the model it is built from) lives on the
[retail-network pages of warehouse-docs](https://iqvo.github.io/warehouse-docs/network).
