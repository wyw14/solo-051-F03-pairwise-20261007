package source

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrStaleReservation = errors.New("stale reservation generation")
var ErrReservationReleased = errors.New("reservation released")

type Admission struct {
	mu         sync.Mutex
	capacity   int
	used       int
	reserved   int
	generation uint64
}

func NewAdmission(capacity int) *Admission { return &Admission{capacity: capacity} }

// Reserve holds quota for a layer without committing power usage. The
// reservation is bound to the current generation.
func (a *Admission) Reserve(ctx context.Context, layer int, weight int) (*Reservation, error) {
	if weight <= 0 {
		return nil, fmt.Errorf("invalid power weight")
	}
	for {
		a.mu.Lock()
		if a.used+a.reserved+weight <= a.capacity {
			a.reserved += weight
			reservation := &Reservation{admission: a, layer: layer, weight: weight, generation: a.generation}
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

func (a *Admission) Acquire(ctx context.Context, weight int) error {
	reservation, err := a.Reserve(ctx, 0, weight)
	if err != nil {
		return err
	}
	return reservation.Commit()
}

func (a *Admission) Release(weight int) {
	a.mu.Lock()
	a.used -= weight
	if a.used < 0 {
		a.used = 0
	}
	a.mu.Unlock()
}

// Recover reclaims every outstanding reservation and committed usage, and
// bumps the generation so stale reservations are rejected on commit.
func (a *Admission) Recover() {
	a.mu.Lock()
	a.generation++
	a.reserved = 0
	a.used = 0
	a.mu.Unlock()
}

func (a *Admission) Used() int { a.mu.Lock(); defer a.mu.Unlock(); return a.used }

func (a *Admission) Reserved() int { a.mu.Lock(); defer a.mu.Unlock(); return a.reserved }

func (a *Admission) Generation() uint64 { a.mu.Lock(); defer a.mu.Unlock(); return a.generation }

type Reservation struct {
	admission  *Admission
	layer      int
	weight     int
	generation uint64
	committed  bool
	released   bool
}

func (r *Reservation) Layer() int         { return r.layer }
func (r *Reservation) Weight() int        { return r.weight }
func (r *Reservation) Generation() uint64 { return r.generation }

// Commit moves the reserved weight into used admission. It is idempotent:
// duplicate commits do not increase usage.
func (r *Reservation) Commit() error {
	a := r.admission
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.committed {
		return nil
	}
	if r.released {
		return ErrReservationReleased
	}
	if r.generation != a.generation {
		return ErrStaleReservation
	}
	a.reserved -= r.weight
	a.used += r.weight
	r.committed = true
	return nil
}

// Release reclaims quota that was reserved but never committed. Committed
// usage is returned through Admission.Release.
func (r *Reservation) Release() {
	a := r.admission
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.released {
		return
	}
	r.released = true
	if r.committed || r.generation != a.generation {
		return
	}
	a.reserved -= r.weight
}
