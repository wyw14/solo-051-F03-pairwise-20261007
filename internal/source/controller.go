package source

import (
	"context"
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
)

type Controller struct {
	admission *Admission
	heater    *Heater
	adapter   *Adapter
	power     float64
}

func NewController(capacity int) *Controller {
	return &Controller{admission: NewAdmission(capacity), heater: &Heater{}, adapter: NewAdapter()}
}
func (c *Controller) Start(ctx context.Context, power float64) error {
	return c.StartLayer(ctx, 0, power)
}

// StartLayer reserves admission for the layer, confirms the device is
// available, then commits the power. Cancel or failure reclaims the
// uncommitted quota automatically.
func (c *Controller) StartLayer(ctx context.Context, layer int, power float64) error {
	weight := int(power)
	reservation, err := c.admission.Reserve(ctx, layer, weight)
	if err != nil {
		return err
	}
	if err := c.adapter.Open(); err != nil {
		reservation.Release()
		return err
	}
	if err := c.heater.Preheat(ctx); err != nil {
		reservation.Release()
		return err
	}
	if err := reservation.Commit(); err != nil {
		return err
	}
	c.power = power
	return nil
}

// Recover resets admission accounting and bumps the reservation generation,
// so in-flight requests from before the recovery are rejected on commit.
func (c *Controller) Recover() { c.admission.Recover(); c.power = 0 }

func (c *Controller) Stop()                          { c.admission.Release(int(c.power)); c.power = 0 }
func (c *Controller) Power() float64                 { return c.power }
func (c *Controller) AdmissionUsed() int             { return c.admission.Used() }
func (c *Controller) AdmissionReserved() int         { return c.admission.Reserved() }
func (c *Controller) ConfigureBusy(value bool)       { c.adapter.SetBusy(value) }
func (c *Controller) ConfigurePreheatFailures(n int) { c.heater.StableAfter = n; c.heater.Attempts = 0 }
func (c *Controller) Layer(ctx context.Context, layer model.Layer) error {
	if err := c.StartLayer(ctx, layer.Number, layer.RateNanometers); err != nil {
		return fmt.Errorf("layer %d: %w", layer.Number, err)
	}
	return nil
}
