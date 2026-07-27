package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/version"
)

// PrismCentral is the v4 REST implementation of service.PrismCentral.
type PrismCentral struct {
	client *Client
}

// NewPrismCentral builds a Prism Central service layer backed by the v4 REST APIs.
func NewPrismCentral(ep model.Endpoint) *PrismCentral {
	return &PrismCentral{client: NewClient(ep)}
}

// NewPrismCentralWithClient lets callers share an already configured client.
func NewPrismCentralWithClient(c *Client) *PrismCentral {
	return &PrismCentral{client: c}
}

func (p *PrismCentral) Backend() service.Backend { return service.BackendREST }

func (p *PrismCentral) Version(ctx context.Context) (model.ProductVersion, error) {
	var envelope struct {
		Data []domainManagerPayload `json:"data"`
	}
	path := p.client.nsPath(ctx, "prism", "config/domain-managers")
	if err := p.client.getJSON(ctx, path, nil, &envelope); err != nil {
		return model.ProductVersion{}, translateCollection(err)
	}
	for _, dm := range envelope.Data {
		if dm.Config != nil && dm.Config.BuildInfo != nil && dm.Config.BuildInfo.Version != "" {
			return version.Parse("Prism Central", dm.Config.BuildInfo.Version, path), nil
		}
	}
	return model.ProductVersion{}, fmt.Errorf("no domain manager reported a build version")
}

func (p *PrismCentral) ListClusters(ctx context.Context) ([]model.Cluster, error) {
	return listClusters(ctx, p.client)
}

func listClusters(ctx context.Context, c *Client) ([]model.Cluster, error) {
	var out []model.Cluster
	path := c.nsPath(ctx, "clustermgmt", "config/clusters")
	err := c.listPaged(ctx, path, nil, func(raw json.RawMessage) (int, error) {
		var page []clusterPayload
		if err := json.Unmarshal(raw, &page); err != nil {
			return 0, fmt.Errorf("decode clusters: %w", err)
		}
		for _, item := range page {
			out = append(out, item.toModel())
		}
		return len(page), nil
	})
	return out, translateCollection(err)
}

func (p *PrismCentral) ListNetworkControllers(ctx context.Context) ([]model.NetworkController, error) {
	var envelope struct {
		Data []networkControllerPayload `json:"data"`
	}
	path := p.client.nsPath(ctx, "networking", "config/controllers")
	if err := p.client.getJSON(ctx, path, nil, &envelope); err != nil {
		return nil, translateCollection(err)
	}
	out := make([]model.NetworkController, 0, len(envelope.Data))
	for _, item := range envelope.Data {
		out = append(out, item.toModel())
	}
	return out, nil
}

func (p *PrismCentral) ListSubnets(ctx context.Context) ([]model.Subnet, error) {
	var out []model.Subnet
	path := p.client.nsPath(ctx, "networking", "config/subnets")
	err := p.client.listPaged(ctx, path, nil, func(raw json.RawMessage) (int, error) {
		var page []subnetPayload
		if err := json.Unmarshal(raw, &page); err != nil {
			return 0, fmt.Errorf("decode subnets: %w", err)
		}
		for _, item := range page {
			out = append(out, item.toModel())
		}
		return len(page), nil
	})
	return out, translateCollection(err)
}

func (p *PrismCentral) GetSubnet(ctx context.Context, extID string) (model.Subnet, error) {
	var envelope struct {
		Data subnetPayload `json:"data"`
	}
	path := p.client.nsPath(ctx, "networking", "config/subnets/"+url.PathEscape(extID))
	if err := p.client.getJSON(ctx, path, nil, &envelope); err != nil {
		return model.Subnet{}, translate(err)
	}
	return envelope.Data.toModel(), nil
}

func (p *PrismCentral) ListVnicsBySubnet(ctx context.Context, subnetExtID string) ([]model.Vnic, error) {
	subnet, err := p.GetSubnet(ctx, subnetExtID)
	if err != nil {
		return nil, err
	}
	var out []model.Vnic
	path := p.client.nsPath(ctx, "networking", "config/subnets/"+url.PathEscape(subnetExtID)+"/vnics")
	err = p.client.listPaged(ctx, path, nil, func(raw json.RawMessage) (int, error) {
		var page []vnicPayload
		if err := json.Unmarshal(raw, &page); err != nil {
			return 0, fmt.Errorf("decode subnet vnics: %w", err)
		}
		for _, item := range page {
			out = append(out, item.toModel(subnet))
		}
		return len(page), nil
	})
	return out, translateCollection(err)
}

func (p *PrismCentral) ListVMs(ctx context.Context) ([]model.VM, error) {
	var out []model.VM
	path := p.client.nsPath(ctx, "vmm", "ahv/config/vms")
	err := p.client.listPaged(ctx, path, nil, func(raw json.RawMessage) (int, error) {
		var page []vmPayload
		if err := json.Unmarshal(raw, &page); err != nil {
			return 0, fmt.Errorf("decode vms: %w", err)
		}
		for _, item := range page {
			out = append(out, item.toModel())
		}
		return len(page), nil
	})
	return out, translateCollection(err)
}

func (p *PrismCentral) ListFileServers(ctx context.Context) ([]model.FileServer, error) {
	var out []model.FileServer
	path := p.client.nsPath(ctx, "files", "config/file-servers")
	err := p.client.listPaged(ctx, path, nil, func(raw json.RawMessage) (int, error) {
		var page []fileServerPayload
		if err := json.Unmarshal(raw, &page); err != nil {
			return 0, fmt.Errorf("decode file servers: %w", err)
		}
		for _, item := range page {
			out = append(out, model.FileServer{ExtID: item.ExtID, Name: item.Name})
		}
		return len(page), nil
	})
	return out, translateCollection(err)
}

func (p *PrismCentral) GetProtectionInfo(ctx context.Context, entityExtID string) (model.ProtectionInfo, error) {
	var envelope struct {
		Data protectedResourcePayload `json:"data"`
	}
	path := p.client.nsPath(ctx, "dataprotection", "config/protected-resources/"+url.PathEscape(entityExtID))
	if err := p.client.getJSON(ctx, path, nil, &envelope); err != nil {
		return model.ProtectionInfo{}, translate(err)
	}
	return envelope.Data.toModel(), nil
}

func (p *PrismCentral) MigrateSubnets(ctx context.Context, subnetExtIDs []string) (string, error) {
	if len(subnetExtIDs) == 0 {
		return "", fmt.Errorf("no subnets supplied for migration")
	}
	type subnetRef struct {
		SubnetUUID string `json:"subnetUuid"`
	}
	body := struct {
		ObjectType string      `json:"$objectType"`
		Subnets    []subnetRef `json:"subnets"`
	}{
		ObjectType: "networking.v4.config.VlanSubnetMigrationSpec",
		Subnets:    make([]subnetRef, 0, len(subnetExtIDs)),
	}
	for _, id := range subnetExtIDs {
		body.Subnets = append(body.Subnets, subnetRef{SubnetUUID: id})
	}

	var envelope struct {
		Data taskReferencePayload `json:"data"`
	}
	path := p.client.nsPath(ctx, "networking", "config/$actions/migrate-subnets")
	if err := p.client.postJSON(ctx, path, body, &envelope); err != nil {
		return "", translate(err)
	}
	if envelope.Data.ExtID == "" {
		return "", fmt.Errorf("migrate subnets: server returned no task UUID")
	}
	return envelope.Data.ExtID, nil
}

func (p *PrismCentral) GetTask(ctx context.Context, taskExtID string) (model.Task, error) {
	var envelope struct {
		Data taskPayload `json:"data"`
	}
	path := p.client.nsPath(ctx, "prism", "config/tasks/"+url.PathEscape(taskExtID))
	if err := p.client.getJSON(ctx, path, nil, &envelope); err != nil {
		return model.Task{}, translate(err)
	}
	return envelope.Data.toModel(), nil
}

var _ service.PrismCentral = (*PrismCentral)(nil)
