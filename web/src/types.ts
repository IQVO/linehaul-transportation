/** Wire types of apis/openapi.yaml (camelCase, as the REST contract names them). */

export const DEPARTURE_KINDS = ["PICKUP", "LINEHAUL", "AIR"] as const;
export type DepartureKind = (typeof DEPARTURE_KINDS)[number];

export const DEPARTURE_STATES = ["SCHEDULED", "CLOSED", "DEPARTED", "ARRIVED", "CANCELLED"] as const;
export type DepartureState = (typeof DEPARTURE_STATES)[number];

export interface Departure {
  departureId: string;
  laneId: string;
  kind: DepartureKind;
  originNodeId: string;
  destinationNodeId: string;
  cutoffAt: string;
  departsAt: string;
  arrivesAt: string;
  capacityPackages: number;
  bookedPackages: number;
  remainingPackages: number;
  state: DepartureState;
  /** Present when the state is CANCELLED. */
  cancelReason?: string;
  cancelledAt?: string;
  /** Present once the state reached DEPARTED. */
  departedAt?: string;
  /** Present once the state reached ARRIVED. */
  arrivedAt?: string;
}

export interface DepartureList {
  departures: Departure[];
}

export interface ListDeparturesQuery {
  origin?: string;
  destination?: string;
  /** RFC 3339 instant; earliest cutoffAt, inclusive. */
  from?: string;
  /** RFC 3339 instant; latest cutoffAt, exclusive. */
  to?: string;
  limit?: number;
}
