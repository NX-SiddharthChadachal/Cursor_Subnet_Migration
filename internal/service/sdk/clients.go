// Package sdk implements the service layer on top of the Nutanix Go SDK
// clients published at github.com/nutanix/ntnx-api-golang-clients.
//
// Each v4 namespace ships as its own Go module with its own generated
// client.ApiClient type, so there is no single shared client to configure.
// clientSet builds and holds one per namespace for a single endpoint.
package sdk

import (
	"os"
	"time"

	clustermgmtapi "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/api"
	clustermgmtclient "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/client"
	dataprotectionapi "github.com/nutanix/ntnx-api-golang-clients/dataprotection-go-client/v4/api"
	dataprotectionclient "github.com/nutanix/ntnx-api-golang-clients/dataprotection-go-client/v4/client"
	filesapi "github.com/nutanix/ntnx-api-golang-clients/files-go-client/v4/api"
	filesclient "github.com/nutanix/ntnx-api-golang-clients/files-go-client/v4/client"
	networkingapi "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/api"
	networkingclient "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/client"
	prismapi "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/api"
	prismclient "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/client"
	vmmapi "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/api"
	vmmclient "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/client"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
)

// pageSize is the number of records requested per list call. The v4 APIs cap
// the limit at 100 for most collections.
const pageSize = 100

// requestTimeout bounds a single SDK call. Subnet migration submission returns a
// task reference immediately, so no call here is long-running.
const requestTimeout = 60 * time.Second

type clientSet struct {
	subnets            *networkingapi.SubnetsApi
	subnetMigrations   *networkingapi.SubnetMigrationsApi
	networkControllers *networkingapi.NetworkControllersApi
	vms                *vmmapi.VmApi
	clusters           *clustermgmtapi.ClustersApi
	domainManager      *prismapi.DomainManagerApi
	tasks              *prismapi.TasksApi
	protectedResources *dataprotectionapi.ProtectedResourcesApi
	fileServers        *filesapi.FileServersApi
}

func newClientSet(ep model.Endpoint, quiet bool) *clientSet {
	if quiet {
		// Each generated client builds its own logrus logger that writes request
		// lines to os.Stderr, and captures the writer at construction time with
		// no exported way to change it afterwards. Pointing os.Stderr at the
		// null device for the duration of construction leaves those loggers
		// writing nowhere, so the run log stays the only thing on stderr.
		// Construction happens once, before any goroutine starts.
		defer silenceStderr()()
	}

	net := networkingclient.NewApiClient()
	applyNetworking(net, ep)

	vmm := vmmclient.NewApiClient()
	applyVMM(vmm, ep)

	cm := clustermgmtclient.NewApiClient()
	applyClusterMgmt(cm, ep)

	pr := prismclient.NewApiClient()
	applyPrism(pr, ep)

	dp := dataprotectionclient.NewApiClient()
	applyDataProtection(dp, ep)

	fs := filesclient.NewApiClient()
	applyFiles(fs, ep)

	return &clientSet{
		subnets:            networkingapi.NewSubnetsApi(net),
		subnetMigrations:   networkingapi.NewSubnetMigrationsApi(net),
		networkControllers: networkingapi.NewNetworkControllersApi(net),
		vms:                vmmapi.NewVmApi(vmm),
		clusters:           clustermgmtapi.NewClustersApi(cm),
		domainManager:      prismapi.NewDomainManagerApi(pr),
		tasks:              prismapi.NewTasksApi(pr),
		protectedResources: dataprotectionapi.NewProtectedResourcesApi(dp),
		fileServers:        filesapi.NewFileServersApi(fs),
	}
}

// devNull stays open for the lifetime of the process because the SDK loggers
// keep writing to whatever writer they captured.
var devNull *os.File

// silenceStderr redirects os.Stderr to the null device and returns a function
// that restores it.
func silenceStderr() func() {
	if devNull == nil {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return func() {}
		}
		devNull = f
	}
	original := os.Stderr
	os.Stderr = devNull
	return func() { os.Stderr = original }
}

// The generated ApiClient types are structurally identical but nominally
// distinct across modules, so each namespace needs its own small applier. Go
// generics cannot help here because the types share no interface.

func applyNetworking(c *networkingclient.ApiClient, ep model.Endpoint) {
	c.Host = ep.Host
	c.Port = ep.Port
	c.Username = ep.Username
	c.Password = ep.Password
	c.SetVerifySSL(!ep.Insecure)
	c.ReadTimeout = requestTimeout
	c.ConnectTimeout = 30 * time.Second
}

func applyVMM(c *vmmclient.ApiClient, ep model.Endpoint) {
	c.Host = ep.Host
	c.Port = ep.Port
	c.Username = ep.Username
	c.Password = ep.Password
	c.SetVerifySSL(!ep.Insecure)
	c.ReadTimeout = requestTimeout
	c.ConnectTimeout = 30 * time.Second
}

func applyClusterMgmt(c *clustermgmtclient.ApiClient, ep model.Endpoint) {
	c.Host = ep.Host
	c.Port = ep.Port
	c.Username = ep.Username
	c.Password = ep.Password
	c.SetVerifySSL(!ep.Insecure)
	c.ReadTimeout = requestTimeout
	c.ConnectTimeout = 30 * time.Second
}

func applyPrism(c *prismclient.ApiClient, ep model.Endpoint) {
	c.Host = ep.Host
	c.Port = ep.Port
	c.Username = ep.Username
	c.Password = ep.Password
	c.SetVerifySSL(!ep.Insecure)
	c.ReadTimeout = requestTimeout
	c.ConnectTimeout = 30 * time.Second
}

func applyDataProtection(c *dataprotectionclient.ApiClient, ep model.Endpoint) {
	c.Host = ep.Host
	c.Port = ep.Port
	c.Username = ep.Username
	c.Password = ep.Password
	c.SetVerifySSL(!ep.Insecure)
	c.ReadTimeout = requestTimeout
	c.ConnectTimeout = 30 * time.Second
}

func applyFiles(c *filesclient.ApiClient, ep model.Endpoint) {
	c.Host = ep.Host
	c.Port = ep.Port
	c.Username = ep.Username
	c.Password = ep.Password
	c.SetVerifySSL(!ep.Insecure)
	c.ReadTimeout = requestTimeout
	c.ConnectTimeout = 30 * time.Second
}
