package a2a

import (
	"fmt"
	"strings"
	"time"

	"github.com/claudioed/linehaul-transportation/internal/application/views"
)

// problemTypeBase namespaces this context's problem types. It is the SAME
// namespace as the REST adapter's RFC 9457 types (ADR 0006), so a delegating
// agent sees the identifiers a REST caller would for the outcomes both
// surfaces share (not-found, validation-failed, internal-error).
const problemTypeBase = "https://retailnetwork.example/problems/"

// maxListedIDs caps the ids spelled out in the one-line summary of a list.
const maxListedIDs = 10

// problem is the RFC 7807-shaped rejection an agent client receives instead of
// a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func newProblem(status int, slug, title, detail string) *problem {
	return &problem{Type: problemTypeBase + slug, Title: title, Detail: detail, Status: status}
}

func problemMalformedRequest(detail string) *problem {
	return newProblem(400, "malformed-request", "Malformed skill request", detail)
}

func problemUnknownSkill(skill string) *problem {
	return newProblem(400, "unknown-skill", "Unknown skill", fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill))
}

func problemInvalidArgument(detail string) *problem {
	return newProblem(400, "invalid-argument", "Invalid skill argument", detail)
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string   `json:"skill"`
	Data    skillDTO `json:"data,omitempty"`
	Problem *problem `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs; each skill fills its own
// field, keeping the envelope flat and versioned as one contract.
type skillDTO struct {
	Departure  *departureDTO  `json:"departure,omitempty"`
	Departures *departuresDTO `json:"departures,omitempty"`
	summary    string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// departureDTO is the message-boundary representation of a departure: the
// fields of the REST view and the MCP tool, lowerCamelCase, instants as
// RFC 3339 UTC text. Optional trail fields are omitted when absent.
type departureDTO struct {
	DepartureID       string `json:"departureId"`
	LaneID            string `json:"laneId"`
	Kind              string `json:"kind"`
	OriginNodeID      string `json:"originNodeId"`
	DestinationNodeID string `json:"destinationNodeId"`
	CutoffAt          string `json:"cutoffAt"`
	DepartsAt         string `json:"departsAt"`
	ArrivesAt         string `json:"arrivesAt"`
	CapacityPackages  int    `json:"capacityPackages"`
	BookedPackages    int    `json:"bookedPackages"`
	RemainingPackages int    `json:"remainingPackages"`
	State             string `json:"state"`
	CancelReason      string `json:"cancelReason,omitempty"`
	CancelledAt       string `json:"cancelledAt,omitempty"`
	DepartedAt        string `json:"departedAt,omitempty"`
	ArrivedAt         string `json:"arrivedAt,omitempty"`
}

// departuresDTO is the list_departures result; the array is always present.
type departuresDTO struct {
	Departures []departureDTO `json:"departures"`
}

func instant(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func optionalInstant(t *time.Time) string {
	if t == nil {
		return ""
	}
	return instant(*t)
}

// toDepartureDTO folds the application-layer view into the boundary DTO.
func toDepartureDTO(v views.Departure) departureDTO {
	return departureDTO{
		DepartureID: v.DepartureID, LaneID: v.LaneID, Kind: v.Kind,
		OriginNodeID: v.OriginNodeID, DestinationNodeID: v.DestinationNodeID,
		CutoffAt: instant(v.CutoffAt), DepartsAt: instant(v.DepartsAt), ArrivesAt: instant(v.ArrivesAt),
		CapacityPackages: v.CapacityPackages, BookedPackages: v.BookedPackages, RemainingPackages: v.RemainingPackages,
		State: v.State, CancelReason: v.CancelReason, CancelledAt: optionalInstant(v.CancelledAt),
		DepartedAt: optionalInstant(v.DepartedAt), ArrivedAt: optionalInstant(v.ArrivedAt),
	}
}

func toDeparturesDTO(list views.DepartureList) departuresDTO {
	out := departuresDTO{Departures: make([]departureDTO, 0, len(list.Departures))}
	for _, d := range list.Departures {
		out.Departures = append(out.Departures, toDepartureDTO(d))
	}
	return out
}

// summarizeDeparture is the one-line text form of one departure.
func summarizeDeparture(d departureDTO) string {
	return fmt.Sprintf("%s: %s %s %s -> %s, cutoff %s, %d of %d packages booked (%d remaining)",
		d.DepartureID, d.State, d.Kind, d.OriginNodeID, d.DestinationNodeID, d.CutoffAt,
		d.BookedPackages, d.CapacityPackages, d.RemainingPackages)
}

// summarizeDepartures is the one-line text form of the list.
func summarizeDepartures(list departuresDTO) string {
	n := len(list.Departures)
	if n == 0 {
		return "0 departures"
	}
	shown := min(n, maxListedIDs)
	ids := make([]string, 0, shown)
	for _, d := range list.Departures[:shown] {
		ids = append(ids, d.DepartureID)
	}
	out := fmt.Sprintf("%d departures: %s", n, strings.Join(ids, ", "))
	if n > shown {
		out += fmt.Sprintf(" and %d more", n-shown)
	}
	return out
}
