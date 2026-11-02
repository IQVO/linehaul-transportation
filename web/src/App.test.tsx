import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import App from "./App";
import { json, mockApi } from "./test/fetchMock";
import { cancelledDeparture, departure } from "./test/fixtures";

// The console mounts the remote under its own splat route; the remote's
// relative links must resolve against the mount point, not the full URL.
function renderUnderHost(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/linehaul-transportation/*" element={<App />} />
      </Routes>
    </MemoryRouter>,
  );
}

const ROUTES = {
  "GET /departures": json({ items: [departure()] }),
  "GET /departures/dep-7f3a": json(departure()),
  "GET /departures/dep-0c2d": json(cancelledDeparture()),
};

describe("App", () => {
  it("redirects the mount point to the departure horizon", async () => {
    mockApi(ROUTES);
    renderUnderHost("/linehaul-transportation");
    expect(await screen.findByRole("heading", { name: "Departure horizon" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Departures" })).toHaveAttribute(
      "href", "/linehaul-transportation/departures",
    );
  });

  it("keeps the sub-nav at the mount point from a deep route", async () => {
    mockApi(ROUTES);
    renderUnderHost("/linehaul-transportation/departures/dep-7f3a");
    await screen.findByRole("heading", { name: "Departure dep-7f3a" });
    expect(screen.getByRole("link", { name: "Departures" })).toHaveAttribute(
      "href", "/linehaul-transportation/departures",
    );
  });

  it("navigates from the horizon into one departure", async () => {
    mockApi(ROUTES);
    renderUnderHost("/linehaul-transportation");
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: "dep-7f3a" }));
    expect(await screen.findByRole("heading", { name: "Departure dep-7f3a" })).toBeInTheDocument();
  });

  it("shows the cancelled banner on an ended departure", async () => {
    mockApi(ROUTES);
    renderUnderHost("/linehaul-transportation/departures/dep-0c2d");
    expect(await screen.findByText(/vendor closed early/)).toBeInTheDocument();
    expect(screen.getByRole("note")).toHaveTextContent("Cancelled");
  });
});
