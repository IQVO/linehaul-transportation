import { NO_VALUE } from "@warehouse/ui-kit";

/** "2026-10-24 08:05:30Z" for an RFC 3339 instant; the raw text when unparseable. */
export function utcLabel(iso: string | undefined): string {
  if (!iso) return NO_VALUE;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const d = new Date(t).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 19)}Z`;
}

/** A package count: "120 pkgs". */
export function packages(n: number): string {
  return `${n} pkgs`;
}

/** "120 pkgs capacity · 30 booked · 90 remaining" -- the long form used on
 *  the detail screen. */
export function capacitySentence(booked: number, capacity: number): string {
  return `${packages(capacity)} capacity · ${booked} booked · ${capacity - booked} remaining`;
}

/** Booked over capacity: "3/120". */
export function capacityLabel(booked: number, capacity: number): string {
  return `${booked}/${capacity}`;
}

/** The id rule shared by node ids: 1..64 characters, no whitespace, control
 *  characters or "/". */
export function idProblem(what: string, id: string): string | null {
  if (id.length === 0) return `Enter a ${what} id.`;
  if (id.length > 64) return `A ${what} id is at most 64 characters.`;
  // eslint-disable-next-line no-control-regex
  if (/[\s/\u0000-\u001f\u007f]/.test(id)) return `A ${what} id cannot contain spaces, control characters or '/'.`;
  return null;
}

/** Like idProblem, but an empty filter is fine ("any"). */
export function optionalIdProblem(what: string, id: string): string | null {
  return id.length === 0 ? null : idProblem(what, id);
}

const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;

/** A whole RFC 3339 instant (what /departures' from and to expect), or the
 *  reason it is not one; null when empty (no filter) or valid. */
export function instantProblem(field: string, raw: string): string | null {
  if (raw === "") return null;
  if (!RFC3339.test(raw) || Number.isNaN(Date.parse(raw))) {
    return `${field} must be an RFC 3339 instant, e.g. 2026-10-24T08:00:00Z.`;
  }
  return null;
}

/** A strictly whole number typed into a field, or null ("12.5", "1e3", "abc", ""). */
export function wholeNumber(raw: string): number | null {
  const s = raw.trim();
  if (!/^\d+$/.test(s)) return null;
  const n = Number(s);
  return Number.isSafeInteger(n) ? n : null;
}
