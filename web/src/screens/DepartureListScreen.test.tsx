import { MemoryRouter } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { DepartureListScreen } from "./DepartureListScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { cancelledDeparture, departure } from "../test/fixtures";

function renderScreen(path = "/departures") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <DepartureListScreen />
    </MemoryRouter>,
  );
}

describe("DepartureListScreen", () => {
  it("loads the horizon and renders one row per departure", async () => {
    mockApi({ "GET /departures": json({ departures: [departure(), cancelledDeparture()] }) });
    renderScreen();
    expect(await screen.findByRole("link", { name: "dep-7f3a" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "dep-0c2d" })).toBeInTheDocument();
    expect(screen.getByText("n-a → n-b")).toBeInTheDocument();
    expect(screen.getByText("30/120 · 90 left")).toBeInTheDocument();
    expect(screen.getByText("2026-10-24 13:00Z")).toBeInTheDocument();
  });

  it("sends the filters as query parameters only after Apply", async () => {
    const api = mockApi({ "GET /departures": json({ departures: [] }) });
    renderScreen();
    await screen.findByText(/No departure matches/);
    expect(api.calls).toHaveLength(1); // the unfiltered first load
    expect(Object.fromEntries(api.calls[0].query)).toEqual({});

    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Origin node"), "n-a");
    await user.type(screen.getByLabelText("From (cutoff)"), "2026-10-24T08:00:00Z");
    await user.type(screen.getByLabelText("Limit"), "25");
    await user.click(screen.getByRole("button", { name: "Apply filters" }));
    await screen.findByText(/No departure matches/, undefined, { timeout: 3000 });
    const last = api.calls[api.calls.length - 1];
    expect(Object.fromEntries(last.query)).toEqual({
      origin: "n-a", from: "2026-10-24T08:00:00Z", limit: "25",
    });
  });

  it("refuses a malformed filter and fires no request for it", async () => {
    const api = mockApi({ "GET /departures": json({ departures: [] }) });
    renderScreen();
    await screen.findByText(/No departure matches/);
    const before = api.calls.length;

    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Origin node"), "n a");
    await user.click(screen.getByRole("button", { name: "Apply filters" }));
    expect(await screen.findByText(/cannot contain/)).toBeInTheDocument();
    expect(api.calls.length).toBe(before); // the invalid draft never reached the API
  });

  it("refuses a window instant the API would reject", async () => {
    mockApi({ "GET /departures": json({ departures: [] }) });
    renderScreen();
    await screen.findByText(/No departure matches/);
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("To (cutoff)"), "soon");
    await user.click(screen.getByRole("button", { name: "Apply filters" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("to must be an RFC 3339 instant");
  });

  it("shows the problem body of a failed load", async () => {
    mockApi({ "GET /departures": problem(422, "validation-failed", "Validation failed", "from is after to") });
    renderScreen();
    expect(await screen.findByRole("alert")).toHaveTextContent("Validation failed");
    expect(screen.getByText("from is after to")).toBeInTheDocument();
  });
});
