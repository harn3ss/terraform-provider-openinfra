# terraform-provider-openinfra

A Terraform provider for [open-infra](https://github.com/harn3ss/open-infra) — declare
open-infra resources (applications, databases, virtual machines, functions, volumes, …) in
HCL instead of `kind:` manifests.

> **Status: complete but unpublished.** Every open-infra kind is addressable as a typed
> resource and a data source, and the resource lifecycle is verified against a live
> cluster. It is not on the Terraform Registry yet — that needs this repository made
> **public** plus release signing keys. See [Roadmap](#roadmap).

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

## Roadmap

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
- [ ] **Terraform Registry publication** — requires making this repository **public** and
      adding the `GPG_PRIVATE_KEY` / `PASSPHRASE` secrets

### Not covered, on purpose

- **`kind: User` / `kind: Group`** (`iam.openinfra.dev`). Identity is deliberately in a
  separate API group in open-infra, and managing who may log in from the same config
  that manages infrastructure invites an accident with no undo. Use the console or
  `kubectl` for now.
- **Acceptance tests for `fault_injection` and `query`.** Applying a FaultInjection
  breaks running workloads on purpose and a Query launches a job; neither belongs in a
  suite someone might run against a cluster they care about. The schemas are covered by
  unit tests.

## License

Apache-2.0 — see [LICENSE](LICENSE).
