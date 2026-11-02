package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/linehaul-transportation/internal/application/usecases"
)

type slotBody struct {
	DayOfWeek        string `json:"dayOfWeek"`
	CutoffLocalTime  string `json:"cutoffLocalTime"`
	DepartsLocalTime string `json:"departsLocalTime"`
	ArrivesLocalTime string `json:"arrivesLocalTime"`
	CapacityPackages int    `json:"capacityPackages"`
}

type defineLaneScheduleBody struct {
	LaneID string     `json:"laneId"`
	Slots  []slotBody `json:"slots"`
}

type cancelBody struct {
	Reason string `json:"reason"`
}

type capacityBody struct {
	CapacityPackages int `json:"capacityPackages"`
}

type openLoadBody struct {
	DepartureID string `json:"departureId"`
}

type addPackageBody struct {
	TrackingNumber string `json:"trackingNumber"`
}

// handleDefineLaneSchedule: 201 for a new schedule, 200 when the slots of an
// ACTIVE schedule were replaced.
func (s *Server) handleDefineLaneSchedule(w http.ResponseWriter, r *http.Request) {
	var b defineLaneScheduleBody
	if err := decodeBody(r, &b); err != nil {
		writeError(w, r, err)
		return
	}
	cmd := usecases.DefineLaneScheduleCommand{LaneID: b.LaneID, Slots: make([]usecases.SlotInput, len(b.Slots))}
	for i, sl := range b.Slots {
		cmd.Slots[i] = usecases.SlotInput(sl)
	}
	v, created, err := s.Service.DefineLaneSchedule(r.Context(), cmd)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, v)
}

func (s *Server) handleRetireLaneSchedule(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.RetireLaneSchedule(r.Context(), chi.URLParam(r, "laneId"))
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleGetDeparture(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.GetDeparture(r.Context(), chi.URLParam(r, "departureId"))
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleCancelDeparture(w http.ResponseWriter, r *http.Request) {
	var b cancelBody
	if err := decodeBody(r, &b); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Service.CancelDeparture(r.Context(), chi.URLParam(r, "departureId"), b.Reason)
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleChangeDepartureCapacity(w http.ResponseWriter, r *http.Request) {
	var b capacityBody
	if err := decodeBody(r, &b); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Service.ChangeDepartureCapacity(r.Context(), chi.URLParam(r, "departureId"), b.CapacityPackages)
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleOpenLoad(w http.ResponseWriter, r *http.Request) {
	var b openLoadBody
	if err := decodeBody(r, &b); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Service.OpenLoad(r.Context(), b.DepartureID)
	respond(w, r, http.StatusCreated, v, err)
}

func (s *Server) handleAddPackage(w http.ResponseWriter, r *http.Request) {
	var b addPackageBody
	if err := decodeBody(r, &b); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Service.AddPackage(r.Context(), chi.URLParam(r, "loadId"), b.TrackingNumber)
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleCloseLoad(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.CloseLoad(r.Context(), chi.URLParam(r, "loadId"))
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleDispatchLoad(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.DispatchLoad(r.Context(), chi.URLParam(r, "loadId"))
	respond(w, r, http.StatusOK, v, err)
}

func (s *Server) handleReceiveLoad(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.ReceiveLoad(r.Context(), chi.URLParam(r, "loadId"))
	respond(w, r, http.StatusOK, v, err)
}

// respond writes v with the success status, or err as its problem.
func respond(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, status, v)
}
