package preflight

import (
	"context"
	"errors"
	"fmt"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/config"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/logging"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service/composite"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service/rest"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service/sdk"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/version"
)

// Result is what preflight hands to the Controller.
type Result struct {
	PC          service.PrismCentral
	PE          service.PrismElement
	Environment model.EnvironmentInfo
}

// Run performs the reachability probes, reads the versions and selects the
// service backend.
//
// Version discovery deliberately uses the REST client first: it is dependency
// free and answers the question the backend choice depends on. Once the versions
// and the network controller are known, the Go SDK is preferred, because it
// carries the vendor's own request retries, version negotiation and models.
func Run(ctx context.Context, cfg *config.Config, log *logging.Logger) (*Result, error) {
	log = log.WithPhase("preflight")

	pcInfo, err := probeEndpoint(ctx, cfg.PrismCentral, cfg.SkipICMP, log)
	if err != nil {
		return nil, err
	}

	pcProbe := rest.NewPrismCentral(cfg.PrismCentral)
	pcVersion, err := pcProbe.Version(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Prism Central version from %s: %w", cfg.PrismCentral.Host, err)
	}
	pcInfo.Version = pcVersion
	pcInfo.Supported = version.IsSupported(pcVersion)
	log.Infof("Prism Central %s reports version %s", cfg.PrismCentral.Host, pcVersion)

	var (
		peInfo    *model.EndpointInfo
		peVersion model.ProductVersion
		peProbe   *rest.PrismElement
	)
	if cfg.PrismElement != nil {
		info, err := probeEndpoint(ctx, *cfg.PrismElement, cfg.SkipICMP, log)
		if err != nil {
			return nil, err
		}
		peProbe = rest.NewPrismElement(*cfg.PrismElement)
		peVersion, err = peProbe.Version(ctx)
		if err != nil {
			return nil, fmt.Errorf("read AOS version from %s: %w", cfg.PrismElement.Host, err)
		}
		info.Version = peVersion
		info.Supported = version.IsSupported(peVersion)
		peInfo = &info
		log.Infof("Prism Element %s reports AOS version %s", cfg.PrismElement.Host, peVersion)
	} else {
		log.Warnf("No Prism Element endpoint supplied; the protection domain and file server network checks will be reported as incomplete")
	}

	if err := gateVersions(cfg, pcInfo, peInfo, log); err != nil {
		return nil, err
	}

	controllers, err := pcProbe.ListNetworkControllers(ctx)
	if err != nil {
		log.Warnf("Could not read the Flow Virtual Networking controller: %v", err)
	}
	for _, c := range controllers {
		log.Infof("Network controller %s: version=%s status=%s defaultVlanStack=%s", c.ExtID, c.Version, c.Status, c.DefaultVlanStack)
	}
	if len(controllers) == 0 {
		log.Warnf("No Flow Virtual Networking controller was found; advanced networking may not be enabled on this Prism Central, in which case the migration will be rejected")
	}

	backend := selectBackend(cfg, pcInfo, peInfo, controllers, log)

	pc, pe := build(backend, cfg, peProbe)
	pcInfo.Version.Source = fmt.Sprintf("%s (%s backend)", pcInfo.Version.Source, backend)

	clusters, err := pc.ListClusters(ctx)
	if err != nil {
		log.Warnf("Could not enumerate the registered clusters: %v", err)
	}
	for _, c := range clusters {
		log.Infof("Registered cluster %s (%s): AOS %s hypervisors=%v", c.Name, c.ExtID, c.AOSVersion, c.HypervisorTypes)
	}

	return &Result{
		PC: pc,
		PE: pe,
		Environment: model.EnvironmentInfo{
			PrismCentral:       pcInfo,
			PrismElement:       peInfo,
			Clusters:           clusters,
			NetworkControllers: controllers,
			Backend:            string(backend),
		},
	}, nil
}

func probeEndpoint(ctx context.Context, ep model.Endpoint, skipICMP bool, log *logging.Logger) (model.EndpointInfo, error) {
	info := model.EndpointInfo{Kind: ep.Kind, Address: ep.Address()}

	if !skipICMP {
		if reachable, known := CheckICMP(ctx, ep); known {
			info.ICMPReachable = &reachable
			if reachable {
				log.Debugf("%s answers ICMP", ep.Host)
			} else {
				log.Debugf("%s does not answer ICMP, which is common when ICMP is filtered", ep.Host)
			}
		}
	}

	if err := CheckTCP(ctx, ep); err != nil {
		return info, fmt.Errorf("%s endpoint failed the connectivity check: %w", ep.Kind, err)
	}
	info.TCPReachable = true
	log.Infof("%s at %s accepts connections on the Prism API port", ep.Kind, ep.Address())
	return info, nil
}

// gateVersions enforces the qualified-release policy. This iteration is only
// validated against Prism Central and AOS 7.5; older releases need the internal
// command pipeline that is tracked as future work.
func gateVersions(cfg *config.Config, pc model.EndpointInfo, pe *model.EndpointInfo, log *logging.Logger) error {
	var unsupported []string
	if !pc.Supported {
		unsupported = append(unsupported, fmt.Sprintf("Prism Central %s", pc.Version))
	}
	if pe != nil && !pe.Supported {
		unsupported = append(unsupported, fmt.Sprintf("AOS %s", pe.Version))
	}
	if len(unsupported) == 0 {
		return nil
	}
	if cfg.AllowUnsupportedVersion {
		for _, u := range unsupported {
			log.Warnf("%s is outside the qualified release %s; continuing because --allow-unsupported-version was given", u, version.Requirement())
		}
		return nil
	}
	return fmt.Errorf("this tool is qualified for Prism Central and AOS %s only, but found %v; re-run with --allow-unsupported-version to proceed anyway",
		version.Requirement(), unsupported)
}

// selectBackend implements the "Go SDK first, v4 REST if necessary" rule.
func selectBackend(cfg *config.Config, pc model.EndpointInfo, pe *model.EndpointInfo, controllers []model.NetworkController, log *logging.Logger) service.Backend {
	if cfg.Backend != "" {
		log.Infof("Using the %s service layer because it was requested explicitly", cfg.Backend)
		return cfg.Backend
	}

	var reasons []string
	if !pc.Supported {
		reasons = append(reasons, fmt.Sprintf("Prism Central %s is not the qualified release", pc.Version))
	}
	if pe != nil && !pe.Supported {
		reasons = append(reasons, fmt.Sprintf("AOS %s is not the qualified release", pe.Version))
	}
	if len(controllers) == 0 {
		reasons = append(reasons, "no Flow Virtual Networking controller was reported")
	} else {
		usable := false
		for _, c := range controllers {
			if c.IsUsable() {
				usable = true
				break
			}
		}
		if !usable {
			reasons = append(reasons, "the Flow Virtual Networking controller is not up")
		}
	}

	if len(reasons) == 0 {
		log.Infof("Using the %s service layer: Prism Central, AOS and the network controller are all at supported levels", service.BackendGoSDK)
		return service.BackendGoSDK
	}
	for _, r := range reasons {
		log.Warnf("Falling back to the %s service layer because %s", service.BackendREST, r)
	}
	return service.BackendREST
}

func build(backend service.Backend, cfg *config.Config, legacy *rest.PrismElement) (service.PrismCentral, service.PrismElement) {
	var (
		pc service.PrismCentral
		pe service.PrismElement
	)
	switch backend {
	case service.BackendGoSDK:
		pc = sdk.NewPrismCentral(cfg.PrismCentral)
		if cfg.PrismElement != nil {
			pe = composite.NewPrismElement(sdk.NewPrismElement(*cfg.PrismElement), legacy)
		}
	default:
		pc = rest.NewPrismCentral(cfg.PrismCentral)
		if cfg.PrismElement != nil {
			pe = legacy
		}
	}
	return pc, pe
}

// ErrUnreachable is returned when an endpoint fails the TCP probe. Callers use
// it to distinguish a networking problem from an authentication problem.
var ErrUnreachable = errors.New("endpoint unreachable")
