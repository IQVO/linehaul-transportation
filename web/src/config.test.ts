import { describe, expect, it } from "vitest";
import { resolveLinehaulApiBase } from "./config";

describe("resolveLinehaulApiBase", () => {
  it("is this context's own /api/linehaul-transportation under the runtime origin", () => {
    expect(resolveLinehaulApiBase({ apiOrigin: "http://localhost:8000" }, true)).toBe(
      "http://localhost:8000/api/linehaul-transportation",
    );
  });

  it("drops trailing slashes of the origin", () => {
    expect(resolveLinehaulApiBase({ apiOrigin: "http://kong.local///" }, false)).toBe(
      "http://kong.local/api/linehaul-transportation",
    );
  });

  it("falls back to the developer port outside production", () => {
    expect(resolveLinehaulApiBase({}, false)).toBe("http://localhost:8080");
  });

  it("refuses to guess in production", () => {
    expect(() => resolveLinehaulApiBase({}, true)).toThrow(/apiOrigin is required/);
    expect(() => resolveLinehaulApiBase({ apiOrigin: "" }, true)).toThrow(/apiOrigin is required/);
  });
});
