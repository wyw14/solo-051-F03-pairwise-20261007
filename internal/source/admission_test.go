package source

import (
	"context"
	"errors"
	"testing"
)

func TestReservationCommitAccounting(t *testing.T) {
	admission := NewAdmission(10)
	reservation, err := admission.Reserve(context.Background(), 4)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if got := admission.Reserved(); got != 4 {
		t.Fatalf("reserved after reserve = %d, want 4", got)
	}
	if got := admission.Used(); got != 0 {
		t.Fatalf("used after reserve = %d, want 0", got)
	}
	if err := reservation.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := admission.Used(); got != 4 {
		t.Fatalf("used after commit = %d, want 4", got)
	}
	if got := admission.Reserved(); got != 0 {
		t.Fatalf("reserved after commit = %d, want 0", got)
	}
	reservation.Release()
	if got := admission.Used(); got != 0 {
		t.Fatalf("used after release = %d, want 0", got)
	}
}

func TestReservationDuplicateCommitDoesNotIncreaseUsage(t *testing.T) {
	admission := NewAdmission(10)
	reservation, err := admission.Reserve(context.Background(), 3)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := reservation.Commit(); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}
	if got := admission.Used(); got != 3 {
		t.Fatalf("used after duplicate commits = %d, want 3", got)
	}
}

func TestReservationCommitAfterReleaseRejected(t *testing.T) {
	admission := NewAdmission(10)
	reservation, err := admission.Reserve(context.Background(), 3)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	reservation.Release()
	if err := reservation.Commit(); !errors.Is(err, ErrReservationReleased) {
		t.Fatalf("commit after release = %v, want ErrReservationReleased", err)
	}
	if got := admission.Used(); got != 0 {
		t.Fatalf("used = %d, want 0", got)
	}
}

func TestReservationReleaseRecyclesUncommittedQuota(t *testing.T) {
	admission := NewAdmission(5)
	reservation, err := admission.Reserve(context.Background(), 5)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	reservation.Release()
	reservation.Release()
	if got := admission.Reserved(); got != 0 {
		t.Fatalf("reserved after release = %d, want 0", got)
	}
	// The recycled quota is available to a later reservation.
	if _, err := admission.Reserve(context.Background(), 5); err != nil {
		t.Fatalf("reserve after recycle: %v", err)
	}
}

func TestRecoverRejectsStaleReservation(t *testing.T) {
	admission := NewAdmission(10)
	stale, err := admission.Reserve(context.Background(), 4)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	admission.Recover()
	if got := admission.Reserved(); got != 0 {
		t.Fatalf("reserved after recover = %d, want 0", got)
	}
	if err := stale.Commit(); !errors.Is(err, ErrStaleReservation) {
		t.Fatalf("stale commit = %v, want ErrStaleReservation", err)
	}
	if got := admission.Used(); got != 0 {
		t.Fatalf("used after stale commit = %d, want 0", got)
	}
	// Releasing the stale reservation must not reclaim quota twice.
	stale.Release()
	if got := admission.Reserved(); got != 0 {
		t.Fatalf("reserved after stale release = %d, want 0", got)
	}
	// A fresh reservation in the new generation still commits.
	fresh, err := admission.Reserve(context.Background(), 4)
	if err != nil {
		t.Fatalf("reserve after recover: %v", err)
	}
	if err := fresh.Commit(); err != nil {
		t.Fatalf("commit after recover: %v", err)
	}
	if got := admission.Used(); got != 4 {
		t.Fatalf("used = %d, want 4", got)
	}
}

func TestControllerStartFailureRecyclesQuota(t *testing.T) {
	controller := NewController(10)
	controller.ConfigureBusy(true)
	if err := controller.Start(context.Background(), 4); !errors.Is(err, ErrSourceBusy) {
		t.Fatalf("start = %v, want ErrSourceBusy", err)
	}
	if got := controller.AdmissionUsed(); got != 0 {
		t.Fatalf("used after failed start = %d, want 0", got)
	}
	if got := controller.AdmissionReserved(); got != 0 {
		t.Fatalf("reserved after failed start = %d, want 0", got)
	}
}

func TestControllerStartCancelRecyclesQuota(t *testing.T) {
	controller := NewController(10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := controller.Start(ctx, 4); err == nil {
		t.Fatal("start with cancelled context succeeded")
	}
	if got := controller.AdmissionUsed(); got != 0 {
		t.Fatalf("used after cancelled start = %d, want 0", got)
	}
	if got := controller.AdmissionReserved(); got != 0 {
		t.Fatalf("reserved after cancelled start = %d, want 0", got)
	}
}

func TestControllerDuplicateStartDoesNotIncreaseUsage(t *testing.T) {
	controller := NewController(10)
	for i := 0; i < 3; i++ {
		if err := controller.Start(context.Background(), 4); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if got := controller.AdmissionUsed(); got != 4 {
		t.Fatalf("used after duplicate starts = %d, want 4", got)
	}
	if got := controller.Power(); got != 4 {
		t.Fatalf("power = %v, want 4", got)
	}
}

func TestControllerStartAdjustsPowerWithoutStackingUsage(t *testing.T) {
	controller := NewController(10)
	if err := controller.Start(context.Background(), 4); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := controller.Start(context.Background(), 6); err != nil {
		t.Fatalf("adjust: %v", err)
	}
	if got := controller.AdmissionUsed(); got != 6 {
		t.Fatalf("used after adjust = %d, want 6", got)
	}
}

func TestControllerStopFencesInflightReservation(t *testing.T) {
	controller := NewController(10)
	reservation, err := controller.admission.Reserve(context.Background(), 4)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	controller.Stop()
	if err := reservation.Commit(); !errors.Is(err, ErrStaleReservation) {
		t.Fatalf("commit after stop = %v, want ErrStaleReservation", err)
	}
	if got := controller.AdmissionUsed(); got != 0 {
		t.Fatalf("used after fenced commit = %d, want 0", got)
	}
}

func TestControllerStopReleasesUsage(t *testing.T) {
	controller := NewController(10)
	if err := controller.Start(context.Background(), 4); err != nil {
		t.Fatalf("start: %v", err)
	}
	controller.Stop()
	controller.Stop()
	if got := controller.AdmissionUsed(); got != 0 {
		t.Fatalf("used after stop = %d, want 0", got)
	}
	if got := controller.Power(); got != 0 {
		t.Fatalf("power after stop = %v, want 0", got)
	}
}
