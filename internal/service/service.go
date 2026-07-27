// Package service defines the two fundamental service layers the components
// call into. One implementation is backed by the Nutanix Go SDK
// (internal/service/sdk) and the other by the Nutanix v4 REST APIs
// (internal/service/rest). Components depend only on the interfaces here.
package service

import (
	"context"
	"errors"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
)

// ErrUnsupported is returned by a backend that cannot service a call, for
// example an optional inventory API that a given Prism version does not expose.
// Callers treat it as "check could not be completed" rather than as a failure.
var ErrUnsupported = errors.New("operation not supported by this service backend")

// ErrNotFound is returned when a looked-up entity does not exist. The data
// protection lookup relies on it: an unprotected VM has no protected-resource
// record.
var ErrNotFound = errors.New("entity not found")

// Backend names a service layer implementation.
type Backend string

const (
	BackendGoSDK Backend = "go-sdk"
	BackendREST  Backend = "v4-rest"
)

// PrismCentral is everything the components need from a Prism Central. It spans
// the networking, VMM, cluster management, Prism, data protection and Files
// namespaces of the v4 API surface.
type PrismCentral interface {
	// Backend reports which service layer is answering.
	Backend() Backend

	// Version returns the Prism Central version.
	Version(ctx context.Context) (model.ProductVersion, error)

	// ListClusters returns the Prism Element clusters registered to this PC.
	ListClusters(ctx context.Context) ([]model.Cluster, error)

	// ListNetworkControllers returns the Flow Virtual Networking controllers.
	// An empty slice means advanced networking is not enabled.
	ListNetworkControllers(ctx context.Context) ([]model.NetworkController, error)

	// ListSubnets returns every subnet visible to this PC across all clusters.
	ListSubnets(ctx context.Context) ([]model.Subnet, error)

	// GetSubnet re-reads one subnet, used to confirm state after a migration.
	GetSubnet(ctx context.Context, extID string) (model.Subnet, error)

	// ListVnicsBySubnet returns the vNICs attached to a subnet. This is the
	// authoritative MAC-to-subnet mapping and does not require walking VMs.
	ListVnicsBySubnet(ctx context.Context, subnetExtID string) ([]model.Vnic, error)

	// ListVMs returns every AHV VM visible to this PC with its NIC
	// configuration, which is where the VLAN mode and trunked VLAN list live.
	ListVMs(ctx context.Context) ([]model.VM, error)

	// ListFileServers returns the Nutanix Files instances known to this PC.
	ListFileServers(ctx context.Context) ([]model.FileServer, error)

	// GetProtectionInfo reports how an entity is protected. It returns
	// ErrNotFound when the entity is not protected.
	GetProtectionInfo(ctx context.Context, entityExtID string) (model.ProtectionInfo, error)

	// MigrateSubnets submits the basic-to-advanced migration for the given
	// subnet UUIDs and returns the task UUID to poll.
	MigrateSubnets(ctx context.Context, subnetExtIDs []string) (string, error)

	// GetTask reads a Prism task.
	GetTask(ctx context.Context, taskExtID string) (model.Task, error)
}

// PrismElement is what the components need from a Prism Element. Beyond the
// version it exposes the two cluster-local inventories that Prism Central does
// not surface: legacy protection domains and file server network assignments.
type PrismElement interface {
	Backend() Backend

	// Version returns the AOS version of the cluster.
	Version(ctx context.Context) (model.ProductVersion, error)

	// ListProtectionDomains returns the legacy protection domains configured on
	// the cluster. Returns ErrUnsupported when the backend cannot read them.
	ListProtectionDomains(ctx context.Context) ([]model.ProtectionDomain, error)

	// ListFileServerNetworks returns the subnets each file server consumes.
	// Returns ErrUnsupported when the backend cannot read them.
	ListFileServerNetworks(ctx context.Context) ([]model.FileServerNetwork, error)
}
