// Package config turns command line flags, environment variables and
// interactive prompts into the run configuration.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/logging"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
)

// DefaultPort is the Prism API port on both Prism Central and Prism Element.
const DefaultPort = 9440

// Config is the fully resolved run configuration.
type Config struct {
	PrismCentral model.Endpoint
	// PrismElement is optional. Without it the protection domain and file server
	// network prechecks degrade to warnings, because those inventories are only
	// readable from a cluster.
	PrismElement *model.Endpoint

	// Backend pins the service layer. Empty means auto-select.
	Backend service.Backend
	// AllowUnsupportedVersion proceeds even when Prism Central or AOS is not the
	// qualified release.
	AllowUnsupportedVersion bool
	// SkipICMP omits the best-effort ping probe and relies on the TCP check.
	SkipICMP bool

	// SubnetFilter restricts the run to these subnet names or UUIDs. Empty means
	// every basic VLAN subnet found.
	SubnetFilter []string
	// ExcludeSubnets removes subnet names or UUIDs from the candidate list, which
	// is how an operator acts on the "exclude it from this migration" advice.
	ExcludeSubnets []string

	// BatchSize is how many subnets are submitted per migration task. One keeps
	// the rollout strictly rolling, which is the default.
	BatchSize int
	// PollInterval is how often a migration task is polled.
	PollInterval time.Duration
	// TaskTimeout bounds how long one migration task may run.
	TaskTimeout time.Duration
	// ContinueOnFailure keeps migrating later subnets after one fails.
	ContinueOnFailure bool
	// Concurrency bounds the parallel API calls the prechecks issue.
	Concurrency int

	// DryRun runs prechecks and reports eligibility without migrating anything.
	DryRun bool
	// AssumeYes skips the confirmation prompt before the migration starts.
	AssumeYes bool

	LogLevel   logging.Level
	LogFile    string
	ReportPath string
	// NonInteractive fails instead of prompting for a missing input.
	NonInteractive bool
}

// Load parses flags and environment variables, then prompts for whatever is
// still missing. args excludes the program name.
func Load(args []string, stdin io.Reader, stdout io.Writer) (*Config, error) {
	var (
		pcHost      string
		pcUser      string
		pcPassword  string
		peHost      string
		peUser      string
		pePassword  string
		port        int
		skipPE      bool
		backend     string
		logLevel    string
		subnets     string
		exclude     string
		insecure    bool
		verifyTLS   bool
		pollSeconds int
		taskMinutes int
		cfg         = &Config{}
	)

	fs := flag.NewFlagSet("subnet-migrator", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { usage(stdout, fs) }

	fs.StringVar(&pcHost, "pc", envOr("NTNX_PC_HOST", ""), "Prism Central IP address or FQDN")
	fs.StringVar(&pcUser, "pc-user", envOr("NTNX_PC_USERNAME", "admin"), "Prism Central admin user")
	fs.StringVar(&pcPassword, "pc-password", os.Getenv("NTNX_PC_PASSWORD"), "Prism Central password (prefer the NTNX_PC_PASSWORD environment variable or the prompt)")
	fs.StringVar(&peHost, "pe", envOr("NTNX_PE_HOST", ""), "Prism Element IP address or FQDN")
	fs.StringVar(&peUser, "pe-user", envOr("NTNX_PE_USERNAME", "admin"), "Prism Element admin user")
	fs.StringVar(&pePassword, "pe-password", os.Getenv("NTNX_PE_PASSWORD"), "Prism Element password (prefer the NTNX_PE_PASSWORD environment variable or the prompt)")
	fs.BoolVar(&skipPE, "skip-pe", envBool("NTNX_SKIP_PE", false), "Do not connect to Prism Element; the protection domain and file server network checks become warnings")
	fs.IntVar(&port, "port", envInt("NTNX_PORT", DefaultPort), "Prism API port")
	fs.BoolVar(&insecure, "insecure", envBool("NTNX_INSECURE", true), "Skip TLS certificate verification (Prism ships a self-signed certificate)")
	fs.BoolVar(&verifyTLS, "verify-tls", false, "Verify TLS certificates; overrides --insecure")

	fs.StringVar(&backend, "backend", envOr("NTNX_BACKEND", ""), "Service layer to use: go-sdk, v4-rest, or empty to auto-select")
	fs.BoolVar(&cfg.AllowUnsupportedVersion, "allow-unsupported-version", envBool("NTNX_ALLOW_UNSUPPORTED_VERSION", false), "Continue even if Prism Central or AOS is not the qualified release")
	fs.BoolVar(&cfg.SkipICMP, "skip-ping", envBool("NTNX_SKIP_PING", false), "Skip the best-effort ICMP probe and rely on the TCP port check")

	fs.StringVar(&subnets, "subnets", envOr("NTNX_SUBNETS", ""), "Comma separated subnet names or UUIDs to consider; default is every basic VLAN subnet")
	fs.StringVar(&exclude, "exclude-subnets", envOr("NTNX_EXCLUDE_SUBNETS", ""), "Comma separated subnet names or UUIDs to leave out of this migration")

	fs.IntVar(&cfg.BatchSize, "batch-size", envInt("NTNX_BATCH_SIZE", 1), "Subnets submitted per migration task; 1 migrates strictly one at a time")
	fs.IntVar(&pollSeconds, "poll-interval", envInt("NTNX_POLL_INTERVAL", 10), "Seconds between migration task polls")
	fs.IntVar(&taskMinutes, "task-timeout", envInt("NTNX_TASK_TIMEOUT", 30), "Minutes to wait for one migration task before giving up")
	fs.BoolVar(&cfg.ContinueOnFailure, "continue-on-failure", envBool("NTNX_CONTINUE_ON_FAILURE", false), "Keep migrating the remaining subnets after one fails")
	fs.IntVar(&cfg.Concurrency, "concurrency", envInt("NTNX_CONCURRENCY", 8), "Maximum parallel API calls issued by the prechecks")

	fs.BoolVar(&cfg.DryRun, "dry-run", envBool("NTNX_DRY_RUN", false), "Run the prechecks and report eligibility without migrating anything")
	fs.BoolVar(&cfg.AssumeYes, "yes", envBool("NTNX_ASSUME_YES", false), "Do not ask for confirmation before migrating")
	fs.BoolVar(&cfg.NonInteractive, "non-interactive", envBool("NTNX_NON_INTERACTIVE", false), "Fail instead of prompting for missing input")

	fs.StringVar(&logLevel, "log-level", envOr("NTNX_LOG_LEVEL", "info"), "Log level: debug, info, warn or error")
	fs.StringVar(&cfg.LogFile, "log-file", envOr("NTNX_LOG_FILE", ""), "Also write the run log to this file")
	fs.StringVar(&cfg.ReportPath, "report", envOr("NTNX_REPORT", ""), "Write the JSON run report to this path")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg.LogLevel = logging.ParseLevel(logLevel)
	cfg.SubnetFilter = splitList(subnets)
	cfg.ExcludeSubnets = splitList(exclude)
	cfg.PollInterval = time.Duration(pollSeconds) * time.Second
	cfg.TaskTimeout = time.Duration(taskMinutes) * time.Minute
	if verifyTLS {
		insecure = false
	}

	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "":
		cfg.Backend = ""
	case string(service.BackendGoSDK), "sdk", "go":
		cfg.Backend = service.BackendGoSDK
	case string(service.BackendREST), "rest", "v4":
		cfg.Backend = service.BackendREST
	default:
		return nil, fmt.Errorf("unknown backend %q: use go-sdk or v4-rest", backend)
	}

	prompter := newPrompter(stdin, stdout, cfg.NonInteractive)

	// Prism Central is mandatory: it owns the subnets and the migration action.
	if pcHost == "" {
		host, err := prompter.text("Prism Central IP address or FQDN", "")
		if err != nil {
			return nil, err
		}
		pcHost = host
	}
	if pcUser == "" {
		user, err := prompter.text("Prism Central admin user", "admin")
		if err != nil {
			return nil, err
		}
		pcUser = user
	}
	if pcPassword == "" {
		pw, err := prompter.secret(fmt.Sprintf("Password for %s@%s", pcUser, pcHost))
		if err != nil {
			return nil, err
		}
		pcPassword = pw
	}
	cfg.PrismCentral = model.Endpoint{
		Kind:     model.EndpointPrismCentral,
		Host:     normalizeHost(pcHost),
		Port:     port,
		Username: pcUser,
		Password: pcPassword,
		Insecure: insecure,
	}

	if !skipPE {
		if peHost == "" {
			host, err := prompter.text("Prism Element IP address or FQDN (blank to skip)", "")
			if err != nil {
				return nil, err
			}
			peHost = host
		}
		if peHost != "" {
			if peUser == "" {
				user, err := prompter.text("Prism Element admin user", "admin")
				if err != nil {
					return nil, err
				}
				peUser = user
			}
			if pePassword == "" {
				pw, err := prompter.secret(fmt.Sprintf("Password for %s@%s", peUser, peHost))
				if err != nil {
					return nil, err
				}
				pePassword = pw
			}
			cfg.PrismElement = &model.Endpoint{
				Kind:     model.EndpointPrismElement,
				Host:     normalizeHost(peHost),
				Port:     port,
				Username: peUser,
				Password: pePassword,
				Insecure: insecure,
			}
		}
	}

	return cfg, cfg.validate()
}

func (c *Config) validate() error {
	var problems []string
	if c.PrismCentral.Host == "" {
		problems = append(problems, "Prism Central host is required")
	}
	if c.PrismCentral.Username == "" {
		problems = append(problems, "Prism Central user is required")
	}
	if c.PrismCentral.Password == "" {
		problems = append(problems, "Prism Central password is required")
	}
	if c.PrismCentral.Port <= 0 || c.PrismCentral.Port > 65535 {
		problems = append(problems, "port must be between 1 and 65535")
	}
	if c.BatchSize < 1 {
		problems = append(problems, "batch-size must be at least 1")
	}
	if c.Concurrency < 1 {
		problems = append(problems, "concurrency must be at least 1")
	}
	if c.PollInterval <= 0 {
		problems = append(problems, "poll-interval must be positive")
	}
	if c.TaskTimeout <= 0 {
		problems = append(problems, "task-timeout must be positive")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// normalizeHost strips a scheme, a trailing slash and a port suffix so that a
// pasted Prism URL works as well as a bare address.
func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimSuffix(host, "/")
	// Leave IPv6 literals alone; only strip a ":9440"-style suffix.
	if !strings.Contains(host, "[") && strings.Count(host, ":") == 1 {
		if idx := strings.LastIndex(host, ":"); idx > 0 {
			if _, err := strconv.Atoi(host[idx+1:]); err == nil {
				host = host[:idx]
			}
		}
	}
	return host
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return fallback
}

func usage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, `subnet-migrator migrates Nutanix VLAN subnets from the basic (Acropolis) network
stack to the advanced (Flow Virtual Networking) stack.

It runs three phases in order: prechecks, migration execution, postchecks.

Usage:
  subnet-migrator [flags]

Every input can come from a flag, an environment variable or an interactive
prompt. Passwords are read without echo when prompted.

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprintf(w, `
Examples:
  # Interactive: prompts for hosts, users and passwords.
  subnet-migrator

  # Report only, no changes.
  subnet-migrator --pc pc.example.com --pe pe.example.com --dry-run

  # Migrate two named subnets, one at a time, writing a JSON report.
  subnet-migrator --pc 10.0.0.10 --pe 10.0.0.20 \
      --subnets vlan-100,vlan-200 --report ./run.json --yes
`)
}
