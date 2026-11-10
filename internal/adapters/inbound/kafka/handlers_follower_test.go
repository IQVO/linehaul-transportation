package kafka

import "testing"

func lockedPayload(asg, into string) map[string]any {
	d := map[string]any{
		"assignmentId": asg, "orderId": "or-" + asg, "groupNo": 1, "promiseId": "pr-" + asg,
		"nodeId": "fc-1", "pickupDepartureId": "dep-missing", "unknownField": 7,
	}
	if into != "" {
		d["consolidatedInto"] = into
		d["mergedAssignmentIds"] = []string{"as-1", "as-2"}
	}
	return d
}

// The carrier lock of a merged package books (here: reports BookingFailed on an
// unknown departure); the follower lock decodes consolidatedInto, ignores
// mergedAssignmentIds and leaves only its inbox row (ADR 0014).
func TestOrderSourcingHandlers_FollowerLockOnlyClaimsTheEvent(t *testing.T) {
	r := newRig(t)
	r.mustSend("order", TypeLocked, "follower-1", lockedPayload("as-2", "as-1"))
	if c := r.store.Counts(); c.Volumes != 0 || c.OutboxEvents != 0 || c.Processed != 1 {
		t.Fatalf("counts after a follower lock = %+v (want only the inbox row)", c)
	}
	before := r.store.Counts()
	r.mustSend("order", TypeLocked, "follower-1", lockedPayload("as-2", "as-1"))
	if after := r.store.Counts(); after != before {
		t.Fatalf("redelivery changed the store: %+v -> %+v", before, after)
	}
}

func TestOrderSourcingHandlers_CarrierLockOfAMergedPackageIsBooked(t *testing.T) {
	r := newRig(t)
	r.mustSend("order", TypeLocked, "follower-1", lockedPayload("as-2", "as-1"))
	r.mustSend("order", TypeLocked, "carrier-1", lockedPayload("as-1", ""))
	if c := r.store.Counts(); c.Volumes != 1 || c.OutboxEvents != 1 || c.Processed != 2 {
		t.Fatalf("counts = %+v (want the carrier row, one BookingFailed, two inbox rows)", c)
	}
}
