import { LINEHAUL_API_BASE } from "./config";
import type { Departure, DepartureList, ListDeparturesQuery } from "./types";

/** RFC 9457 problem+json body every error response from linehaul-transportation
 *  returns (`type` = https://retailnetwork.example/problems/<slug>). */
export interface ProblemDetails {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
}

/**
 * Every failed call -- an HTTP error with a problem+json body, an HTTP error
 * without one, or a network failure -- surfaces as one ApiError, so a screen
 * has a single shape to render: `title` (the problem's category) and `detail`
 * (what exactly was wrong), plus `slug` to branch on a specific problem
 * (e.g. `not-found`, `validation-failed`).
 */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ProblemDetails | null;
  readonly title: string;
  readonly detail: string;
  /** Last path segment of problem.type (e.g. "not-found"); "" when unknown. */
  readonly slug: string;

  constructor(status: number, problem: ProblemDetails | null, fallbackTitle: string) {
    const title = problem?.title || fallbackTitle;
    const detail = problem?.detail ?? "";
    super(detail || title);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.title = title;
    this.detail = detail;
    this.slug = problem?.type ? (problem.type.split("/").pop() ?? "") : "";
  }
}

/** Normalizes anything thrown by a request into an ApiError. */
export function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err;
  const message = err instanceof Error ? err.message : String(err);
  return new ApiError(0, { detail: message }, "Network error");
}

type Query = Record<string, string | number | boolean | undefined>;

export function withQuery(path: string, query?: Query): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

/**
 * The one door to the service. Only this context's own REST API is ever
 * called (`LINEHAUL_API_BASE` = <apiOrigin>/api/linehaul-transportation): no
 * sibling context, no MCP server. This remote is read-only, so the method is
 * always GET and no custom header is ever sent (simple CORS request).
 */
async function request<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${LINEHAUL_API_BASE}${path}`, { method: "GET" });
  } catch (err) {
    throw toApiError(err);
  }
  if (!res.ok) {
    let problem: ProblemDetails | null = null;
    try {
      problem = (await res.json()) as ProblemDetails;
    } catch {
      // non-JSON error body -- fall through with problem = null
    }
    throw new ApiError(res.status, problem, `${res.status} ${res.statusText}`.trim());
  }
  return (await res.json()) as T;
}

const seg = encodeURIComponent;

/** The wire page of GET /departures (fleet REST guideline section 3). */
interface DeparturePage {
  items?: Departure[];
  nextCursor?: string;
}

/** Page size cap of the service and a bound on the follow-the-cursor loop. */
const MAX_PAGE_SIZE = 500;
const MAX_PAGES = 100;

/**
 * GET /departures?origin=&destination=&from=&to=&limit=&cursor= -- by cutoffAt.
 * Follows `nextCursor` until it is absent (never assumes one page); a `limit`
 * is the most departures the caller wants in total.
 */
export async function listDepartures(q: ListDeparturesQuery): Promise<DepartureList> {
  const departures: Departure[] = [];
  let cursor: string | undefined;
  for (let pages = 0; pages < MAX_PAGES; pages++) {
    const page = await request<DeparturePage>(
      withQuery("/departures", {
        origin: q.origin,
        destination: q.destination,
        from: q.from,
        to: q.to,
        limit: q.limit === undefined ? undefined : Math.min(q.limit, MAX_PAGE_SIZE),
        cursor,
      }),
    );
    departures.push(...(page.items ?? []));
    if (!page.nextCursor || (q.limit !== undefined && departures.length >= q.limit)) break;
    cursor = page.nextCursor;
  }
  return { departures: q.limit === undefined ? departures : departures.slice(0, q.limit) };
}

/** GET /departures/{departureId} (404 not-found). */
export async function getDeparture(departureId: string): Promise<Departure> {
  return request<Departure>(`/departures/${seg(departureId)}`);
}
