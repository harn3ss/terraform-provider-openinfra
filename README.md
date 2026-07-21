# terraform-provider-openinfra

A Terraform provider for [open-infra](https://github.com/harn3ss/open-infra) — declare
open-infra resources (applications, databases, virtual machines, functions, volumes, …) in
HCL instead of `kind:` manifests.

[![Terraform Registry](https://img.shields.io/badge/terraform-harn3ss%2Fopeninfra-7B42BC?logo=terraform)](https://registry.terraform.io/providers/harn3ss/openinfra/latest)

```hcl
terraform {
  required_providers {
    openinfra = {
      source  = "harn3ss/openinfra"
      version = "~> 0.1"
    }
  }
}
```

Every open-infra kind is addressable as a typed resource and a data source. The resource
lifecycle is verified against a live cluster on every change.

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

## Example

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

**Resources** — every kind, with full CRUD and `terraform import` via `namespace/name`:

| Resource | Maps to |
|---|---|
| `openinfra_application` | `kind: Application` (container workload) |
| `openinfra_database` | `kind: Application` with `spec.database` (postgres/mysql/mongo/babelfish) |
| `openinfra_virtual_machine` | `kind: VirtualMachine` |
| `openinfra_function` | `kind: Function` |
| `openinfra_volume` | `kind: Volume` |
| `openinfra_file_share` | `kind: FileShare` |
| `openinfra_security_group` | `kind: SecurityGroup` |
| `openinfra_model` | `kind: Model` |
| `openinfra_query` | `kind: Query` |
| `openinfra_migration` | `kind: Migration` |
| `openinfra_replication` | `kind: Replication` |
| `openinfra_dataflow` | `kind: DataFlow` |
| `openinfra_stream` | `kind: Stream` |
| `openinfra_directory` | `kind: Directory` |
| `openinfra_fault_injection` | `kind: FaultInjection` |
| `openinfra_vm_image` | `kind: VmImage` |

Sixteen resources across fifteen kinds — `application` and `database` are two front doors
onto `kind: Application`, because a managed database is a data-only Application and giving
it its own resource reads far better in HCL.

See [`examples/full-stack.tf`](examples/full-stack.tf) for most of these in one file.

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

> ⚠️ **This repository mirrors open-infra's CRD schemas by hand, and nothing enforces it.**
> A field missing here cannot be expressed in HCL — silently absent, not an error.

Most changes are one line in [`internal/provider/kinds.go`](internal/provider/kinds.go).
[CONTRIBUTING.md](CONTRIBUTING.md#adding-or-changing-a-resource) has the mapping table and
the three things that are easy to get wrong.

## Development

```bash
go build ./...       # compile
go test ./...        # unit tests — no cluster needed
go generate ./...    # regenerate docs/
gofmt -l .           # must print nothing
```

Dev overrides, acceptance tests against a real cluster, and how to add a kind:
[CONTRIBUTING.md](CONTRIBUTING.md). Cutting a release: [RELEASING.md](RELEASING.md).

## How the resources are built

Three resources — `application`, `database`, `virtual_machine` — are hand-written,
because they carry behaviour a schema cannot express: per-engine connection-secret
naming, start/stop semantics.

The other twelve are **generated from a table**. Every open-infra CRD has the same
lifecycle — create a namespaced custom resource, read its status, replace its spec,
delete it — so writing a resource per kind meant ~250 lines of identical CRUD each, and
twelve separate places to drift from the XRD that actually defines the schema. Instead,
[`internal/provider/kinds.go`](internal/provider/kinds.go) declares each kind as data
and [`resource_generic.go`](internal/provider/resource_generic.go) turns it into a
working resource. Adding a field is a line in a table.

Two behaviours worth knowing about, both deliberate:

**Server-side defaults.** An attribute whose XRD supplies a default is declared
`Optional` *and* `Computed`. Without the second half, the server filling in the default
would look like drift and every plan would show a diff.

**Refresh is conservative.** `Read` pulls back only scalar attributes that your config
already sets. It does not adopt values for fields you never wrote, and it skips nested
blocks entirely — a `source` block comes back with `port: 5432` and `schemas:
["public"]` filled in even when you gave only the required keys, and adopting those
would produce a permanent phantom diff. The honest consequence: **an out-of-band change
to a field you don't manage in HCL, or to anything inside a nested block, is not
detected.** The next apply that touches the resource corrects it, since apply always
sends the full desired spec.

## Status

- [x] Provider skeleton (terraform-plugin-framework), Kubernetes client, provider config
- [x] Resources for `application`, `database`, `virtual_machine` (CRUD + import)
- [x] Data sources for every CRD
- [x] Docs generation (`tfplugindocs`)
- [x] GoReleaser + signed release workflow
- [x] Typed resources for every remaining kind — `function`, `volume`, `file_share`,
      `security_group`, `model`, `query`, `migration`, `replication`, `dataflow`,
      `stream`, `directory`, `fault_injection`, `vm_image`
- [x] Acceptance tests exercising real create / update / import / destroy cycles against
      a live cluster, asserting against the **live CR spec** rather than only against
      Terraform state
- [x] **Published to the Terraform Registry** as
      [`harn3ss/openinfra`](https://registry.terraform.io/providers/harn3ss/openinfra/latest)
      — GPG-signed releases across 13 OS/arch targets, verified by a clean-room
      `terraform init` + `apply` against a real cluster.

### Not covered, on purpose

- **`kind: User` / `kind: Group`** (`iam.openinfra.dev`). Identity is deliberately in a
  separate API group in open-infra, and managing who may log in from the same config
  that manages infrastructure invites an accident with no undo. Use the console or
  `kubectl` for now.
- **Acceptance tests for `fault_injection` and `query`.** Applying a FaultInjection
  breaks running workloads on purpose and a Query launches a job; neither belongs in a
  suite someone might point at a cluster they care about. Their schemas are covered by
  unit tests.

## License

Apache-2.0 — see [LICENSE](LICENSE).
