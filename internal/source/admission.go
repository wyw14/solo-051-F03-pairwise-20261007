package source

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrStaleReservation = errors.New("stale reservation generation")
var ErrReservationReleased = errors.New("reservation already released")

type reservationState int

const (
	reservationPending reservationState = iota
	reservationCommitted
	reservationReleased
)

// Admission hands out quota in two phases: Reserve holds capacity for a
// layer, and Commit turns the hold into usage once the device is confirmed
// available. A generation counter fences reservations across recoveries:
// Recover bumps the generation and reclaims every pending hold, so requests
// reserved before a recovery are rejected when they try to commit.
type Admission struct {
	mu         sync.Mutex
	capacity   int
	used       int
	reserved   int
	generation uint64
}

func NewAdmission(capacity int) *Admission { return &Admission{capacity: capacity} }

// Reserve holds weight units of capacity and returns a Reservation. The
// hold keeps other reservations from overbooking capacity but does not
// count as usage until committed.
func (a *Admission) Reserve(ctx context.Context, weight int) (*Reservation, error) {
	if weight <= 0 {
		return nil, fmt.Errorf("invalid power weight")
	}
	for {
		a.mu.Lock()
		if a.used+a.reserved+weight <= a.capacity {
			a.reserved += weight
			reservation := &Reservation{admission: a, weight: weight, generation: a.generation}
			a.mu.Unlock()
			return reservation, nil
		}
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
}

// Recover fences off every reservation taken before the call: pending holds
// are reclaimed and stale reservations are rejected on Commit.
func (a *Admission) Recover() {
	a.mu.Lock()
	a.generation++
	a.reserved = 0
	a.mu.Unlock()
}

func (a *Admission) Used() int { a.mu.Lock(); defer a.mu.Unlock(); return a.used }

func (a *Admission) Reserved() int { a.mu.Lock(); defer a.mu.Unlock(); return a.reserved }

// Reservation is a held slice of admission quota. Its state is guarded by
// the owning Admission's mutex.
type Reservation struct {
	admission  *Admission
	weight     int
	generation uint64
	state      reservationState
}

func (r *Reservation) Weight() int { return r.weight }

// Commit converts the hold into usage. It is idempotent: committing an
// already-committed reservation does not increase usage again.
func (r *Reservation) Commit() error {
	a := r.admission
	a.mu.Lock()
	defer a.mu.Unlock()
	switch r.state {
	case reservationCommitted:
		return nil
	case reservationReleased:
		return ErrReservationReleased
	}
	if r.generation != a.generation {
		r.state = reservationReleased
		return fmt.Errorf("commit reservation: %w", ErrStaleReservation)
	}
	a.reserved -= r.weight
	a.used += r.weight
	r.state = reservationCommitted
	return nil
}

// Release returns any quota the reservation still holds — pending or
// committed — and is safe to call more than once.
func (r *Reservation) Release() {
	a := r.admission
	a.mu.Lock()
	defer a.mu.Unlock()
	switch r.state {
	case reservationPending:
		// A stale hold was already reclaimed by Recover.
		if r.generation == a.generation {
			a.reserved -= r.weight
		}
	case reservationCommitted:
		a.used -= r.weight
	}
	r.state = reservationReleased
}
