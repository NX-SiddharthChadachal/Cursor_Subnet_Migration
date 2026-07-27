package sdk

import (
	"context"
	"fmt"
	"reflect"
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
// When quiet is set the SDK's own request logging is suppressed so that the run
// log stays readable.
func NewPrismCentral(ep model.Endpoint, quiet bool) *PrismCentral {
	return &PrismCentral{endpoint: ep, clients: newClientSet(ep, quiet)}
}

func (p *PrismCentral) Backend() service.Backend { return service.BackendGoSDK }

func (p *PrismCentral) Version(ctx context.Context) (model.ProductVersion, error) {
	var out model.ProductVersion
	err := guard("read the Prism Central version", func() error {
		resp, err := p.clients.domainManager.ListDomainManagers(nil)
		if err != nil {
			return fmt.Errorf("list domain managers: %w", err)
		}
		if resp == nil || resp.Data == nil {
			return fmt.Errorf("prism central returned no domain manager")
		}
		managers, err := coerceList[prismDomainManager](resp.Data.GetValue())
		if err != nil {
			return err
		}
		for _, dm := range managers {
			if dm.Config == nil || dm.Config.BuildInfo == nil || dm.Config.BuildInfo.Version == nil {
				continue
			}
			out = version.Parse("Prism Central", *dm.Config.BuildInfo.Version, "prism/v4 domain-managers")
			return nil
		}
		return fmt.Errorf("no domain manager reported a build version")
	})
	return out, err
}

func (p *PrismCentral) ListClusters(ctx context.Context) ([]model.Cluster, error) {
	return listClusters(p.clients)
}

func (p *PrismCentral) ListNetworkControllers(ctx context.Context) ([]model.NetworkController, error) {
	var out []model.NetworkController
	err := guard("list the network controllers", func() error {
		resp, err := p.clients.networkControllers.ListNetworkControllers(intPtr(0), intPtr(pageSize))
		if err != nil {
			// A Prism Central without Flow Virtual Networking answers with an
			// error rather than an empty list on some builds; the caller treats
			// "no controller" as "advanced networking unavailable".
			return fmt.Errorf("list network controllers: %w", err)
		}
		if resp == nil || resp.Data == nil {
			return nil
		}
		items, err := coerceList[networkingconfig.NetworkController](resp.Data.GetValue())
		if err != nil {
			return err
		}
		for _, c := range items {
			out = append(out, convertNetworkController(c))
		}
		return nil
	})
	return out, err
}

func (p *PrismCentral) ListSubnets(ctx context.Context) ([]model.Subnet, error) {
	var out []model.Subnet
	err := guard("list the subnets", func() error {
		return paginate(func(page int) (int, error) {
			resp, err := p.clients.subnets.ListSubnets(&page, intPtr(pageSize), nil, nil, nil, nil)
			if err != nil {
				return 0, fmt.Errorf("list subnets: %w", err)
			}
			if resp == nil || resp.Data == nil {
				return 0, nil
			}
			items, err := coerceList[networkingconfig.Subnet](resp.Data.GetValue())
			if err != nil {
				return 0, err
			}
			for _, s := range items {
				out = append(out, convertSubnet(s))
			}
			return len(items), nil
		})
	})
	return out, err
}

func (p *PrismCentral) GetSubnet(ctx context.Context, extID string) (model.Subnet, error) {
	var out model.Subnet
	err := guard("read subnet "+extID, func() error {
		resp, err := p.clients.subnets.GetSubnetById(&extID)
		if err != nil {
			if isNotFound(err) {
				return fmt.Errorf("get subnet %s: %w", extID, service.ErrNotFound)
			}
			return fmt.Errorf("get subnet %s: %w", extID, err)
		}
		if resp == nil || resp.Data == nil {
			return fmt.Errorf("get subnet %s: %w", extID, service.ErrNotFound)
		}
		s, err := coerceOne[networkingconfig.Subnet](resp.Data.GetValue())
		if err != nil {
			return err
		}
		out = convertSubnet(s)
		return nil
	})
	return out, err
}

func (p *PrismCentral) ListVnicsBySubnet(ctx context.Context, subnetExtID string) ([]model.Vnic, error) {
	subnet, err := p.GetSubnet(ctx, subnetExtID)
	if err != nil {
		return nil, err
	}
	var out []model.Vnic
	err = guard("list the vNICs of subnet "+subnetExtID, func() error {
		return paginate(func(page int) (int, error) {
			resp, err := p.clients.subnets.ListVnicsBySubnetId(&subnetExtID, &page, intPtr(pageSize), nil, nil, nil)
			if err != nil {
				return 0, fmt.Errorf("list vNICs of subnet %s: %w", subnetExtID, err)
			}
			if resp == nil || resp.Data == nil {
				return 0, nil
			}
			items, err := coerceList[networkingconfig.Vnic](resp.Data.GetValue())
			if err != nil {
				return 0, err
			}
			for _, v := range items {
				out = append(out, convertVnic(v, subnet))
			}
			return len(items), nil
		})
	})
	return out, err
}

func (p *PrismCentral) ListVMs(ctx context.Context) ([]model.VM, error) {
	var out []model.VM
	err := guard("list the VMs", func() error {
		return paginate(func(page int) (int, error) {
			resp, err := p.clients.vms.ListVms(&page, intPtr(pageSize), nil, nil, nil)
			if err != nil {
				return 0, fmt.Errorf("list VMs: %w", err)
			}
			if resp == nil || resp.Data == nil {
				return 0, nil
			}
			items, err := coerceList[ahvVM](resp.Data.GetValue())
			if err != nil {
				return 0, err
			}
			for _, v := range items {
				out = append(out, convertVM(v))
			}
			return len(items), nil
		})
	})
	return out, err
}

func (p *PrismCentral) ListFileServers(ctx context.Context) ([]model.FileServer, error) {
	var out []model.FileServer
	err := guard("list the file servers", func() error {
		return paginate(func(page int) (int, error) {
			resp, err := p.clients.fileServers.ListFileServers(&page, intPtr(pageSize), nil, nil, nil)
			if err != nil {
				// Files is an optional product; a Prism Central without it
				// answers 404 or 501 here. Reporting it as unsupported lets the
				// precheck record an incomplete check instead of failing the run.
				if isNotFound(err) || isNotImplemented(err) {
					return 0, service.ErrUnsupported
				}
				return 0, fmt.Errorf("list file servers: %w", err)
			}
			if resp == nil || resp.Data == nil {
				return 0, nil
			}
			items, err := coerceList[filesFileServer](resp.Data.GetValue())
			if err != nil {
				return 0, err
			}
			for _, f := range items {
				out = append(out, convertFileServer(f))
			}
			return len(items), nil
		})
	})
	return out, err
}

func (p *PrismCentral) GetProtectionInfo(ctx context.Context, entityExtID string) (model.ProtectionInfo, error) {
	var out model.ProtectionInfo
	err := guard("read the protection status of "+entityExtID, func() error {
		resp, err := p.clients.protectedResources.GetProtectedResourceById(&entityExtID)
		if err != nil {
			if isNotFound(err) {
				return service.ErrNotFound
			}
			if isNotImplemented(err) {
				return service.ErrUnsupported
			}
			return fmt.Errorf("get protected resource %s: %w", entityExtID, err)
		}
		if resp == nil || resp.Data == nil {
			return service.ErrNotFound
		}
		pr, err := coerceOne[dataprotectionProtectedResource](resp.Data.GetValue())
		if err != nil {
			return err
		}
		out = convertProtectionInfo(pr)
		return nil
	})
	return out, err
}

func (p *PrismCentral) MigrateSubnets(ctx context.Context, subnetExtIDs []string) (string, error) {
	if len(subnetExtIDs) == 0 {
		return "", fmt.Errorf("no subnets supplied for migration")
	}
	var taskExtID string
	err := guard("submit the subnet migration", func() error {
		spec := networkingconfig.NewVlanSubnetMigrationSpec()
		spec.Subnets = make([]networkingconfig.SubnetInfo, 0, len(subnetExtIDs))
		for i := range subnetExtIDs {
			item := networkingconfig.NewSubnetInfo()
			item.SubnetUuid = &subnetExtIDs[i]
			spec.Subnets = append(spec.Subnets, *item)
		}

		resp, err := p.clients.subnetMigrations.MigrateSubnets(spec)
		if err != nil {
			return fmt.Errorf("migrate subnets: %w", err)
		}
		if resp == nil || resp.Data == nil {
			return fmt.Errorf("migrate subnets: server returned no task reference")
		}
		ref, err := coerceOne[networkingprism.TaskReference](resp.Data.GetValue())
		if err != nil {
			return err
		}
		if ref.ExtId == nil || *ref.ExtId == "" {
			return fmt.Errorf("migrate subnets: task reference carried no UUID")
		}
		taskExtID = *ref.ExtId
		return nil
	})
	return taskExtID, err
}

func (p *PrismCentral) GetTask(ctx context.Context, taskExtID string) (model.Task, error) {
	var out model.Task
	err := guard("read task "+taskExtID, func() error {
		resp, err := p.clients.tasks.GetTaskById(&taskExtID, nil)
		if err != nil {
			return fmt.Errorf("get task %s: %w", taskExtID, err)
		}
		if resp == nil || resp.Data == nil {
			return fmt.Errorf("get task %s: %w", taskExtID, service.ErrNotFound)
		}
		t, err := coerceOne[prismTask](resp.Data.GetValue())
		if err != nil {
			return err
		}
		out = convertTask(t)
		return nil
	})
	return out, err
}

// listClusters is shared by the Prism Central and Prism Element backends.
func listClusters(clients *clientSet) ([]model.Cluster, error) {
	var out []model.Cluster
	err := guard("list the clusters", func() error {
		return paginate(func(page int) (int, error) {
			resp, err := clients.clusters.ListClusters(&page, intPtr(pageSize), nil, nil, nil, nil, nil)
			if err != nil {
				return 0, fmt.Errorf("list clusters: %w", err)
			}
			if resp == nil || resp.Data == nil {
				return 0, nil
			}
			items, err := coerceList[clustermgmtCluster](resp.Data.GetValue())
			if err != nil {
				return 0, err
			}
			for _, c := range items {
				out = append(out, convertCluster(c))
			}
			return len(items), nil
		})
	})
	return out, err
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

// statusOf extracts the HTTP status line from a generated client error.
//
// Every namespace module declares its own GenericOpenAPIError type whose Error()
// returns just the response body, but the struct also carries the status line in
// a Status field. Reading that field reflectively beats matching on error text,
// which matters most for the protected-resource lookup where a 404 is the normal
// answer for an unprotected VM.
func statusOf(err error) string {
	v := reflect.ValueOf(err)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	if field := v.FieldByName("Status"); field.IsValid() && field.Kind() == reflect.String {
		return field.String()
	}
	return ""
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(statusOf(err), "404") {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "404") || strings.Contains(msg, "not found")
}

func isNotImplemented(err error) bool {
	if err == nil {
		return false
	}
	status := statusOf(err)
	if strings.Contains(status, "501") || strings.Contains(status, "503") {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "501") || strings.Contains(msg, "not implemented")
}

var _ service.PrismCentral = (*PrismCentral)(nil)
