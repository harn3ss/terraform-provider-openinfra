# terraform-provider-openinfra

A Terraform provider for [open-infra](https://github.com/harn3ss/open-infra) — declare
open-infra resources (applications, databases, virtual machines, functions, volumes, …) in
HCL instead of `kind:` manifests.

> **Status: scaffolding.** Nothing is published yet. See [Roadmap](#roadmap).

## Why a provider

open-infra resources are Kubernetes CRDs, so they *can* already be managed with the generic
`kubernetes_manifest` resource. A first-party provider exists for ergonomics, not raw
capability:

- **Typed resources and validation** — `openinfra_virtual_machine` with real attributes,
  plan-time validation, and documented fields, instead of an untyped YAML blob.
- **Sensible defaults and computed outputs** — connection details, assigned IPs, and status
  surfaced as attributes you can reference from other resources.
- **Mixing with the rest of your estate** — reference an open-infra database from the same
  config that manages DNS, TLS, or a cloud account.

## Example (target shape)

```hcl
provider "openinfra" {
  # defaults to the ambient kubeconfig / in-cluster config
  kubeconfig = "~/.kube/config"
}

resource "openinfra_virtual_machine" "dc" {
  name      = "windowsdc"
  namespace = "default"
  os        = "windows-server-2022"
  cpu       = 4
  memory    = "8Gi"
  running   = true
}

resource "openinfra_database" "app" {
  name      = "orders"
  namespace = "default"
  engine    = "postgres"
}

output "orders_connection_secret" {
  value = openinfra_database.app.connection_secret
}
```

## What's implemented

**Resources** (full CRUD + `terraform import` via `namespace/name`):

| Resource | Maps to |
|---|---|
| `openinfra_application` | `kind: Application` (container workload) |
| `openinfra_database` | `kind: Application` with `spec.database` (postgres/mysql/mongo/babelfish) |
| `openinfra_virtual_machine` | `kind: VirtualMachine` |

**Data sources** — one for **every** open-infra CRD (15): `application`, `dataflow`,
`directory`, `fault_injection`, `file_share`, `function`, `migration`, `model`, `query`,
`replication`, `security_group`, `stream`, `virtual_machine`, `vm_image`, `volume`.

Data sources return `spec` and `status` as JSON strings rather than mirroring every field:

```hcl
data "openinfra_virtual_machine" "dc" { name = "windowsdc" }

output "vm_os" {
  value = jsondecode(data.openinfra_virtual_machine.dc.spec).os
}
```

That's deliberate. Hand-mirroring fifteen evolving CRD schemas would guarantee silent drift —
a field added in open-infra would simply be unreadable here, with no error. JSON stays correct
as the platform changes. Typed resources exist for the kinds you *author*; data sources are for
reading what's already there.

## Keeping in sync with open-infra

> ⚠️ **This repository hand-mirrors open-infra's CRD schemas, and nothing enforces it.**

When you change the platform's API surface, update this provider in the same change:

- **New `kind:`** → add it to `crdKinds` in `internal/provider/provider.go` (gets a data source
  automatically), and add a typed resource if it should be authored from HCL.
- **New/renamed `spec` field** on a typed resource → update its schema *and* its `manifest()`
  mapping, or the field cannot be expressed in HCL.
- **Renamed connection Secret** → update `connectionSecretName()` in `resource_database.go`,
  which duplicates the composition's per-engine naming. `TestConnectionSecretName` pins the
  current values so the coupling is at least visible.

## Development

```bash
go build ./...          # compile
go test ./...           # unit tests (no cluster needed)
go generate ./...       # regenerate docs/ with tfplugindocs
gofmt -l .              # must be empty

# acceptance tests run against a REAL cluster and create/destroy resources
TF_ACC=1 KUBECONFIG=/etc/rancher/k3s/k3s.yaml go test ./internal/provider/ -v
```

## Roadmap

- [x] Provider skeleton (terraform-plugin-framework), Kubernetes client, provider config
- [x] Resources for `application`, `database`, `virtual_machine` (CRUD + import)
- [x] Data sources for every CRD
- [x] Docs generation (`tfplugindocs`)
- [x] GoReleaser + signed release workflow
- [ ] Typed resources for the remaining kinds (`function`, `volume`, `file_share`,
      `security_group`, …) — data sources cover reading them today
- [ ] Acceptance tests exercising a real create/destroy cycle (scaffolding is in place;
      no cluster-backed cases written yet)
- [ ] **Terraform Registry publication** — requires making this repository **public** and
      adding the `GPG_PRIVATE_KEY` / `PASSPHRASE` secrets

## License

Apache-2.0 — see [LICENSE](LICENSE).
