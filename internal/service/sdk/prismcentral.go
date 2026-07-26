package sdk

import (
	"context"
	"fmt"
	"strings"

	networkingconfig "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	networkingprism "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/prism/v4/config"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/version"
)

// PrismCentral is the Go SDK backed implementation of service.PrismCentral.
type PrismCentral struct {
	endpoint model.Endpoint
	clients  *clientSet
}

// NewPrismCentral builds a Prism Central service layer backed by the Go SDK.
func NewPrismCentral(ep model.Endpoint) *PrismCentral {
	return &PrismCentral{endpoint: ep, clients: newClientSet(ep)}
}

func (p *PrismCentral) Backend() service.Backend { return service.BackendGoSDK }

func (p *PrismCentral) Version(ctx context.Context) (model.ProductVersion, error) {
	resp, err := p.clients.domainManager.ListDomainManagers(nil)
	if err != nil {
		return model.ProductVersion{}, fmt.Errorf("list domain managers: %w", err)
	}
	if resp == nil || resp.Data == nil {
		return model.ProductVersion{}, fmt.Errorf("prism central returned no domain manager")
	}
	managers, ok := resp.Data.GetValue().([]prismDomainManager)
	if !ok {
		return model.ProductVersion{}, fmt.Errorf("unexpected domain manager payload %T", resp.Data.GetValue())
	}
	for _, dm := range managers {
		if dm.Config == nil || dm.Config.BuildInfo == nil || dm.Config.BuildInfo.Version == nil {
			continue
		}
		return version.Parse("Prism Central", *dm.Config.BuildInfo.Version, "prism/v4 domain-managers"), nil
	}
	return model.ProductVersion{}, fmt.Errorf("no domain manager reported a build version")
}

func (p *PrismCentral) ListClusters(ctx context.Context) ([]model.Cluster, error) {
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

func (p *PrismCentral) ListNetworkControllers(ctx context.Context) ([]model.NetworkController, error) {
	resp, err := p.clients.networkControllers.ListNetworkControllers(intPtr(0), intPtr(pageSize))
	if err != nil {
		// A Prism Central without Flow Virtual Networking enabled answers with
		// an error rather than an empty list on some builds; the caller treats
		// "no controller" as "advanced networking unavailable".
		return nil, fmt.Errorf("list network controllers: %w", err)
	}
	if resp == nil || resp.Data == nil {
		return nil, nil
	}
	items, ok := resp.Data.GetValue().([]networkingconfig.NetworkController)
	if !ok {
		return nil, fmt.Errorf("unexpected network controller payload %T", resp.Data.GetValue())
	}
	out := make([]model.NetworkController, 0, len(items))
	for _, c := range items {
		out = append(out, convertNetworkController(c))
	}
	return out, nil
}

func (p *PrismCentral) ListSubnets(ctx context.Context) ([]model.Subnet, error) {
	var out []model.Subnet
	err := paginate(func(page int) (int, error) {
		resp, err := p.clients.subnets.ListSubnets(&page, intPtr(pageSize), nil, nil, nil, nil)
		if err != nil {
			return 0, fmt.Errorf("list subnets: %w", err)
		}
		if resp == nil || resp.Data == nil {
			return 0, nil
		}
		items, ok := resp.Data.GetValue().([]networkingconfig.Subnet)
		if !ok {
			return 0, fmt.Errorf("unexpected subnet payload %T", resp.Data.GetValue())
		}
		for _, s := range items {
			out = append(out, convertSubnet(s))
		}
		return len(items), nil
	})
	return out, err
}

func (p *PrismCentral) GetSubnet(ctx context.Context, extID string) (model.Subnet, error) {
	resp, err := p.clients.subnets.GetSubnetById(&extID)
	if err != nil {
		return model.Subnet{}, fmt.Errorf("get subnet %s: %w", extID, err)
	}
	if resp == nil || resp.Data == nil {
		return model.Subnet{}, fmt.Errorf("get subnet %s: %w", extID, service.ErrNotFound)
	}
	s, ok := resp.Data.GetValue().(networkingconfig.Subnet)
	if !ok {
		return model.Subnet{}, fmt.Errorf("unexpected subnet payload %T", resp.Data.GetValue())
	}
	return convertSubnet(s), nil
}

func (p *PrismCentral) ListVnicsBySubnet(ctx context.Context, subnetExtID string) ([]model.Vnic, error) {
	subnet, err := p.GetSubnet(ctx, subnetExtID)
	if err != nil {
		return nil, err
	}
	var out []model.Vnic
	err = paginate(func(page int) (int, error) {
		resp, err := p.clients.subnets.ListVnicsBySubnetId(&subnetExtID, &page, intPtr(pageSize), nil, nil, nil)
		if err != nil {
			return 0, fmt.Errorf("list vnics of subnet %s: %w", subnetExtID, err)
		}
		if resp == nil || resp.Data == nil {
			return 0, nil
		}
		items, ok := resp.Data.GetValue().([]networkingconfig.Vnic)
		if !ok {
			return 0, fmt.Errorf("unexpected vnic payload %T", resp.Data.GetValue())
		}
		for _, v := range items {
			out = append(out, convertVnic(v, subnet))
		}
		return len(items), nil
	})
	return out, err
}

func (p *PrismCentral) ListVMs(ctx context.Context) ([]model.VM, error) {
	var out []model.VM
	err := paginate(func(page int) (int, error) {
		resp, err := p.clients.vms.ListVms(&page, intPtr(pageSize), nil, nil, nil)
		if err != nil {
			return 0, fmt.Errorf("list vms: %w", err)
		}
		if resp == nil || resp.Data == nil {
			return 0, nil
		}
		items, ok := resp.Data.GetValue().([]ahvVM)
		if !ok {
			return 0, fmt.Errorf("unexpected vm payload %T", resp.Data.GetValue())
		}
		for _, v := range items {
			out = append(out, convertVM(v))
		}
		return len(items), nil
	})
	return out, err
}

func (p *PrismCentral) ListFileServers(ctx context.Context) ([]model.FileServer, error) {
	var out []model.FileServer
	err := paginate(func(page int) (int, error) {
		resp, err := p.clients.fileServers.ListFileServers(&page, intPtr(pageSize), nil, nil, nil)
		if err != nil {
			// Files is an optional product; a Prism Central without it answers
			// 404 or 501 here. Surfacing ErrUnsupported lets the precheck record
			// a warning instead of failing the run.
			if isNotImplemented(err) {
				return 0, service.ErrUnsupported
			}
			return 0, fmt.Errorf("list file servers: %w", err)
		}
		if resp == nil || resp.Data == nil {
			return 0, nil
		}
		items, ok := resp.Data.GetValue().([]filesFileServer)
		if !ok {
			return 0, fmt.Errorf("unexpected file server payload %T", resp.Data.GetValue())
		}
		for _, f := range items {
			out = append(out, convertFileServer(f))
		}
		return len(items), nil
	})
	return out, err
}

func (p *PrismCentral) GetProtectionInfo(ctx context.Context, entityExtID string) (model.ProtectionInfo, error) {
	resp, err := p.clients.protectedResources.GetProtectedResourceById(&entityExtID)
	if err != nil {
		if isNotFound(err) {
			return model.ProtectionInfo{}, service.ErrNotFound
		}
		if isNotImplemented(err) {
			return model.ProtectionInfo{}, service.ErrUnsupported
		}
		return model.ProtectionInfo{}, fmt.Errorf("get protected resource %s: %w", entityExtID, err)
	}
	if resp == nil || resp.Data == nil {
		return model.ProtectionInfo{}, service.ErrNotFound
	}
	pr, ok := resp.Data.GetValue().(dataprotectionProtectedResource)
	if !ok {
		return model.ProtectionInfo{}, fmt.Errorf("unexpected protected resource payload %T", resp.Data.GetValue())
	}
	return convertProtectionInfo(pr), nil
}

func (p *PrismCentral) MigrateSubnets(ctx context.Context, subnetExtIDs []string) (string, error) {
	if len(subnetExtIDs) == 0 {
		return "", fmt.Errorf("no subnets supplied for migration")
	}
	spec := networkingconfig.NewVlanSubnetMigrationSpec()
	spec.Subnets = make([]networkingconfig.SubnetInfo, 0, len(subnetExtIDs))
	for i := range subnetExtIDs {
		item := networkingconfig.NewSubnetInfo()
		item.SubnetUuid = &subnetExtIDs[i]
		spec.Subnets = append(spec.Subnets, *item)
	}

	resp, err := p.clients.subnetMigrations.MigrateSubnets(spec)
	if err != nil {
		return "", fmt.Errorf("migrate subnets: %w", err)
	}
	if resp == nil || resp.Data == nil {
		return "", fmt.Errorf("migrate subnets: server returned no task reference")
	}
	ref, ok := resp.Data.GetValue().(networkingprism.TaskReference)
	if !ok {
		return "", fmt.Errorf("unexpected task reference payload %T", resp.Data.GetValue())
	}
	if ref.ExtId == nil || *ref.ExtId == "" {
		return "", fmt.Errorf("migrate subnets: task reference carried no UUID")
	}
	return *ref.ExtId, nil
}

func (p *PrismCentral) GetTask(ctx context.Context, taskExtID string) (model.Task, error) {
	resp, err := p.clients.tasks.GetTaskById(&taskExtID, nil)
	if err != nil {
		return model.Task{}, fmt.Errorf("get task %s: %w", taskExtID, err)
	}
	if resp == nil || resp.Data == nil {
		return model.Task{}, fmt.Errorf("get task %s: %w", taskExtID, service.ErrNotFound)
	}
	t, ok := resp.Data.GetValue().(prismTask)
	if !ok {
		return model.Task{}, fmt.Errorf("unexpected task payload %T", resp.Data.GetValue())
	}
	return convertTask(t), nil
}

func intPtr(v int) *int { return &v }

// paginate walks a v4 collection until a page comes back short. fetch returns
// the number of records the page contained.
func paginate(fetch func(page int) (int, error)) error {
	const maxPages = 1000
	for page := 0; page < maxPages; page++ {
		n, err := fetch(page)
		if err != nil {
			return err
		}
		if n < pageSize {
			return nil
		}
	}
	return fmt.Errorf("aborting pagination after %d pages", maxPages)
}

// isNotFound recognises the 404 the generated clients wrap into their error
// string. The SDK does not expose the status code, so the message is all there
// is to match on.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "404") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "does not exist")
}

func isNotImplemented(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "501") ||
		strings.Contains(msg, "not implemented") ||
		strings.Contains(msg, "no route") ||
		strings.Contains(msg, "unavailable")
}

var _ service.PrismCentral = (*PrismCentral)(nil)
