package rest

import (
	"context"
	"fmt"
	"strings"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
)

// Two of the dependency prechecks need inventories that no v4 namespace exposes:
//
//   - Legacy protection domains, which live in the cluster-local Prism Gateway
//     v2.0 API. The v4 data protection namespace only models Prism Central
//     protection policies and their protected resources.
//   - The subnets a Nutanix Files instance is configured with. The v4 Files
//     namespace models data protection for file servers, not their networks, so
//     the internal (storage) and client network UUIDs are only readable from the
//     Prism Gateway v1 vfilers API.
//
// Both endpoints are cluster-local, which is why the tool asks for a Prism
// Element endpoint in addition to Prism Central.

const (
	legacyProtectionDomainsPath = "/PrismGateway/services/rest/v2.0/protection_domains"
	legacyVFilersPath           = "/PrismGateway/services/rest/v1/vfilers"
)

type legacyProtectionDomainEnvelope struct {
	Entities []struct {
		Name   string `json:"name"`
		Active *bool  `json:"active"`
		VMs    []struct {
			VMID   string `json:"vm_id"`
			VMName string `json:"vm_name"`
		} `json:"vms"`
	} `json:"entities"`
}

func listProtectionDomains(ctx context.Context, c *Client) ([]model.ProtectionDomain, error) {
	var envelope legacyProtectionDomainEnvelope
	if err := c.getJSON(ctx, legacyProtectionDomainsPath, nil, &envelope); err != nil {
		return nil, translate(err)
	}
	out := make([]model.ProtectionDomain, 0, len(envelope.Entities))
	for _, e := range envelope.Entities {
		pd := model.ProtectionDomain{Name: e.Name, Active: derefBool(e.Active)}
		for _, vm := range e.VMs {
			if id := normalizeLegacyEntityID(vm.VMID); id != "" {
				pd.VMExtIDs = append(pd.VMExtIDs, id)
			}
			if vm.VMName != "" {
				pd.VMNames = append(pd.VMNames, vm.VMName)
			}
		}
		out = append(out, pd)
	}
	return out, nil
}

type legacyVFilerEnvelope struct {
	Entities []struct {
		UUID            string `json:"uuid"`
		Name            string `json:"name"`
		InternalNetwork *struct {
			UUID string `json:"uuid"`
		} `json:"internalNetwork"`
		ExternalNetworks []struct {
			UUID string `json:"uuid"`
		} `json:"externalNetworks"`
		NVMs []struct {
			UUID string `json:"uuid"`
		} `json:"nvms"`
		NVMUUIDs []string `json:"nvmUuids"`
	} `json:"entities"`
}

func listFileServerNetworks(ctx context.Context, c *Client) ([]model.FileServerNetwork, error) {
	var envelope legacyVFilerEnvelope
	if err := c.getJSON(ctx, legacyVFilersPath, nil, &envelope); err != nil {
		return nil, translate(err)
	}
	out := make([]model.FileServerNetwork, 0, len(envelope.Entities))
	for _, e := range envelope.Entities {
		fsn := model.FileServerNetwork{FileServerExtID: e.UUID, FileServerName: e.Name}
		if e.InternalNetwork != nil && e.InternalNetwork.UUID != "" {
			fsn.SubnetExtIDs = append(fsn.SubnetExtIDs, e.InternalNetwork.UUID)
		}
		for _, ext := range e.ExternalNetworks {
			if ext.UUID != "" {
				fsn.SubnetExtIDs = append(fsn.SubnetExtIDs, ext.UUID)
			}
		}
		for _, nvm := range e.NVMs {
			if nvm.UUID != "" {
				fsn.VMExtIDs = append(fsn.VMExtIDs, nvm.UUID)
			}
		}
		for _, id := range e.NVMUUIDs {
			if id != "" {
				fsn.VMExtIDs = append(fsn.VMExtIDs, id)
			}
		}
		out = append(out, fsn)
	}
	return out, nil
}

// normalizeLegacyEntityID strips the "<cluster-id>::" prefix the legacy APIs put
// in front of some entity identifiers so that they compare equal to v4 UUIDs.
func normalizeLegacyEntityID(id string) string {
	id = strings.TrimSpace(id)
	if idx := strings.LastIndex(id, "::"); idx >= 0 {
		id = id[idx+2:]
	}
	return id
}

// describeLegacyFailure wraps a legacy inventory error with the context an
// operator needs to decide whether to care.
func describeLegacyFailure(name string, err error) error {
	return fmt.Errorf("%s inventory unavailable: %w", name, err)
}
