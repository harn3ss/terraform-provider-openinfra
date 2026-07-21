# Contributing

```bash
go build ./...       # compile
go test ./...        # unit tests — no cluster needed
go generate ./...    # regenerate docs/ from the provider schema
gofmt -l .           # must print nothing
```

CI runs all four, plus a check that `docs/` is not stale. The Registry serves `docs/`
verbatim, so a schema change without a matching `go generate` ships documentation
describing a provider that no longer exists.

## Running the provider against a real cluster

Terraform's `dev_overrides` makes it load a locally built binary instead of the Registry:

```bash
go build -o ~/go/bin/terraform-provider-openinfra .
```

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "harn3ss/openinfra" = "/home/<you>/go/bin"
  }
  direct {}
}
```

With an override active, **`terraform init` is skipped** — run `terraform plan` directly.
Terraform prints a warning on every command to remind you the override is on; that's
intended, not a problem.

## Acceptance tests

These create and destroy **real resources in a real open-infra install**. They are skipped
unless `TF_ACC` is set:

```bash
TF_ACC=1 KUBECONFIG=/etc/rancher/k3s/k3s.yaml go test ./internal/provider/ -run TestAcc -v
```

They assert against the **live custom resource**, not just Terraform state — that's what
proves an HCL attribute became a field the API server actually kept, rather than one it
silently pruned.

Two kinds are deliberately not covered: applying a `fault_injection` breaks running
workloads on purpose, and a `query` launches a job. Neither belongs in a suite someone
might point at a cluster they care about. Their schemas are covered by unit tests.

Crossplane deletes claims asynchronously, so `CheckDestroy` polls rather than asserting
once — a finalizer means the object is still present the instant `DELETE` returns.

## Adding or changing a resource

> ⚠️ **This repository mirrors open-infra's CRD schemas by hand, and nothing enforces
> it.** A field missing here cannot be expressed in HCL — silently absent, not an error.

Most changes are one line in [`internal/provider/kinds.go`](internal/provider/kinds.go),
which declares each kind as data; [`resource_generic.go`](internal/provider/resource_generic.go)
turns each entry into a full resource.

| Change in open-infra | Change here |
|---|---|
| New `kind:` | An entry in `genericKinds`, **and** one in `crdKinds` ([`provider.go`](internal/provider/provider.go)) for its data source. A test fails if the two names disagree. |
| New or renamed `spec` field | A line in that kind's `Attrs`. |
| Changed XRD default | Set `Default:` on the attr. |
| Field became immutable | Set `Replaces: true`. |
| Renamed connection Secret | Update `connectionSecretName()` in [`resource_database.go`](internal/provider/resource_database.go). |

Three things that are easy to get wrong:

**`Default:` is not documentation.** It makes the attribute `Optional` *and* `Computed`.
Without the second half, the server filling in its own default reads as drift and every
`terraform plan` shows a diff.

**Prefer `Replaces: true` when unsure.** Silently accepting an update the Composition
ignores is the worse failure — the config then lies about what is running.

**camelCasing is deliberately naive.** The XRDs are inconsistent (`nodeIP`, but
`sourceUrl` and `queryId`), so any general initialism rule would be right for one field
and wrong for the next. Fields that don't follow plain camelCasing set `Path` explicitly,
next to the field it applies to. A test guards this.

`application`, `database` and `virtual_machine` stay hand-written: they carry behaviour a
schema table can't express, like per-engine connection-secret naming and start/stop
semantics.

### Not in scope

`kind: User` and `kind: Group` (`iam.openinfra.dev`) are deliberately absent. Identity
lives in a separate API group in open-infra, and managing who may log in from the same
config that manages infrastructure invites an accident with no undo.

## A known limitation, before you file it as a bug

`Read` refreshes only scalar attributes your config already sets, and skips nested blocks
entirely. A `source` block comes back from the cluster with `port: 5432` and
`schemas: ["public"]` populated even when you supplied only the required keys, so adopting
the server's view would produce a permanent phantom diff.

The cost is real and is not a bug: **an out-of-band change to a field you don't manage in
HCL, or to anything inside a nested block, is not detected.** The next apply that touches
the resource corrects it, because apply always sends the full desired spec.

## Releasing

See [RELEASING.md](RELEASING.md).
