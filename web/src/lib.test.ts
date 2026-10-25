import { describe, expect, it } from "vitest";
import { capacityLabel, idProblem, instantProblem, optionalIdProblem, packages, utcLabel, wholeNumber } from "./lib";

describe("utcLabel", () => {
  it("formats an instant to the second in UTC", () => {
    expect(utcLabel("2026-10-24T12:00:30+00:00")).toBe("2026-10-24 12:00:30Z");
    expect(utcLabel("2026-10-24T09:00:30-03:00")).toBe("2026-10-24 12:00:30Z");
  });
  it("shows a dash for nothing and the raw text for garbage", () => {
    expect(utcLabel(undefined)).toBe("—");
    expect(utcLabel("soon")).toBe("soon");
  });
});

describe("package counts", () => {
  it("packages", () => {
    expect(packages(120)).toBe("120 pkgs");
  });
  it("capacityLabel is booked over capacity", () => {
    expect(capacityLabel(30, 120)).toBe("30/120");
    expect(capacityLabel(0, 10)).toBe("0/10");
  });
});

describe("idProblem", () => {
  it("accepts a plain node id", () => {
    expect(idProblem("origin node", "n-a")).toBeNull();
    expect(idProblem("origin node", "a".repeat(64))).toBeNull();
  });
  it("names what is wrong", () => {
    expect(idProblem("origin node", "")).toMatch(/Enter a origin node id/);
    expect(idProblem("origin node", "a".repeat(65))).toMatch(/at most 64/);
    expect(idProblem("origin node", "n a")).toMatch(/cannot contain/);
    expect(idProblem("origin node", "n/a")).toMatch(/cannot contain/);
    expect(idProblem("origin node", "n\ta")).toMatch(/cannot contain/);
  });
  it("lets an optional filter be empty", () => {
    expect(optionalIdProblem("origin node", "")).toBeNull();
    expect(optionalIdProblem("origin node", "n/a")).toMatch(/cannot contain/);
  });
});

describe("instantProblem", () => {
  it("accepts an empty filter and a whole RFC 3339 instant", () => {
    expect(instantProblem("from", "")).toBeNull();
    expect(instantProblem("from", "2026-10-24T08:00:00Z")).toBeNull();
    expect(instantProblem("to", "2026-10-24T08:00:30.250+02:00")).toBeNull();
  });
  it("refuses anything the API would reject", () => {
    expect(instantProblem("from", "soon")).toMatch(/RFC 3339/);
    expect(instantProblem("from", "2026-10-24")).toMatch(/RFC 3339/);
    expect(instantProblem("to", "2026-13-40T08:00:00Z")).toMatch(/RFC 3339/);
  });
});

describe("wholeNumber", () => {
  it("parses a plain integer", () => {
    expect(wholeNumber("500")).toBe(500);
    expect(wholeNumber("0")).toBe(0);
  });
  it("refuses decimals, exponents, negatives and text", () => {
    expect(wholeNumber("12.5")).toBeNull();
    expect(wholeNumber("1e3")).toBeNull();
    expect(wholeNumber("-1")).toBeNull();
    expect(wholeNumber("")).toBeNull();
    expect(wholeNumber("abc")).toBeNull();
  });
});
