---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
description: "The terms Linehaul Transportation speaks, each with the code identifier that implements it, and the neighbour terms it uses."
---

# Ubiquitous language

The network-wide glossary is on the [retail-network pages of warehouse-docs](https://iqvo.github.io/warehouse-docs/network/strategic-design/ubiquitous-language). The code
identifiers below are the names in this repository; `(context)` marks a term that names the context itself rather than a type.

## This context's terms

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Middle mile** | Transport between nodes: vendor node to sortation centre, sortation centre to delivery station, air | `(context)` | **Last mile** (`last-mile-delivery`: delivery station to the door) |
| **Departure** | One scheduled trip on a lane with a cutoff and a capacity | `departure.Departure` | **Lane** (`node-topology`: the static connection a departure runs on) |
| **Pickup departure** | The departure that collects packages at a vendor node at one of its declared cutoffs | `departure.KindPickup` | **Cutoff** (the vendor's declared local time; the pickup departure is the trip generated from it) |
| **Departure kind** | `PICKUP`, `LINEHAUL` or `AIR` | `departure.Kind` | **Lane mode** (`GROUND` or `AIR`, a property of the lane) |
| **Departure times** | `cutoffAt <= departsAt < arrivesAt` | `departure.Departure` fields; checked by `validTimes` and a `CHECK` on `departures` | **Nominal transit** (the lane's planning figure in minutes) |
| **Capacity** | Packages a departure can carry and how many are booked; remaining = capacity - booked | `capacity`, `booked`, `Remaining()` | **Throughput cap** (a node's declared orders per time unit) |
| **Booking** | Counting one package against a departure's capacity, done on `FulfillmentLocked` (policy `BookOnLock`) or when a package joins a LINEHAUL or AIR load | `Departure.Book`, `Departure.Release` | **Capacity hold** (`delivery-promise`'s provisional reservation) |
| **Departure cutoff** | The instant after which no booking is accepted on a departure | `departure.ErrDepartureClosed`, `Departure.CutoffAt` | **Induct cutoff** of a delivery wave (`last-mile-delivery`) |
| **Load** | The packages (tracking numbers) consolidated onto one departure | `load.Load` | **Departure** (the trip; a load is what is physically put on it) |
| **Lane schedule** | The weekly template that generates a lane's departures | `schedule.LaneSchedule`, `schedule.Slot` | **Departure** (one concrete trip generated from a slot) |
| **Horizon** | The 14 days of departures kept ahead by the horizon roll (policy `HorizonRoll`) | `Service.GenerateDepartures` | **Operating day** of a node |
| **Expected volume** | The local record of one fulfillment assignment: which pickup departure it is booked on and which tracking numbers belong to it | `volume.ExpectedVolume` | **Booked** (only locked assignments are booked) |
| **Booking moved / failed** | An assignment rebooked on the next pickup of the same origin, or one that no departure could hold | `volume.BookingMoved`, `volume.BookingFailed` | **Cancelled departure** (the cause) |
| **Pickup eligible** | An ACTIVE vendor fulfillment centre with a declared profile | `network.NodeView.PickupEligible` | **Active node** (any operator or type) |
| **Dispatch / receive** | The load leaves its origin (CLOSED to DEPARTED) / is received at its destination (DEPARTED to ARRIVED) | `Service.DispatchLoad`, `Service.ReceiveLoad` | **Departure state** (updated by the same command) |

## Neighbour terms used here

| Term | Definition (owner) | Where it appears | Not to be confused with |
| --- | --- | --- | --- |
| **Node** | One physical building of the network, of one node type (`node-topology`) | `network.NodeView` (`node_views`) | **Node operator** (`NETWORK` or `VENDOR`) |
| **Node type** | `FULFILLMENT_CENTER`, `INBOUND_CROSS_DOCK`, `RETURNS_CENTER`, `SORTATION_CENTER`, `DELIVERY_STATION`, `AIR_HUB` | `network.NodeType` | **Departure kind** |
| **Operational profile** | The node's declared operating hours, holidays, cutoffs, throughput cap and time zone, versioned by `profileRevision` | `network.Profile` | **Scorecard** (`node-performance`) |
| **Cutoff** | A local time by which a node declares packages are ready for hand-over | `network.Cutoff` | **Departure cutoff** (`cutoffAt`) |
| **Lane** | A directed, mode-specific connection between two nodes (`node-topology`) | `network.LaneView` (`lane_views`) | **Lane schedule** |
| **Fulfillment assignment** | The decision of which node and path fulfil one shipment group (`order-sourcing`) | `assignmentId` | **Load** |
| **Lock** | The point after which an assignment never changes node (`order-sourcing`) | `FulfillmentLocked` | **Booking** (which follows the lock) |
| **Tracking number** | The network-wide unique id of one package (`shipping-and-tracking`) | `trackingNumber` in `load_packages`, `expected_volumes` | `loadId` |
| **Delivery wave / induction** | A delivery station's dispatch slot and the placing of an arrived package into it (`last-mile-delivery`) | consumer of `LoadArrived` | **Departure**, `ReceiveLoad` |

The warehouse-systems fleet has no translation here: it never touches this context. The fleet's CPT is the cutoff a vendor must meet, handled by `vendor-connectivity`.
