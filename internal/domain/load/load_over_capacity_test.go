package load

import (
	"testing"

	"github.com/claudioed/linehaul-transportation/internal/domain/departure"
)

// ADR 0015 classifies load-over-capacity as FINAL: nothing in the model can make
// the same request pass later. These tests pin the three facts that argument
// stands on (a full load stays full; the capacity is a snapshot of OpenLoad; a
// package already on the load is not an error), so a change that breaks one of
// them fails here and the class is revisited.
func TestAFullLoadStaysFullForANewTrackingNumber(t *testing.T) {
	d := dep(t, departure.KindLinehaul, 2)
	l, err := Open("load-1", d, false)
	if err != nil {
		t.Fatal(err)
	}
	add(t, l, "A")
	add(t, l, "B")

	for i := 0; i < 3; i++ {
		_, err := l.AddPackage("C", false, departure.StateScheduled)
		wantErr(t, "retry of a new tracking number on a full load", err, ErrLoadOverCapacity)
	}
	// A package that is already on the load changes nothing and is not refused.
	if added, err := l.AddPackage("A", false, departure.StateScheduled); added || err != nil {
		t.Fatalf("AddPackage(A) again = %v, %v, want a no-op", added, err)
	}
	if l.PackageCount() != 2 {
		t.Fatalf("packages = %d, want 2: nothing removes a package", l.PackageCount())
	}
	// The capacity is the snapshot taken at OpenLoad: the departure's capacity
	// changing afterwards does not reach the load.
	if _, err := d.ChangeCapacity(10, t0); err != nil {
		t.Fatal(err)
	}
	if l.Capacity() != 2 {
		t.Fatalf("load capacity = %d, want the snapshot 2", l.Capacity())
	}
	_, err = l.AddPackage("C", false, departure.StateScheduled)
	wantErr(t, "after the departure capacity changed", err, ErrLoadOverCapacity)
}

// Once the load leaves OPEN the same request is an illegal transition, never a
// success: over-capacity cannot turn into an accepted package.
func TestAFullLoadNeverAcceptsANewPackageAfterItLeavesOpen(t *testing.T) {
	l := openLoad(t, 1)
	add(t, l, "A")
	if _, err := l.Close(departure.StateScheduled, t0); err != nil {
		t.Fatal(err)
	}
	if added, err := l.AddPackage("B", false, departure.StateScheduled); added || err == nil {
		t.Fatalf("AddPackage(B) on a CLOSED load = %v, %v, want a refusal", added, err)
	}
}
