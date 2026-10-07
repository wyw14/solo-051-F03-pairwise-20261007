package source

import (
	"context"
	"fmt"
	"sync"

	"github.com/wyw14/cry-161/internal/model"
)

type Controller struct {
	admission *Admission
	heater    *Heater
	adapter   *Adapter
	mu        sync.Mutex
	power     float64
	active    *Reservation
}

func NewController(capacity int) *Controller {
	return &Controller{admission: NewAdmission(capacity), heater: &Heater{}, adapter: NewAdapter()}
}

// Start reserves admission quota for the layer's power draw, confirms the
// device is available, and only then commits the power. Cancellation or any
// failure releases the uncommitted reservation automatically. Starting an
// already-running source at the same power is a no-op and does not increase
// admission usage.
func (c *Controller) Start(ctx context.Context, power float64) error {
	c.mu.Lock()
	if c.active != nil && c.power == power {
		c.mu.Unlock()
		return nil
	}
	previous := c.active
	c.active = nil
	c.power = 0
	c.mu.Unlock()
	if previous != nil {
		previous.Release()
	}
	weight := int(power)
	reservation, err := c.admission.Reserve(ctx, weight)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			reservation.Release()
		}
	}()
	if err := c.adapter.Open(); err != nil {
		return err
	}
	if err := c.heater.Preheat(ctx); err != nil {
		return err
	}
	if err := reservation.Commit(); err != nil {
		return err
	}
	committed = true
	c.mu.Lock()
	displaced := c.active
	c.active = reservation
	c.power = power
	c.mu.Unlock()
	if displaced != nil {
		displaced.Release()
	}
	return nil
}

// Stop releases the committed reservation and fences the admission
// generation, so reservations taken before the stop are rejected when they
// try to commit. Recovery paths call Stop, which is what makes stale
// in-flight requests fail instead of consuming quota.
func (c *Controller) Stop() {
	c.mu.Lock()
	active := c.active
	c.active = nil
	c.power = 0
	c.mu.Unlock()
	if active != nil {
		active.Release()
	}
	c.admission.Recover()
}

func (c *Controller) Power() float64 { c.mu.Lock(); defer c.mu.Unlock(); return c.power }
func (c *Controller) AdmissionUsed() int {
	return c.admission.Used()
}
func (c *Controller) AdmissionReserved() int {
	return c.admission.Reserved()
}
func (c *Controller) ConfigureBusy(value bool) { c.adapter.SetBusy(value) }
func (c *Controller) ConfigurePreheatFailures(n int) {
	c.heater.StableAfter = n
	c.heater.Attempts = 0
}
func (c *Controller) Layer(ctx context.Context, layer model.Layer) error {
	if err := c.Start(ctx, layer.RateNanometers); err != nil {
		return fmt.Errorf("layer %d: %w", layer.Number, err)
	}
	return nil
}
