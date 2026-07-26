package rest

import (
	"context"
	"fmt"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/version"
)

// PrismElement is the REST implementation of service.PrismElement. The version
// comes from the v4 cluster management namespace; the protection domain and file
// server inventories come from the cluster-local Prism Gateway APIs, which are
// the only ones that expose them.
type PrismElement struct {
	client *Client
}

// NewPrismElement builds a Prism Element service layer backed by REST.
func NewPrismElement(ep model.Endpoint) *PrismElement {
	return &PrismElement{client: NewClient(ep)}
}

// NewPrismElementWithClient lets callers share an already configured client.
func NewPrismElementWithClient(c *Client) *PrismElement {
	return &PrismElement{client: c}
}

func (p *PrismElement) Backend() service.Backend { return service.BackendREST }

func (p *PrismElement) Version(ctx context.Context) (model.ProductVersion, error) {
	clusters, err := listClusters(ctx, p.client)
	if err != nil {
		return model.ProductVersion{}, err
	}
	for _, c := range clusters {
		if c.AOSVersion != "" {
			return version.Parse("AOS", c.AOSVersion, "clustermgmt/v4 clusters"), nil
		}
	}
	return model.ProductVersion{}, fmt.Errorf("no cluster reported an AOS build version")
}

func (p *PrismElement) ListProtectionDomains(ctx context.Context) ([]model.ProtectionDomain, error) {
	return listProtectionDomains(ctx, p.client)
}

func (p *PrismElement) ListFileServerNetworks(ctx context.Context) ([]model.FileServerNetwork, error) {
	return listFileServerNetworks(ctx, p.client)
}

var _ service.PrismElement = (*PrismElement)(nil)
