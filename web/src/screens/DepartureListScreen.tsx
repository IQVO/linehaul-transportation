import { useState } from "react";
import { Card } from "@warehouse/ui-kit";
import { listDepartures } from "../api";
import { Form, FormRow, InlineError, SubmitButton, TextField } from "../components/formkit";
import { DepartureTable, PageHeader, Stack } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { instantProblem, optionalIdProblem, wholeNumber } from "../lib";

interface Filters {
  origin: string;
  destination: string;
  from: string;
  to: string;
  limit: string;
}

const INITIAL: Filters = { origin: "", destination: "", from: "", to: "", limit: "" };

/**
 * The departure horizon: GET /departures, ordered by cutoffAt then id,
 * optionally narrowed to one origin, one destination and a cutoff window
 * [from, to) (RFC 3339 instants, from inclusive, to exclusive). The API has
 * no cursor: `limit` (1..1000, default 500) bounds the page.
 */
export function DepartureListScreen() {
  const [draft, setDraft] = useState<Filters>(INITIAL);
  const [applied, setApplied] = useState<Filters>(INITIAL);
  const limit = wholeNumber(applied.limit);
  const departures = useRequest(
    () =>
      listDepartures({
        origin: applied.origin || undefined,
        destination: applied.destination || undefined,
        from: applied.from || undefined,
        to: applied.to || undefined,
        limit: limit ?? undefined,
      }),
    `departures|${applied.origin}|${applied.destination}|${applied.from}|${applied.to}|${applied.limit}`,
  );

  const originProblem = optionalIdProblem("origin node", draft.origin.trim());
  const destinationProblem = optionalIdProblem("destination node", draft.destination.trim());
  const fromProblem = instantProblem("from", draft.from.trim());
  const toProblem = instantProblem("to", draft.to.trim());
  const limitProblem = draft.limit.trim() === "" || wholeNumber(draft.limit) !== null ? null : "limit must be a whole number.";
  const apply = () => {
    if (originProblem || destinationProblem || fromProblem || toProblem || limitProblem) return;
    setApplied({
      origin: draft.origin.trim(),
      destination: draft.destination.trim(),
      from: draft.from.trim(),
      to: draft.to.trim(),
      limit: draft.limit.trim(),
    });
  };

  return (
    <Stack gap={5}>
      <PageHeader
        title="Departure horizon"
        subtitle="linehaul-transportation · the departures generated from the lane schedules, their remaining capacity and where each trip stands"
      />
      <Card>
        <Form onSubmit={apply} label="Filter departures">
          <FormRow>
            <TextField
              label="Origin node"
              value={draft.origin}
              onChange={(origin) => setDraft({ ...draft, origin })}
              placeholder="n-a"
              hint="Optional: only departures leaving this node."
            />
            <TextField
              label="Destination node"
              value={draft.destination}
              onChange={(destination) => setDraft({ ...draft, destination })}
              placeholder="n-b"
              hint="Optional: only departures bound for this node."
            />
            <TextField
              label="From (cutoff)"
              value={draft.from}
              onChange={(from) => setDraft({ ...draft, from })}
              placeholder="2026-10-24T08:00:00Z"
              hint="Optional: earliest cutoffAt, inclusive (RFC 3339)."
            />
            <TextField
              label="To (cutoff)"
              value={draft.to}
              onChange={(to) => setDraft({ ...draft, to })}
              placeholder="2026-10-25T08:00:00Z"
              hint="Optional: latest cutoffAt, exclusive (RFC 3339)."
            />
            <TextField
              label="Limit"
              value={draft.limit}
              onChange={(limit) => setDraft({ ...draft, limit })}
              placeholder="500"
              inputMode="numeric"
              hint="Optional: at most this many departures, 1..1000."
            />
            <SubmitButton>Apply filters</SubmitButton>
          </FormRow>
          <InlineError message={originProblem ?? destinationProblem ?? fromProblem ?? toProblem ?? limitProblem} />
        </Form>
      </Card>
      <RequestView
        state={departures}
        what="departures"
        isEmpty={(list) => list.departures.length === 0}
        empty="No departure matches these filters in the horizon."
      >
        {(list) => (
          <Card>
            <DepartureTable departures={list.departures} />
          </Card>
        )}
      </RequestView>
    </Stack>
  );
}
