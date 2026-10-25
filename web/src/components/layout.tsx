import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { DataTable, StatusPill } from "@warehouse/ui-kit";
import { capacityLabel } from "../lib";
import type { Departure, DepartureKind, DepartureState } from "../types";

export function PageHeader({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div>
      <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>{title}</h1>
      <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>{subtitle}</p>
    </div>
  );
}

export function Stack({ children, gap = 4 }: { children: ReactNode; gap?: 3 | 4 | 5 }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: `var(--wh-space-${gap})` }}>
      {children}
    </div>
  );
}

/** A label/value list (dl) for a record's scalar fields. */
export function Facts({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl
      style={{
        display: "grid",
        gridTemplateColumns: "max-content 1fr",
        gap: "var(--wh-space-2) var(--wh-space-4)",
        margin: 0,
        fontSize: "var(--wh-font-size-sm)",
      }}
    >
      {items.map((item) => (
        <div key={item.label} style={{ display: "contents" }}>
          <dt style={{ color: "var(--wh-color-text-muted)" }}>{item.label}</dt>
          <dd style={{ margin: 0 }}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

type Tone = "neutral" | "progress" | "success" | "warning" | "danger";

/** A departure's lifecycle is linehaul-transportation's own, so StatusPill's
 *  tone override carries it. */
const STATE_TONE: Record<DepartureState, Tone> = {
  SCHEDULED: "progress",
  CLOSED: "warning",
  DEPARTED: "neutral",
  ARRIVED: "success",
  CANCELLED: "danger",
};

export function StatePill({ state }: { state: DepartureState }) {
  return <StatusPill status={state} tone={STATE_TONE[state]} size="sm" />;
}

const KIND_TONE: Record<DepartureKind, Tone> = {
  PICKUP: "progress",
  LINEHAUL: "neutral",
  AIR: "warning",
};

/** PICKUP / LINEHAUL / AIR, colour-coded the same way everywhere. */
export function KindPill({ kind }: { kind: DepartureKind }) {
  return <StatusPill status={kind} tone={KIND_TONE[kind]} size="sm" />;
}

/** The departures of the horizon, one row each, newest cutoff first. */
export function DepartureTable({ departures }: { departures: Departure[] }) {
  return (
    <DataTable
      rowKey={(d) => d.departureId}
      rows={departures}
      columns={[
        {
          key: "departure",
          header: "Departure",
          render: (d) => <Link to={`../departures/${encodeURIComponent(d.departureId)}`}>{d.departureId}</Link>,
        },
        { key: "kind", header: "Kind", render: (d) => <KindPill kind={d.kind} /> },
        { key: "lane", header: "Lane", render: (d) => d.laneId },
        { key: "leg", header: "Leg", render: (d) => `${d.originNodeId} → ${d.destinationNodeId}` },
        { key: "cutoff", header: "Cutoff", render: (d) => utc(d.cutoffAt) },
        { key: "departs", header: "Departs", render: (d) => utc(d.departsAt) },
        {
          key: "capacity",
          header: "Booked",
          align: "right",
          render: (d) => `${capacityLabel(d.bookedPackages, d.capacityPackages)} · ${d.remainingPackages} left`,
        },
        { key: "state", header: "State", render: (d) => <StatePill state={d.state} /> },
      ]}
    />
  );
}

function utc(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const d = new Date(t).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 16)}Z`;
}
