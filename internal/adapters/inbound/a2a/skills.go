package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/claudioed/linehaul-transportation/internal/application/ports"
	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
	"github.com/claudioed/linehaul-transportation/internal/domain/shared"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/claudioed/linehaul-transportation/internal/adapters/inbound/a2a"

// Skill ids advertised on the Agent Card (see Card). They are the MCP tool
// names of the same reads (ADR 0008), so one name identifies one question
// across both agent-facing surfaces.
const (
	skillListDepartures = "list_departures"
	skillGetDeparture   = "get_departure"
)

// skillRequest is the wire contract of every skill call: the first message
// part carries (as a Data part, or a Text part holding the same JSON) the
// skill id and its arguments. Strict on purpose: an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored, and a
// misspelled name like "limitt" must not silently fall back to the default.
// (Go's encoding/json matches field names case-insensitively, so "ORIGIN"
// still reaches origin; only genuinely unknown names are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// listDeparturesArgs and getDepartureArgs are the per-skill argument shapes,
// the same names and meanings as the MCP tools.
type listDeparturesArgs struct {
	Origin      string `json:"origin"`
	Destination string `json:"destination"`
	From        string `json:"from"`
	To          string `json:"to"`
	Limit       int    `json:"limit"`
}

type getDepartureArgs struct {
	DepartureID string `json:"departureId"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (ADR 0011).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, prob := parseSkillRequest(ec)
	if prob != nil {
		return reply(ec, envelope{Skill: "", Problem: prob}, prob.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, prob := run(ctx, deps, req)
	if prob != nil {
		span.SetStatus(codes.Error, prob.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: prob}, prob.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillListDepartures:
		data, prob := deps.listDepartures(ctx, req.Arguments)
		if prob != nil {
			return skillDTO{}, prob
		}
		return skillDTO{Departures: &data, summary: summarizeDepartures(data)}, nil
	case skillGetDeparture:
		data, prob := deps.getDeparture(ctx, req.Arguments)
		if prob != nil {
			return skillDTO{}, prob
		}
		return skillDTO{Departure: &data, summary: summarizeDeparture(data)}, nil
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first part.
// A Data part (or a Text part holding the same JSON object) is accepted;
// anything else is a request-shape problem, answered, not raised, because a
// malformed delegation is a business answer for the calling agent, not a
// transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// listDepartures is the departure-horizon read skill, through the same
// ListDepartures use case the REST and MCP adapters use. Filters are ANDed;
// from is inclusive and to exclusive on cutoffAt.
func (d Deps) listDepartures(ctx context.Context, rawArgs json.RawMessage) (departuresDTO, *problem) {
	var args listDeparturesArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return departuresDTO{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	q := usecases.ListDeparturesQuery{Origin: args.Origin, Destination: args.Destination, Limit: args.Limit}
	var prob *problem
	if q.From, prob = parseInstant("from", args.From); prob != nil {
		return departuresDTO{}, prob
	}
	if q.To, prob = parseInstant("to", args.To); prob != nil {
		return departuresDTO{}, prob
	}
	list, err := d.Departures.ListDepartures(ctx, q)
	if err != nil {
		return departuresDTO{}, problemFromUseCase(err, "")
	}
	return toDeparturesDTO(list), nil
}

// parseInstant parses an optional RFC 3339 argument; an unparsable value is
// refused as REST refuses its query parameter: 422 validation-failed.
func parseInstant(name, raw string) (*time.Time, *problem) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, problemFromUseCase(shared.Invalid("%s must be an RFC 3339 instant", name), "")
	}
	return &t, nil
}

// getDeparture is the single-departure read skill, through the same
// GetDeparture use case the REST and MCP adapters use.
func (d Deps) getDeparture(ctx context.Context, rawArgs json.RawMessage) (departureDTO, *problem) {
	var args getDepartureArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return departureDTO{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	if strings.TrimSpace(args.DepartureID) == "" {
		return departureDTO{}, problemInvalidArgument("departureId is required")
	}
	v, err := d.Departures.GetDeparture(ctx, args.DepartureID)
	if err != nil {
		return departureDTO{}, problemFromUseCase(err, args.DepartureID)
	}
	return toDepartureDTO(v), nil
}

// problemFromUseCase maps a use case error to a problem answer, using the
// SAME classification and slugs as the REST adapter (internal/adapters/inbound/http
// problemCatalogue) for the errors a read can raise: a missing departure is
// 404 not-found, a rejected query is 422 validation-failed and anything
// unrecognised is 500 internal-error. (The REST adapter's 409 and the other
// 422 classes belong to writes this read-only agent does not offer.) The
// not-found and internal details are adapter-owned text, so neither the shared
// error's wording nor infrastructure detail (DSNs, SQL) ever reaches an agent
// client; the cause of a 500 is logged.
func problemFromUseCase(err error, departureID string) *problem {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return newProblem(http.StatusNotFound, "not-found", "Not found", fmt.Sprintf("no departure exists with departureId %q", departureID))
	case errors.Is(err, shared.ErrValidation):
		return newProblem(http.StatusUnprocessableEntity, "validation-failed", "Validation failed", strings.TrimPrefix(err.Error(), shared.ErrValidation.Error()+": "))
	default:
		slog.Error("a2a skill failed", "error", err)
		return newProblem(http.StatusInternalServerError, "internal-error", "Internal server error", "the skill failed; see server logs (trace id in the response metadata)")
	}
}
