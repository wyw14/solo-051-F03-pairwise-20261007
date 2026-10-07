package source

import (
	"context"
	"errors"
	"testing"

	"github.com/wyw14/cry-161/internal/model"
)

func TestReservationCommitIsIdempotent(t *testing.T) {
	admission := NewAdmission(10)
	reservation, err := admission.Reserve(context.Background(), 1, 3)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if got := admission.Reserved(); got != 3 {
		t.Fatalf("reserved = %d, want 3", got)
	}
	if err := reservation.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := reservation.Commit(); err != nil {
		t.Fatalf("duplicate commit: %v", err)
	}
	if got := admission.Used(); got != 3 {
		t.Fatalf("used = %d, want 3 after duplicate commit", got)
	}
	if got := admission.Reserved(); got != 0 {
		t.Fatalf("reserved = %d, want 0 after commit", got)
	}
}

func TestReservationReleaseReclaimsUncommittedQuota(t *testing.T) {
	admission := NewAdmission(10)
	reservation, err := admission.Reserve(context.Background(), 2, 4)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	reservation.Release()
	reservation.Release()
	if got := admission.Reserved(); got != 0 {
		t.Fatalf("reserved = %d, want 0 after release", got)
	}
	if err := reservation.Commit(); !errors.Is(err, ErrReservationReleased) {
		t.Fatalf("commit after release = %v, want ErrReservationReleased", err)
	}
}

func TestRecoverRejectsStaleReservation(t *testing.T) {
	admission := NewAdmission(10)
	reservation, err := admission.Reserve(context.Background(), 1, 5)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	admission.Recover()
	if err := reservation.Commit(); !errors.Is(err, ErrStaleReservation) {
		t.Fatalf("commit after recover = %v, want ErrStaleReservation", err)
	}
	reservation.Release()
	if got := admission.Used(); got != 0 {
		t.Fatalf("used = %d, want 0 after recover", got)
	}
	if got := admission.Reserved(); got != 0 {
		t.Fatalf("reserved = %d, want 0 after recover", got)
	}
	fresh, err := admission.Reserve(context.Background(), 1, 5)
	if err != nil {
		t.Fatalf("reserve after recover: %v", err)
	}
	if err := fresh.Commit(); err != nil {
		t.Fatalf("commit fresh reservation: %v", err)
	}
	if got := admission.Used(); got != 5 {
		t.Fatalf("used = %d, want 5", got)
	}
}

func TestControllerStartFailureReclaimsQuota(t *testing.T) {
	controller := NewController(10)
	controller.ConfigureBusy(true)
	if err := controller.Start(context.Background(), 4); err == nil {
		t.Fatal("start on busy device should fail")
	}
	if got := controller.AdmissionUsed(); got != 0 {
		t.Fatalf("used = %d, want 0 after failed start", got)
	}
	if got := controller.AdmissionReserved(); got != 0 {
		t.Fatalf("reserved = %d, want 0 after failed start", got)
	}
}

func TestControllerStartCancelReclaimsQuota(t *testing.T) {
	controller := NewController(10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := controller.Start(ctx, 4); err == nil {
		t.Fatal("start with cancelled context should fail")
	}
	if got := controller.AdmissionReserved(); got != 0 {
		t.Fatalf("reserved = %d, want 0 after cancelled start", got)
	}
}

func TestControllerLayerReservesPerLayer(t *testing.T) {
	controller := NewController(10)
	layer := model.Layer{Number: 3, Material: "SiO2", TargetNanometers: 10, RateNanometers: 2}
	if err := controller.Layer(context.Background(), layer); err != nil {
		t.Fatalf("layer: %v", err)
	}
	if got := controller.AdmissionUsed(); got != 2 {
		t.Fatalf("used = %d, want 2 after layer start", got)
	}
	controller.Stop()
	if got := controller.AdmissionUsed(); got != 0 {
		t.Fatalf("used = %d, want 0 after stop", got)
	}
}

func TestControllerRecoverRejectsInFlightStart(t *testing.T) {
	controller := NewController(10)
	reservation, err := controller.admission.Reserve(context.Background(), 1, 4)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	controller.Recover()
	if err := reservation.Commit(); !errors.Is(err, ErrStaleReservation) {
		t.Fatalf("commit after controller recover = %v, want ErrStaleReservation", err)
	}
	if got := controller.AdmissionUsed(); got != 0 {
		t.Fatalf("used = %d, want 0 after recover", got)
	}
}
