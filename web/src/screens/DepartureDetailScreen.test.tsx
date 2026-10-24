import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DepartureDetailScreen } from "./DepartureDetailScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { cancelledDeparture, departure } from "../test/fixtures";

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/departures/:departureId" element={<DepartureDetailScreen />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("DepartureDetailScreen", () => {
  it("reads one departure and shows its plan, capacity and state", async () => {
    mockApi({ "GET /departures/dep-7f3a": json(departure()) });
    renderAt("/departures/dep-7f3a");
    expect(await screen.findByRole("heading", { name: "Departure dep-7f3a" })).toBeInTheDocument();
    expect(screen.getByText("lane-1")).toBeInTheDocument();
    expect(screen.getByText("n-a")).toBeInTheDocument();
    expect(screen.getByText("n-b")).toBeInTheDocument();
    expect(screen.getByText("120 pkgs capacity · 30 booked · 90 remaining")).toBeInTheDocument();
    expect(screen.getByText("2026-10-24 13:00:00Z")).toBeInTheDocument();
    expect(screen.getByText("SCHEDULED")).toBeInTheDocument();
  });

  it("shows the cancel note on a cancelled departure", async () => {
    mockApi({ "GET /departures/dep-0c2d": json(cancelledDeparture()) });
    renderAt("/departures/dep-0c2d");
    expect(await screen.findByRole("note")).toHaveTextContent("Cancelled");
    expect(screen.getByText(/vendor closed early/)).toBeInTheDocument();
    expect(screen.getByText("CANCELLED")).toBeInTheDocument();
  });

  it("shows the problem body of a 404", async () => {
    mockApi({ "GET /departures/dep-nope": problem(404, "not-found", "Not found", "departure dep-nope does not exist") });
    renderAt("/departures/dep-nope");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Not found");
    expect(alert).toHaveTextContent("departure dep-nope does not exist");
  });
});
