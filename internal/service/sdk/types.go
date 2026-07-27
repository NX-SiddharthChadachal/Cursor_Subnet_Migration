package sdk

import (
	clustermgmtconfig "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	dataprotectionconfig "github.com/nutanix/ntnx-api-golang-clients/dataprotection-go-client/v4/models/dataprotection/v4/config"
	filesconfig "github.com/nutanix/ntnx-api-golang-clients/files-go-client/v4/models/files/v4/config"
	prismconfig "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	ahvconfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
)

// The generated list responses carry their payload in a oneOf wrapper whose
// GetValue returns interface{}. These aliases keep the type assertions at the
// call sites readable.
type (
	prismDomainManager              = prismconfig.DomainManager
	prismTask                       = prismconfig.Task
	clustermgmtCluster              = clustermgmtconfig.Cluster
	ahvVM                           = ahvconfig.Vm
	filesFileServer                 = filesconfig.FileServer
	dataprotectionProtectedResource = dataprotectionconfig.ProtectedResource
)
