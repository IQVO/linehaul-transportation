import { useParams } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { getDeparture } from "../api";
import { Facts, KindPill, PageHeader, Stack, StatePill } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { capacitySentence, utcLabel } from "../lib";

/** One departure by id: GET /departures/{departureId}. */
export function DepartureDetailScreen() {
  const { departureId } = useParams();
  const departure = useRequest(
    departureId ? () => getDeparture(departureId) : null,
    `departure|${departureId}`,
  );

  return (
    <Stack gap={5}>
      <PageHeader
        title={departureId ? `Departure ${departureId}` : "Departure"}
        subtitle="linehaul-transportation · capacity, trip and state of one departure"
      />
      <RequestView state={departure} what="departure">
        {(d) => (
          <Card>
            <Stack>
              <Facts
                items={[
                  { label: "Kind", value: <KindPill kind={d.kind} /> },
                  { label: "State", value: <StatePill state={d.state} /> },
                  { label: "Lane", value: d.laneId },
                  { label: "Origin node", value: d.originNodeId },
                  { label: "Destination node", value: d.destinationNodeId },
                  { label: "Cutoff at", value: utcLabel(d.cutoffAt) },
                  { label: "Departs at", value: utcLabel(d.departsAt) },
                  { label: "Arrives at", value: utcLabel(d.arrivesAt) },
                  { label: "Capacity", value: capacitySentence(d.bookedPackages, d.capacityPackages) },
                ]}
              />
              {d.state === "CANCELLED" && (
                <div
                  role="note"
                  style={{
                    color: "var(--wh-color-status-danger)",
                    background: "var(--wh-color-status-danger-bg)",
                    borderRadius: "var(--wh-radius-md)",
                    padding: "10px 14px",
                    fontSize: "var(--wh-font-size-sm)",
                  }}
                >
                  <strong>Cancelled</strong> {utcLabel(d.cancelledAt)} — {d.cancelReason ?? "no reason recorded"}.
                  Its bookings were rebooked or reported failed.
                </div>
              )}
            </Stack>
          </Card>
        )}
      </RequestView>
    </Stack>
  );
}
