import type { Departure } from "../types";

/** A SCHEDULED linehaul departure of lane-1, n-a -> n-b, cutoff in one hour. */
export function departure(over: Partial<Departure> = {}): Departure {
  return {
    departureId: "dep-7f3a",
    laneId: "lane-1",
    kind: "LINEHAUL",
    originNodeId: "n-a",
    destinationNodeId: "n-b",
    cutoffAt: "2026-10-24T13:00:00Z",
    departsAt: "2026-10-24T13:30:00Z",
    arrivesAt: "2026-10-24T14:30:00Z",
    capacityPackages: 120,
    bookedPackages: 30,
    remainingPackages: 90,
    state: "SCHEDULED",
    ...over,
  };
}

/** A cancelled pickup departure, for the ended-trip rendering. */
export function cancelledDeparture(over: Partial<Departure> = {}): Departure {
  return departure({
    departureId: "dep-0c2d",
    kind: "PICKUP",
    laneId: "lane-2",
    originNodeId: "n-c",
    destinationNodeId: "n-d",
    capacityPackages: 10,
    bookedPackages: 0,
    remainingPackages: 10,
    cutoffAt: "2026-10-24T15:00:00Z",
    departsAt: "2026-10-24T15:10:00Z",
    arrivesAt: "2026-10-24T16:10:00Z",
    state: "CANCELLED",
    cancelReason: "vendor closed early",
    cancelledAt: "2026-10-24T10:00:00Z",
    ...over,
  });
}
