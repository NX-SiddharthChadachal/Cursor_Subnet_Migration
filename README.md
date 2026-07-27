# Subnet Migrator

Migrates Nutanix VLAN subnets from the **basic** (Acropolis) network stack to the
**advanced** (Flow Virtual Networking) stack, with the prechecks and postchecks
that stop the known failure modes from taking a migration down half-way.

Qualified for **Prism Central 7.5 and AOS 7.5**.

## What it does

A run is three phases, in order:

**Prechecks** — decide which basic VLAN subnets are safe to move. Four conditions
make a subnet ineligible:

| Check | Why it blocks | Suggested action |
| --- | --- | --- |
| Trunked vNICs | Atlas will not take a port carrying more than one VLAN | Raise a support ticket |
| Duplicate MAC addresses | Collisions inside one VM, across VMs, or across clusters registered to the same Prism Central | Raise a support ticket |
| Nutanix Files usage | Migrating a file server's storage or client network disrupts it | Raise a support ticket, or exclude the subnet |
| Protection domain / policy usage | Migrating can disrupt replication for protected VMs | Raise a support ticket, or exclude the subnet |

**Execution** — submits the migration for the eligible subnets one subnet per
task by default, so a failure is attributable to a single subnet, and polls each
task to completion before starting the next.

**Postchecks** — re-reads the migrated subnets to confirm they report the
advanced stack, and rebuilds the MAC index from scratch, because moving ports
between the two stacks is exactly what can produce a new collision.

The duplicate MAC detection runs entirely over the v4 APIs, so it does not need
`nuclei` or SSH access to a CVM.

## Architecture

```
                    ┌────────────┐
                    │ Controller │  runs the phases, polls tasks, owns the log
                    └──────┬─────┘
        ┌──────────────────┼──────────────────┐
   ┌────┴─────┐     ┌──────┴──────┐    ┌──────┴─────┐
   │Prechecks │     │ Executioner │    │ Postchecks │
   └────┬─────┘     └──────┬──────┘    └──────┬─────┘
        └──────────────────┼──────────────────┘
              ┌────────────┴────────────┐
        ┌─────┴──────┐          ┌───────┴──────┐
        │ Go SDK     │          │ v4 REST      │   two service layers
        │ service    │          │ service      │
        └────────────┘          └──────────────┘
```

Both service layers implement the same interfaces (`internal/service`), so no
component knows which transport is in use. Preflight probes reachability and the
product versions, then prefers the Go SDK when Prism Central, AOS and the Flow
Virtual Networking controller are all at supported levels, and falls back to the
v4 REST layer otherwise. `--backend go-sdk|v4-rest` pins the choice.

Layout:

| Path | Role |
| --- | --- |
| `cmd/subnet-migrator` | entrypoint |
| `internal/config` | flags, environment variables, interactive prompts |
| `internal/preflight` | reachability probe, version gate, backend selection |
| `internal/controller` | phase orchestration and task polling |
| `internal/prechecks` | inventory gathering and the four checks |
| `internal/executor` | rolling migration submission |
| `internal/postchecks` | verification and MAC re-check |
| `internal/service/sdk` | service layer on the Nutanix Go SDK clients |
| `internal/service/rest` | service layer on the v4 REST APIs, plus the two legacy Prism Element inventories |
| `internal/model` | vendor-neutral domain types both layers produce |
| `internal/report` | text and JSON output |

### Why a Prism Element endpoint is asked for

Two inventories the dependency checks need have no v4 namespace and are only
readable from a cluster: legacy protection domains
(`/PrismGateway/services/rest/v2.0/protection_domains`) and the subnets each file
server uses (`/PrismGateway/services/rest/v1/vfilers`). Without a Prism Element
endpoint those two checks are reported as incomplete rather than as passing; the
run still works, using FSVM name matching and the Prism Central
protected-resource lookup as weaker substitutes.

## Build

Requires Go 1.22 or newer.

```bash
make build          # dist/linux-amd64/subnet-migrator and dist/windows-amd64/subnet-migrator.exe
make build-linux
make build-windows
make dev            # host platform, into bin/
```

## Run

Interactive — prompts for the addresses, users and passwords, reading passwords
without echo:

```bash
./subnet-migrator
```

Report only, changing nothing:

```bash
./subnet-migrator --pc pc.example.com --pe 10.0.0.20 --dry-run
```

Migrate two named subnets and write a JSON report:

```bash
./subnet-migrator --pc 10.0.0.10 --pe 10.0.0.20 \
    --subnets vlan-100,vlan-200 --report ./run.json --yes
```

Every input can come from a flag, an environment variable (`NTNX_PC_HOST`,
`NTNX_PC_PASSWORD`, `NTNX_PE_HOST`, `NTNX_PE_PASSWORD`, …) or a prompt. Run
`--help` for the full list.

Useful flags:

| Flag | Effect |
| --- | --- |
| `--dry-run` | prechecks and eligibility report only |
| `--subnets`, `--exclude-subnets` | limit the run by subnet name or UUID |
| `--batch-size` | subnets per migration task, default 1 |
| `--continue-on-failure` | keep going after a subnet fails |
| `--poll-interval`, `--task-timeout` | task polling behaviour |
| `--backend` | pin the service layer |
| `--report` | write the JSON run report |
| `--log-file`, `--log-level` | run log destination and verbosity (`debug` also shows SDK request traffic) |
| `--allow-unsupported-version` | proceed on a release other than 7.5 |
| `--verify-tls` | verify certificates; off by default because Prism ships a self-signed one |

Exit codes: `0` success, `1` failure, `2` partial success or a postcheck finding
that needs a support ticket.

## Output

The run log names every subnet as it moves through the phases. The final report,
in text on stdout and optionally as JSON, lists the subnets that were migrated
with the VMs attached to each, then the subnets that could not be migrated with
the reason and the suggested action.

## Not in this iteration

- Prism Central and AOS older than 7.5, which need the older API surface down to
  6.8 and an internal-command pipeline.
- Opening the support ticket automatically.
- Reading duplicate MAC addresses by SSH-ing into a PCVM, which would need an SSH
  service layer.
