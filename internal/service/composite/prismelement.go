// Package composite pairs service backends so that a component always sees one
// service.PrismElement, regardless of which backend can answer a given call.
package composite

import (
	"context"
	"errors"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service/rest"
)

// PrismElement serves the version from a primary backend (the Go SDK when it was
// selected) and falls back to the REST backend for the legacy inventories, which
// have no SDK equivalent.
type PrismElement struct {
	primary service.PrismElement
	legacy  *rest.PrismElement
}

// NewPrismElement wraps a primary Prism Element backend with a REST fallback.
// When primary is already the REST backend the wrapper is transparent.
func NewPrismElement(primary service.PrismElement, legacy *rest.PrismElement) *PrismElement {
	return &PrismElement{primary: primary, legacy: legacy}
}

func (p *PrismElement) Backend() service.Backend { return p.primary.Backend() }

func (p *PrismElement) Version(ctx context.Context) (model.ProductVersion, error) {
	v, err := p.primary.Version(ctx)
	if err == nil || p.legacy == nil {
		return v, err
	}
	return p.legacy.Version(ctx)
}

func (p *PrismElement) ListProtectionDomains(ctx context.Context) ([]model.ProtectionDomain, error) {
	pds, err := p.primary.ListProtectionDomains(ctx)
	if !p.shouldFallBack(err) {
		return pds, err
	}
	return p.legacy.ListProtectionDomains(ctx)
}

func (p *PrismElement) ListFileServerNetworks(ctx context.Context) ([]model.FileServerNetwork, error) {
	networks, err := p.primary.ListFileServerNetworks(ctx)
	if !p.shouldFallBack(err) {
		return networks, err
	}
	return p.legacy.ListFileServerNetworks(ctx)
}

func (p *PrismElement) shouldFallBack(err error) bool {
	return p.legacy != nil && errors.Is(err, service.ErrUnsupported)
}

var _ service.PrismElement = (*PrismElement)(nil)
