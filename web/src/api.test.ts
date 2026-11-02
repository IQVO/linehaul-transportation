import { describe, expect, it } from "vitest";
import { ApiError, getDeparture, listDepartures, toApiError, withQuery } from "./api";
import { LINEHAUL_API_BASE } from "./config";
import { json, mockApi, problem } from "./test/fetchMock";
import { departure } from "./test/fixtures";

describe("withQuery", () => {
  it("drops empty values and encodes the rest", () => {
    expect(withQuery("/departures", { limit: 25, origin: "", destination: undefined, from: "2026-10-24T08:00:00Z" })).toBe(
      "/departures?limit=25&from=2026-10-24T08%3A00%3A00Z",
    );
    expect(withQuery("/departures", {})).toBe("/departures");
    expect(withQuery("/departures")).toBe("/departures");
  });
});

describe("the API base", () => {
  it("is this context's own /api/linehaul-transportation or the developer port, never a sibling", () => {
    expect(LINEHAUL_API_BASE).toBe("http://localhost:8080");
  });
});

describe("requests", () => {
  it("lists departures with the filters as query parameters and no custom header", async () => {
    const api = mockApi({ "GET /departures": json({ items: [departure()] }) });
    const list = await listDepartures({
      origin: "n-a", destination: "n-b", from: "2026-10-24T08:00:00Z", to: "2026-10-25T08:00:00Z", limit: 25,
    });
    expect(list.departures).toHaveLength(1);
    const call = api.calls[0];
    expect(call.url.startsWith(LINEHAUL_API_BASE + "/departures?")).toBe(true);
    expect(Object.fromEntries(call.query)).toEqual({
      origin: "n-a", destination: "n-b", from: "2026-10-24T08:00:00Z", to: "2026-10-25T08:00:00Z", limit: "25",
    });
    expect(call.headers).toEqual({});
    expect(call.method).toBe("GET");
  });

  it("normalizes a missing array to an empty one", async () => {
    mockApi({ "GET /departures": json({}) });
    expect(await listDepartures({})).toEqual({ departures: [] });
  });

  it("reads one departure by escaped id", async () => {
    const api = mockApi({ "GET /departures/dep%207": json(departure({ departureId: "dep 7" })) });
    expect((await getDeparture("dep 7")).departureId).toBe("dep 7");
    expect(api.calls.map((c) => c.path)).toEqual(["/departures/dep%207"]);
  });

  it("follows nextCursor until it is absent", async () => {
    const api = mockApi({
      "GET /departures": (call) =>
        call.query.get("cursor") === "c2"
          ? json({ items: [departure({ departureId: "dep-2" })] })
          : json({ items: [departure({ departureId: "dep-1" })], nextCursor: "c2" }),
    });
    const list = await listDepartures({});
    expect(list.departures.map((d) => d.departureId)).toEqual(["dep-1", "dep-2"]);
    expect(api.calls.map((c) => c.query.get("cursor"))).toEqual([null, "c2"]);
  });

  it("stops at the caller's limit", async () => {
    const api = mockApi({ "GET /departures": json({ items: [departure()], nextCursor: "more" }) });
    const list = await listDepartures({ limit: 1 });
    expect(list.departures).toHaveLength(1);
    expect(api.calls).toHaveLength(1);
  });

  it("calls nothing but this context's API", async () => {
    const api = mockApi({
      "GET /departures": json({ items: [] }),
      "GET /departures/dep-7f3a": json(departure()),
    });
    await listDepartures({});
    await getDeparture("dep-7f3a");
    for (const c of api.calls) expect(c.url.startsWith(LINEHAUL_API_BASE + "/")).toBe(true);
  });
});

describe("errors", () => {
  it("keeps the problem's title, detail and slug", async () => {
    mockApi({ "GET /departures/dep-nope": problem(404, "not-found", "Not found", "departure dep-nope does not exist") });
    const err = await getDeparture("dep-nope").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const e = err as ApiError;
    expect([e.status, e.slug, e.title, e.detail]).toEqual([404, "not-found", "Not found", "departure dep-nope does not exist"]);
    expect(e.message).toBe("departure dep-nope does not exist");
  });

  it("maps a validation failure of the window filters", async () => {
    mockApi({ "GET /departures": problem(422, "validation-failed", "Validation failed", "from is after to") });
    const e = (await listDepartures({ from: "2026-10-25T00:00:00Z", to: "2026-10-24T00:00:00Z" }).catch((err: unknown) => err)) as ApiError;
    expect([e.status, e.slug]).toEqual([422, "validation-failed"]);
  });

  it("falls back to the status text for a body that is not a problem", async () => {
    mockApi({ "GET /departures/x": new Response("<html>", { status: 502, statusText: "Bad Gateway" }) });
    const e = (await getDeparture("x").catch((err: unknown) => err)) as ApiError;
    expect([e.status, e.title, e.slug, e.problem]).toEqual([502, "502 Bad Gateway", "", null]);
  });

  it("turns a network failure into an ApiError with status 0", async () => {
    mockApi({});
    const e = (await getDeparture("x").catch((err: unknown) => err)) as ApiError;
    expect(e.status).toBe(0);
    expect(e.title).toBe("Network error");
    expect(toApiError(new Error("boom")).detail).toBe("boom");
    expect(toApiError("text").detail).toBe("text");
    expect(toApiError(e)).toBe(e);
  });
});
