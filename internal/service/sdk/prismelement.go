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
// service.Compose pairs it with the REST legacy client so the checks still run.
type PrismElement struct {
	endpoint model.Endpoint
	clients  *clientSet
}

// NewPrismElement builds a Prism Element service layer backed by the Go SDK.
func NewPrismElement(ep model.Endpoint) *PrismElement {
	return &PrismElement{endpoint: ep, clients: newClientSet(ep)}
}

func (p *PrismElement) Backend() service.Backend { return service.BackendGoSDK }

// Version reads the AOS version from the single cluster this Prism Element
// serves. A Prism Element only ever returns its own cluster plus, on some
// builds, the Prism Central it is registered to, so the cluster carrying an AOS
// build version wins.
func (p *PrismElement) Version(ctx context.Context) (model.ProductVersion, error) {
	clusters, err := p.listClusters(ctx)
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

func (p *PrismElement) listClusters(ctx context.Context) ([]model.Cluster, error) {
	var out []model.Cluster
	err := paginate(func(page int) (int, error) {
		resp, err := p.clients.clusters.ListClusters(&page, intPtr(pageSize), nil, nil, nil, nil, nil)
		if err != nil {
			return 0, fmt.Errorf("list clusters: %w", err)
		}
		if resp == nil || resp.Data == nil {
			return 0, nil
		}
		items, ok := resp.Data.GetValue().([]clustermgmtCluster)
		if !ok {
			return 0, fmt.Errorf("unexpected cluster payload %T", resp.Data.GetValue())
		}
		for _, c := range items {
			out = append(out, convertCluster(c))
		}
		return len(items), nil
	})
	return out, err
}

func (p *PrismElement) ListProtectionDomains(ctx context.Context) ([]model.ProtectionDomain, error) {
	return nil, service.ErrUnsupported
}

func (p *PrismElement) ListFileServerNetworks(ctx context.Context) ([]model.FileServerNetwork, error) {
	return nil, service.ErrUnsupported
}

var _ service.PrismElement = (*PrismElement)(nil)
