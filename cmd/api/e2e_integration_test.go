//go:build integration

package main

// The real-wiring test boots run(ctx), the production entry point, against a
// real Postgres and a real Kafka broker (both testcontainers). It is driven
// ONLY through REST and through Kafka messages on the producers' topics, and it
// reads the result ONLY from Postgres and from the output topics. Nothing here
// is a double: the consumers, the use cases, the Postgres repositories, the
// Idempotency-Key middleware, the outbox relay, the encoders and the Kafka
// writer are the ones the binary runs.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/linehaul-transportation/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/linehaul-transportation/internal/testing/kafkatest"
	"github.com/claudioed/linehaul-transportation/internal/testing/pgtest"
)

const (
	eventsTopic    = "retailnetwork.linehaul-transportation.events"
	analyticsTopic = "retailnetwork.linehaul-transportation.analytics"

	nodeTopologyTopic  = "retailnetwork.node-topology.events"
	orderSourcingTopic = "retailnetwork.order-sourcing.events"
	shippingTopic      = "retailnetwork.shipping-and-tracking.events"

	typeNode     = "com.retailnetwork.logistics.node-topology.node."
	typeLane     = "com.retailnetwork.logistics.node-topology.lane."
	typeAssign   = "com.retailnetwork.commerce.order-sourcing.assignment."
	typeShipping = "com.retailnetwork.logistics.shipping-and-tracking.label."
)

// service is the running binary under test.
type service struct {
	t      *testing.T
	base   string
	pool   *pgtest.Pool
	cancel context.CancelFunc
	done   chan error
	keys   int
	events int
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

// boot starts run(ctx) on a fresh database (no migration applied: the binary
// migrates at boot) and waits until /healthz answers.
func boot(t *testing.T) *service {
	t.Helper()
	dbURL := pgtest.EmptyURL(t)
	addr := freeAddr(t)
	for _, topic := range []string{nodeTopologyTopic, orderSourcingTopic, shippingTopic} {
		kafkatest.CreateTopic(t, topic)
	}
	t.Setenv("DATABASE_URL", dbURL)
	kafkatest.SetBrokersEnv(t)
	t.Setenv("NODE_TOPOLOGY_CONSUMER_GROUP", "linehaul-e2e-node-topology")
	t.Setenv("ORDER_SOURCING_CONSUMER_GROUP", "linehaul-e2e-order-sourcing")
	t.Setenv("SHIPPING_AND_TRACKING_CONSUMER_GROUP", "linehaul-e2e-shipping")
	t.Setenv("HTTP_ADDR", addr)
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0")
	t.Setenv("OUTBOX_RELAY_INTERVAL", "100ms")
	t.Setenv("GENERATE_INTERVAL", "500ms")
	t.Setenv("LOG_LEVEL", "error")

	ctx, cancel := context.WithCancel(context.Background())
	s := &service{t: t, base: "http://" + addr, cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- run(ctx) }()
	t.Cleanup(func() { cancel(); s.waitStopped(30 * time.Second) })

	s.eventually(60*time.Second, "healthz", func() bool { return s.get("/healthz").status == http.StatusOK })
	s.pool = pgtest.OpenPool(t, dbURL)
	return s
}

func (s *service) waitStopped(timeout time.Duration) error {
	select {
	case err := <-s.done:
		s.done <- err
		return err
	case <-time.After(timeout):
		s.t.Errorf("run(ctx) did not return within %s of the cancellation", timeout)
		return nil
	}
}

func (s *service) eventually(timeout time.Duration, what string, ok func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *service) call(method, path, body, key string) response {
	s.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.base+path, rd)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return response{status: -1, body: []byte(err.Error())}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: raw}
}

func (s *service) get(path string) response { return s.call(http.MethodGet, path, "", "") }

// getJSON reads a 200 JSON document.
func (s *service) getJSON(path string) map[string]any {
	s.t.Helper()
	r := s.get(path)
	if r.status != http.StatusOK {
		s.t.Fatalf("GET %s = %d %s", path, r.status, r.body)
	}
	var out map[string]any
	if err := json.Unmarshal(r.body, &out); err != nil {
		s.t.Fatal(err)
	}
	return out
}

// write sends a state-changing request under a fresh key and requires the status.
func (s *service) write(method, path, body string, want int) map[string]any {
	s.t.Helper()
	s.keys++
	r := s.call(method, path, body, fmt.Sprintf("e2e-%d", s.keys))
	if r.status != want {
		s.t.Fatalf("%s %s = %d %s, want %d", method, path, r.status, r.body, want)
	}
	var out map[string]any
	_ = json.Unmarshal(r.body, &out)
	return out
}

func (s *service) count(query string) int {
	s.t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		s.t.Fatalf("%s: %v", query, err)
	}
	return n
}

func str(m map[string]any, k string) string { v, _ := m[k].(string); return v }

// send publishes one CloudEvents message of a PRODUCER's type on its topic.
func (s *service) send(topic, producer, typ string, data any) string {
	s.t.Helper()
	s.events++
	id := fmt.Sprintf("e2e-ev-%d", s.events)
	s.sendWithID(topic, producer, typ, id, data)
	return id
}

func (s *service) sendWithID(topic, producer, typ, id string, data any) {
	s.t.Helper()
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(id)
	e.SetSource("/retailnetwork/" + producer)
	e.SetType(typ)
	e.SetSubject("subject-1")
	e.SetTime(time.Now().UTC())
	if err := e.SetData("application/json", data); err != nil {
		s.t.Fatal(err)
	}
	b, err := e.MarshalJSON()
	if err != nil {
		s.t.Fatal(err)
	}
	kafkatest.Produce(s.t, topic, kafkatest.Message{Key: "subject-1", Value: b, Headers: map[string]string{"content-type": cloudevents.MediaType}})
}

type departureView struct {
	ID, Kind, State, Origin, Destination string
	Cutoff                               time.Time
	Capacity, Booked                     int
}

// departures lists the departures from an origin, earliest cutoff first.
func (s *service) departures(origin string) []departureView {
	s.t.Helper()
	doc := s.getJSON("/departures?origin=" + origin)
	list, _ := doc["departures"].([]any)
	var out []departureView
	for _, raw := range list {
		m, _ := raw.(map[string]any)
		cutoff, _ := time.Parse(time.RFC3339, str(m, "cutoffAt"))
		capacity, _ := m["capacityPackages"].(float64)
		booked, _ := m["bookedPackages"].(float64)
		out = append(out, departureView{ID: str(m, "departureId"), Kind: str(m, "kind"), State: str(m, "state"), Origin: str(m, "originNodeId"),
			Destination: str(m, "destinationNodeId"), Cutoff: cutoff, Capacity: int(capacity), Booked: int(booked)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cutoff.Before(out[j].Cutoff) })
	return out
}

// scheduled waits for at least n SCHEDULED departures of a kind from origin.
func (s *service) scheduled(origin, kind string, n int) []departureView {
	s.t.Helper()
	var got []departureView
	s.eventually(60*time.Second, fmt.Sprintf("%d %s departures from %s", n, kind, origin), func() bool {
		got = got[:0]
		for _, d := range s.departures(origin) {
			if d.Kind == kind && d.State == "SCHEDULED" {
				got = append(got, d)
			}
		}
		return len(got) >= n
	})
	return got
}

func (s *service) departure(id string) map[string]any { return s.getJSON("/departures/" + id) }

// world is what the REST and Kafka flow created.
type world struct {
	pickup1, pickup2, pickup3, linehaul string
	pickupLoad, lineLoad                string
}

func TestRealWiring(t *testing.T) {
	s := boot(t)

	t.Run("ready while the output topics do not exist", func(t *testing.T) {
		r := s.get("/readyz")
		if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"status":"ready"}` {
			t.Fatalf("/readyz = %d %s, want 200 ready", r.status, r.body)
		}
	})

	var w world
	t.Run("node-topology events produce pickup departures", func(t *testing.T) { w = networkFlow(t, s) })
	t.Run("order-sourcing and shipping events book and rebook", func(t *testing.T) { bookingFlow(t, s, &w) })
	t.Run("a pickup load is dispatched and received", func(t *testing.T) { pickupLoadFlow(t, s, &w) })
	t.Run("a lane schedule produces line-haul departures that take a load", func(t *testing.T) { lineHaulFlow(t, s, &w) })
	t.Run("idempotent REST replay and idempotent consumers", func(t *testing.T) { assertIdempotency(t, s, w) })
	t.Run("exact CloudEvents on the output topics", func(t *testing.T) { assertTopics(t, s, w) })
	t.Run("Kafka down", func(t *testing.T) { assertKafkaDown(t, s) })
}

const vendorProfile = `{"nodeId":"fc-1","profileRevision":1,"timezone":"America/Sao_Paulo","operatingHoursByDay":{` +
	`"monday":{"start":"06:00","end":"22:00"},"tuesday":{"start":"06:00","end":"22:00"},"wednesday":{"start":"06:00","end":"22:00"},` +
	`"thursday":{"start":"06:00","end":"22:00"},"friday":{"start":"06:00","end":"22:00"}},"holidays":[],"handlingTimeMinutes":45,` +
	`"cutoffs":[{"localTime":"18:00","days":["monday","tuesday","wednesday","thursday","friday"],"maxOrders":40}],` +
	`"throughputCap":{"value":10,"timeUnit":"HOUR","unit":"ORDER"},"declaredBy":"VENDOR"}`

func networkFlow(t *testing.T, s *service) world {
	t.Helper()
	register := func(id, typ, operator string) {
		s.send(nodeTopologyTopic, "node-topology", typeNode+"NodeRegistered", map[string]any{
			"nodeId": id, "nodeType": typ, "subtype": nil, "operator": operator, "vendorId": nil, "vendorNodeCode": nil,
			"regionId": "reg-1", "timezone": "America/Sao_Paulo", "postalCode": "01310-100", "countryCode": "BR",
		})
	}
	register("fc-1", "FULFILLMENT_CENTER", "VENDOR")
	register("sc-1", "SORTATION_CENTER", "NETWORK")
	register("ds-1", "DELIVERY_STATION", "NETWORK")
	var profile map[string]any
	if err := json.Unmarshal([]byte(vendorProfile), &profile); err != nil {
		t.Fatal(err)
	}
	s.send(nodeTopologyTopic, "node-topology", typeNode+"NodeOperationalProfileDeclared", profile)
	for _, id := range []string{"fc-1", "sc-1", "ds-1"} {
		s.send(nodeTopologyTopic, "node-topology", typeNode+"NodeActivated", map[string]any{"nodeId": id, "activatedAt": time.Now().UTC().Format(time.RFC3339)})
	}
	s.send(nodeTopologyTopic, "node-topology", typeLane+"LaneOpened", map[string]any{
		"laneId": "ln-1", "originNodeId": "fc-1", "destinationNodeId": "sc-1", "mode": "GROUND", "nominalTransitMinutes": 120,
	})
	s.send(nodeTopologyTopic, "node-topology", typeLane+"LaneOpened", map[string]any{
		"laneId": "ln-2", "originNodeId": "sc-1", "destinationNodeId": "ds-1", "mode": "GROUND", "nominalTransitMinutes": 90,
	})

	pickups := s.scheduled("fc-1", "PICKUP", 4)
	for _, p := range pickups {
		if p.Destination != "sc-1" || p.Capacity < 1 {
			t.Fatalf("pickup %+v: want a pickup toward sc-1 with capacity", p)
		}
	}
	return world{pickup1: pickups[0].ID, pickup2: pickups[1].ID, pickup3: pickups[2].ID}
}

func lockedData(assignment, departureID string) map[string]any {
	return map[string]any{
		"assignmentId": assignment, "orderId": "or-" + assignment, "groupNo": 1, "promiseId": "pr-" + assignment, "nodeId": "fc-1",
		"pickupDepartureId": departureID, "lockedAt": time.Now().UTC().Format(time.RFC3339), "lockReason": "PURCHASE_ORDER_ACCEPTED",
	}
}

func bookingFlow(t *testing.T, s *service, w *world) {
	t.Helper()
	s.send(orderSourcingTopic, "order-sourcing", typeAssign+"FulfillmentReleasedToNode", map[string]any{
		"assignmentId": "as-1", "releaseNo": 1, "orderId": "or-as-1", "groupNo": 1, "nodeId": "fc-1", "pickupDepartureId": w.pickup1,
		"requiredShipBy": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339), "promisedDeliverByLatest": time.Now().Add(96 * time.Hour).UTC().Format(time.RFC3339),
		"tier": "STANDARD", "lines": []any{map[string]any{"catalogItemId": "ci-1", "listingId": "li-1", "quantity": 1}},
	})
	s.send(shippingTopic, "shipping-and-tracking", typeShipping+"LabelIssued", map[string]any{
		"labelRef": "lb-1", "trackingNumber": "TRK1", "poNumber": "po-1", "assignmentId": "as-1", "orderId": "or-as-1", "packageNo": 1,
		"tier": "STANDARD", "carrier": "own", "stopKey": "sk-1", "destinationPostalPrefix": "013",
	})
	s.send(orderSourcingTopic, "order-sourcing", typeAssign+"FulfillmentLocked", lockedData("as-1", w.pickup1))
	s.send(orderSourcingTopic, "order-sourcing", typeAssign+"FulfillmentLocked", lockedData("as-2", w.pickup1))
	s.eventually(60*time.Second, "two packages booked on the first pickup", func() bool {
		return s.departure(w.pickup1)["bookedPackages"] == float64(2)
	})
	s.eventually(60*time.Second, "TRK1 recorded against as-1", func() bool {
		return s.count(`SELECT count(*) FROM expected_volumes WHERE 'TRK1' = ANY(tracking_numbers)`) == 1
	})

	// Cancelling the first pickup rebooks both assignments onto the second.
	if d := s.write(http.MethodPost, "/departures/"+w.pickup1+":cancel", `{"reason":"road closed"}`, http.StatusOK); str(d, "state") != "CANCELLED" {
		t.Fatalf("cancel = %v", d)
	}
	if got := s.departure(w.pickup2); got["bookedPackages"] != float64(2) {
		t.Fatalf("second pickup after the rebooking = %v, want 2 booked", got)
	}
	// A booking on a cancelled departure moves on its own.
	s.send(orderSourcingTopic, "order-sourcing", typeAssign+"FulfillmentLocked", lockedData("as-3", w.pickup1))
	s.eventually(60*time.Second, "the late lock rebooked", func() bool {
		return s.departure(w.pickup2)["bookedPackages"] == float64(3)
	})
}

func pickupLoadFlow(t *testing.T, s *service, w *world) {
	t.Helper()
	w.pickupLoad = str(s.write(http.MethodPost, "/loads", `{"departureId":"`+w.pickup2+`"}`, http.StatusCreated), "loadId")
	if l := s.write(http.MethodPost, "/loads/"+w.pickupLoad+"/packages", `{"trackingNumber":"TRK1"}`, http.StatusOK); l["packageCount"] != float64(1) {
		t.Fatalf("add package = %v", l)
	}
	if l := s.write(http.MethodPost, "/loads/"+w.pickupLoad+":close", "", http.StatusOK); str(l, "state") != "CLOSED" {
		t.Fatalf("close = %v", l)
	}
	l := s.write(http.MethodPost, "/loads/"+w.pickupLoad+":dispatch", "", http.StatusOK)
	if str(l, "state") != "DEPARTED" || str(l, "departureKind") != "PICKUP" {
		t.Fatalf("dispatch = %v", l)
	}
	if l = s.write(http.MethodPost, "/loads/"+w.pickupLoad+":receive", "", http.StatusOK); str(l, "state") != "ARRIVED" {
		t.Fatalf("receive = %v", l)
	}
	if d := s.departure(w.pickup2); str(d, "state") != "ARRIVED" {
		t.Fatalf("pickup after the receive = %v", d)
	}
	if r := s.call(http.MethodPost, "/loads/"+w.pickupLoad+"/packages", `{"trackingNumber":"TRK9"}`, "late-add"); r.status != http.StatusConflict ||
		!strings.Contains(string(r.body), "illegal-transition") {
		t.Fatalf("add to an ARRIVED load = %d %s, want 409 illegal-transition", r.status, r.body)
	}
}

func lineHaulFlow(t *testing.T, s *service, w *world) {
	t.Helper()
	slots := make([]string, 0, 7)
	for _, d := range []string{"MONDAY", "TUESDAY", "WEDNESDAY", "THURSDAY", "FRIDAY", "SATURDAY", "SUNDAY"} {
		slots = append(slots, `{"dayOfWeek":"`+d+`","cutoffLocalTime":"20:00","departsLocalTime":"21:00","arrivesLocalTime":"23:00","capacityPackages":5}`)
	}
	body := `{"laneId":"ln-2","slots":[` + strings.Join(slots, ",") + `]}`
	if sc := s.write(http.MethodPost, "/lane-schedules", body, http.StatusCreated); str(sc, "laneId") != "ln-2" {
		t.Fatalf("define schedule = %v", sc)
	}
	// The horizon roll (every 500ms in this test) expands the schedule.
	line := s.scheduled("sc-1", "LINEHAUL", 1)
	if line[0].Destination != "ds-1" || line[0].Capacity != 5 {
		t.Fatalf("line-haul departure = %+v", line[0])
	}
	w.linehaul = line[0].ID

	w.lineLoad = str(s.write(http.MethodPost, "/loads", `{"departureId":"`+w.linehaul+`"}`, http.StatusCreated), "loadId")
	s.write(http.MethodPost, "/loads/"+w.lineLoad+"/packages", `{"trackingNumber":"TRK2"}`, http.StatusOK)
	if d := s.departure(w.linehaul); d["bookedPackages"] != float64(1) {
		t.Fatalf("a line-haul package is booked on its departure, got %v", d)
	}
	s.write(http.MethodPost, "/loads/"+w.lineLoad+":close", "", http.StatusOK)
	s.write(http.MethodPost, "/loads/"+w.lineLoad+":dispatch", "", http.StatusOK)
	if l := s.write(http.MethodPost, "/loads/"+w.lineLoad+":receive", "", http.StatusOK); str(l, "state") != "ARRIVED" {
		t.Fatalf("receive = %v", l)
	}
	if r := s.call(http.MethodPost, "/lane-schedules/ln-2:retire", "", "retire-1"); r.status != http.StatusOK {
		t.Fatalf("retire = %d %s", r.status, r.body)
	}
}

func assertIdempotency(t *testing.T, s *service, w world) {
	t.Helper()
	body := `{"departureId":"` + w.pickup3 + `"}`
	first := s.call(http.MethodPost, "/loads", body, "replay-1")
	second := s.call(http.MethodPost, "/loads", body, "replay-1")
	if first.status != http.StatusCreated || first.status != second.status || !bytes.Equal(first.body, second.body) {
		t.Fatalf("first %d %s, replay %d %s", first.status, first.body, second.status, second.body)
	}
	if r := s.call(http.MethodPost, "/loads", `{"departureId":"other"}`, "replay-1"); r.status != http.StatusUnprocessableEntity ||
		!strings.Contains(string(r.body), "idempotency-key-reused") {
		t.Fatalf("conflicting body = %d %s, want 422 idempotency-key-reused", r.status, r.body)
	}
	if r := s.call(http.MethodPost, "/loads", body, ""); r.status != http.StatusBadRequest {
		t.Fatalf("no key = %d, want 400", r.status)
	}

	// The same CloudEvents id delivered twice is applied once.
	before := s.count(`SELECT count(*) FROM outbox_events`)
	id := s.send(orderSourcingTopic, "order-sourcing", typeAssign+"FulfillmentLocked", lockedData("as-4", w.pickup1))
	s.eventually(60*time.Second, "as-4 booked", func() bool {
		return s.count(`SELECT count(*) FROM processed_events WHERE event_id = '`+id+`'`) == 1
	})
	s.sendWithID(orderSourcingTopic, "order-sourcing", typeAssign+"FulfillmentLocked", id, lockedData("as-4", w.pickup1))
	s.send(shippingTopic, "shipping-and-tracking", typeShipping+"LabelVoided", map[string]any{"labelRef": "lb-x", "trackingNumber": "UNKNOWN", "reason": "test"})
	s.eventually(60*time.Second, "the barrier event processed", func() bool {
		return s.count(`SELECT count(*) FROM processed_events WHERE consumer = 'shipping-and-tracking'`) >= 2
	})
	after := s.count(`SELECT count(*) FROM outbox_events`)
	if got := s.count(`SELECT count(*) FROM processed_events WHERE event_id = '` + id + `'`); got != 1 {
		t.Fatalf("event %s claimed %d times", id, got)
	}
	// as-4 locked on the cancelled first pickup, so its booking moved to the
	// next SCHEDULED one (the third) exactly once; the second is untouched.
	var bookedOn string
	if err := s.pool.QueryRow(context.Background(), `SELECT booked_on FROM expected_volumes WHERE assignment_id = 'as-4'`).Scan(&bookedOn); err != nil || bookedOn != w.pickup3 {
		t.Fatalf("as-4 is booked on %q (err %v), want the third pickup %s", bookedOn, err, w.pickup3)
	}
	if got := s.departure(w.pickup3)["bookedPackages"]; got != float64(1) || s.departure(w.pickup2)["bookedPackages"] != float64(3) || after-before != 2 {
		t.Fatalf("the redelivery changed state: third pickup %v booked, second %v, outbox grew by %d (want 1, 3, 2)", got, s.departure(w.pickup2)["bookedPackages"], after-before)
	}
}

type outboxRow struct {
	id                        int64
	eventID, topic, typ, subj string
	value                     []byte
}

func outboxRows(t *testing.T, s *service) []outboxRow {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), `SELECT id, event_id, topic, event_type, subject, value FROM outbox_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.id, &r.eventID, &r.topic, &r.typ, &r.subj, &r.value); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func assertTopics(t *testing.T, s *service, w world) {
	t.Helper()
	s.eventually(30*time.Second, "all outbox rows published", func() bool {
		return s.count(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`) == 0
	})
	rows := outboxRows(t, s)
	byTopic := map[string][]outboxRow{}
	for _, r := range rows {
		byTopic[r.topic] = append(byTopic[r.topic], r)
	}
	if len(byTopic) != 2 || len(byTopic[eventsTopic]) == 0 || len(byTopic[analyticsTopic]) == 0 {
		t.Fatalf("outbox topics = %v, want exactly the events and the analytics topic", keys(byTopic))
	}
	got := map[string][]kafkatest.Message{}
	for topic, rs := range byTopic {
		got[topic] = kafkatest.ReadN(t, topic, len(rs), 90*time.Second)
		for i, msg := range got[topic] {
			assertMessage(t, msg, rs[i])
		}
		if n := kafkatest.Count(t, topic); int(n) != len(rs) {
			t.Errorf("topic %s holds %d messages, want exactly %d", topic, n, len(rs))
		}
	}
	assertAnalyticsOnly(t, byTopic)
	assertPayloads(t, byTopic, w)
}

func keys(m map[string][]outboxRow) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func assertMessage(t *testing.T, msg kafkatest.Message, row outboxRow) {
	t.Helper()
	e, err := cloudevents.Decode(msg.Value)
	if err != nil {
		t.Fatalf("%s: %v", row.typ, err)
	}
	if !bytes.Equal(msg.Value, row.value) {
		t.Errorf("%s: the message is not the stored outbox bytes", row.typ)
	}
	if !strings.HasPrefix(e.Type(), "com.retailnetwork.logistics.linehaul-transportation.") {
		t.Errorf("%s: type %q is not in this context's namespace", row.typ, e.Type())
	}
	if e.Type() != row.typ || e.ID() != row.eventID || e.Subject() != row.subj || msg.Key != row.subj {
		t.Errorf("%s: type %q id %q subject %q key %q, want the outbox row %+v", row.typ, e.Type(), e.ID(), e.Subject(), msg.Key, row)
	}
	assertAttributes(t, e, row)
	if len(msg.Headers) != 1 || msg.Headers["content-type"] != cloudevents.MediaType {
		t.Errorf("%s: headers %v", row.typ, msg.Headers)
	}
}

func assertAttributes(t *testing.T, e ce.Event, row outboxRow) {
	t.Helper()
	name := row.typ[strings.LastIndex(row.typ, ".")+1:]
	stream := "events"
	if row.topic == analyticsTopic {
		stream = "analytics"
	}
	if e.Source() != "/retailnetwork/linehaul-transportation" || e.DataContentType() != "application/json" ||
		e.DataSchema() != "urn:retailnetwork:linehaul-transportation:"+stream+":"+name+":v1" || e.SpecVersion() != "1.0" {
		t.Errorf("%s: attributes source %q dct %q dataschema %q spec %q", row.typ, e.Source(), e.DataContentType(), e.DataSchema(), e.SpecVersion())
	}
	if e.Time().IsZero() || e.Time().Location() != time.UTC {
		t.Errorf("%s: time %v is not a UTC instant", row.typ, e.Time())
	}
}

// assertAnalyticsOnly checks the stream of every type: the analytics events go
// ONLY to the analytics topic, every integration event ONLY to the events topic.
func assertAnalyticsOnly(t *testing.T, byTopic map[string][]outboxRow) {
	t.Helper()
	const prefix = "com.retailnetwork.logistics.linehaul-transportation."
	analytics := map[string]bool{prefix + "departure.DepartureDeparted": true, prefix + "departure.DepartureArrived": true, prefix + "load.LoadClosed": true}
	seen := map[string]bool{}
	for topic, rs := range byTopic {
		for _, r := range rs {
			seen[r.typ] = true
			if analytics[r.typ] != (topic == analyticsTopic) {
				t.Errorf("%s was written to %s", r.typ, topic)
			}
		}
	}
	for _, want := range []string{"departure.DepartureScheduled", "departure.DepartureCapacityChanged", "departure.DepartureCancelled", "departure.BookingMoved",
		"departure.DepartureDeparted", "departure.DepartureArrived", "load.LoadClosed", "load.LoadDeparted", "load.LoadArrived"} {
		if !seen[prefix+want] {
			t.Errorf("the flow published no %s", want)
		}
	}
}

func dataOf(t *testing.T, r outboxRow) map[string]any {
	t.Helper()
	e, err := cloudevents.Decode(r.value)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(e.Data(), &data); err != nil {
		t.Fatalf("data of %s: %v", e.Type(), err)
	}
	return data
}

// payloads finds the data of the events whose shape matters most to the
// consumers (contracts-delta E3 and E13).
type payloads struct {
	t       *testing.T
	byTopic map[string][]outboxRow
}

const eventPrefix = "com.retailnetwork.logistics.linehaul-transportation."

func (p payloads) find(typ string, pred func(map[string]any) bool) map[string]any {
	p.t.Helper()
	for _, r := range p.byTopic[eventsTopic] {
		if r.typ == eventPrefix+typ {
			if d := dataOf(p.t, r); pred(d) {
				return d
			}
		}
	}
	p.t.Errorf("no %s matching the flow", typ)
	return nil
}

func assertPayloads(t *testing.T, byTopic map[string][]outboxRow, w world) {
	t.Helper()
	p := payloads{t: t, byTopic: byTopic}
	p.pickupDeparted(w)
	p.lineHaulArrived(w)
	p.bookingMoved(w)
	p.departureCancelled(w)
}

func (p payloads) pickupDeparted(w world) {
	d := p.find("load.LoadDeparted", func(d map[string]any) bool { return str(d, "loadId") == w.pickupLoad })
	if d == nil {
		return
	}
	if str(d, "departureKind") != "PICKUP" || str(d, "nodeType") != "FULFILLMENT_CENTER" || str(d, "originNodeId") != "fc-1" || str(d, "departureId") != w.pickup2 {
		p.t.Errorf("pickup LoadDeparted data = %v", d)
	}
	if tn, _ := d["trackingNumbers"].([]any); len(tn) != 1 || tn[0] != "TRK1" {
		p.t.Errorf("pickup LoadDeparted trackingNumbers = %v", d["trackingNumbers"])
	}
}

func (p payloads) lineHaulArrived(w world) {
	d := p.find("load.LoadArrived", func(d map[string]any) bool { return str(d, "loadId") == w.lineLoad })
	if d != nil && (str(d, "departureKind") != "LINEHAUL" || str(d, "nodeType") != "DELIVERY_STATION" || str(d, "destinationNodeId") != "ds-1") {
		p.t.Errorf("line-haul LoadArrived data = %v", d)
	}
}

func (p payloads) bookingMoved(w world) {
	d := p.find("departure.BookingMoved", func(d map[string]any) bool { return str(d, "assignmentId") == "as-1" })
	if d != nil && (str(d, "fromDepartureId") != w.pickup1 || str(d, "toDepartureId") != w.pickup2) {
		p.t.Errorf("BookingMoved data = %v", d)
	}
}

func (p payloads) departureCancelled(w world) {
	d := p.find("departure.DepartureCancelled", func(d map[string]any) bool { return str(d, "departureId") == w.pickup1 })
	if d != nil && str(d, "reason") != "road closed" {
		p.t.Errorf("DepartureCancelled data = %v", d)
	}
}

// assertKafkaDown stops the broker: /readyz must answer 503
// dependency-unavailable (the metadata check dials a fresh connection, so a
// cached answer cannot hide the outage), /healthz stays 200, a write is still
// accepted (the outbox decouples it from Kafka) and the process shuts down
// cleanly.
func assertKafkaDown(t *testing.T, s *service) {
	t.Helper()
	kafkatest.Stop(t)

	var last response
	s.eventually(60*time.Second, "/readyz 503", func() bool {
		last = s.get("/readyz")
		return last.status == http.StatusServiceUnavailable
	})
	if !strings.HasPrefix(last.header.Get("Content-Type"), "application/problem+json") || !strings.Contains(string(last.body), `/problems/dependency-unavailable"`) ||
		!strings.Contains(string(last.body), "kafka") || strings.Contains(string(last.body), "postgres") {
		t.Fatalf("/readyz = %d %s, want 503 dependency-unavailable naming only kafka", last.status, last.body)
	}
	if h := s.get("/healthz"); h.status != http.StatusOK {
		t.Fatalf("/healthz = %d with Kafka down, want 200 (liveness)", h.status)
	}
	free := s.scheduled("fc-1", "PICKUP", 4)
	s.write(http.MethodPost, "/departures/"+free[len(free)-1].ID+":cancel", `{"reason":"kafka is down"}`, http.StatusOK)
	if pending := s.count(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`); pending < 1 {
		t.Fatalf("a write with Kafka down left %d unpublished outbox rows, want it queued", pending)
	}

	s.cancel()
	if err := s.waitStopped(30 * time.Second); err != nil {
		t.Fatalf("run(ctx) returned %v on a cancellation, want a clean shutdown", err)
	}
	if r := s.get("/healthz"); r.status != -1 {
		t.Fatalf("the listener still answers after the shutdown: %d", r.status)
	}
}
