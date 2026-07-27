package sdk

import (
	"context"
	"fmt"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/version"
)

// PrismElement is the Go SDK backed implementation of service.PrismElement.
//
// The two cluster-local inventories the dependency prechecks want — legacy
// protection domains and file server network assignments — have no v4 namespace
// and therefore no generated client. This backend reports them as unsupported;
// the composite wrapper pairs it with the REST legacy client so the checks still
// run.
type PrismElement struct {
	endpoint model.Endpoint
	clients  *clientSet
}

// NewPrismElement builds a Prism Element service layer backed by the Go SDK.
func NewPrismElement(ep model.Endpoint, quiet bool) *PrismElement {
	return &PrismElement{endpoint: ep, clients: newClientSet(ep, quiet)}
}

func (p *PrismElement) Backend() service.Backend { return service.BackendGoSDK }

// Version reads the AOS version from the cluster this Prism Element serves.
func (p *PrismElement) Version(ctx context.Context) (model.ProductVersion, error) {
	clusters, err := listClusters(p.clients)
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
	return nil, service.ErrUnsupported
}

func (p *PrismElement) ListFileServerNetworks(ctx context.Context) ([]model.FileServerNetwork, error) {
	return nil, service.ErrUnsupported
}

var _ service.PrismElement = (*PrismElement)(nil)
