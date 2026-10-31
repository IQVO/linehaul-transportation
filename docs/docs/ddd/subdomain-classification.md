---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
description: "Why Linehaul Transportation is a Supporting subdomain of the Network area, and the classification of its neighbours."
---

# Subdomain classification

`linehaul-transportation` is a **Supporting** subdomain in the **Network** area (retail network; CloudEvents tier segment `logistics`, so published types start with
`com.retailnetwork.logistics.linehaul-transportation.`). The verdict is the one in the retail-network fleet table of warehouse-docs
(`network/docs/strategic-design/subdomain-classification.md` on `main`, section *Solution space: the bounded contexts*), and it matches the strategic classification of
[ADR 0001](/docs/adr/0001-linehaul-transportation-bounded-context). The neighbour rows below are copied from that table, not recalled.

The subdomain it implements is **Transportation (Middle Mile)**: moving packages between nodes on schedules with capacity, verdict Supporting.

## Why Supporting

- **Not Generic**: the departure model is specific to this network. Pickup departures derive from the cutoffs vendors declare in their operational profile, and
  capacity is booked per fulfillment assignment, so the context cannot be bought off the shelf unchanged.
- **Not Core**: transportation optimisation can be Core at very large scale, but here the delivery promise and order sourcing consume this context's
  schedules and capacities, and route optimisation is not modelled. The competitive core stays with `delivery-promise` and `order-sourcing`.
- **Business model**: cost reducer and speed enabler. **Evolution**: custom-built.

What would move it: modelling route or carrier optimisation (towards Core), or replacing it with a bought transport-management system behind an
anti-corruption layer (towards Generic).

## Neighbours (from the fleet table)

| Bounded context | Classification | Tier | Relation to this context |
| --- | --- | --- | --- |
| `node-topology` | Supporting | `logistics` | upstream: nodes, profiles (cutoffs) and lanes |
| `order-sourcing` | Core | `commerce` | upstream (assignments to book) and downstream (reads departures, `BookingMoved`) |
| `shipping-and-tracking` | Generic | `logistics` | upstream (`LabelIssued`, `LabelVoided`) and downstream (`LoadDeparted`, `LoadArrived`) |
| `delivery-promise` | Core | `commerce` | downstream: departures and capacity for witness paths; re-promises on cancellation |
| `last-mile-delivery` | Supporting | `logistics` | downstream: inducts packages on `LoadArrived` |
| `vendor-connectivity` | Supporting | `partner` | no edge (the fleet reaches the network only through its Vendor API) |
| `node-performance` | Supporting | `partner` | no edge |
| `seller-inbound` | Supporting | `partner` | no edge |
| `offer-availability` | Supporting | `commerce` | no edge |
| `customer-ordering` | Generic | `commerce` | no edge |
| `linehaul-transportation` | **Supporting** | `logistics` | this context |

Count in the table: two Core, seven Supporting, two Generic (as the aggregator counts them). The edges actually implemented are listed on [Integration](/docs/ecosystem/integration).

## Tier is not classification

`commerce`, `logistics` and `partner` group contexts by area of the business, like the fleet's `wms` and `wes`; they are not the Core, Supporting or Generic verdict. For this context the two
axes read: tier `logistics`, class Supporting. The tier decides the type prefix; the class decides where investment goes.
