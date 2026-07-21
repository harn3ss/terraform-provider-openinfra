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

## Roadmap

1. Provider skeleton (terraform-plugin-framework), Kubernetes client, provider config.
2. Resources mapped to the open-infra CRDs, starting with `application`, `database`,
   `virtual_machine`.
3. Data sources, import support, acceptance tests against a live cluster.
4. Docs generation (`tfplugindocs`), GoReleaser + signed releases, Terraform Registry
   publication (requires this repository to be **public**).

## License

Apache-2.0 — see [LICENSE](LICENSE).
