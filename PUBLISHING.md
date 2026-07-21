# Publishing to the Terraform Registry

Everything that can be done ahead of time is done. What remains needs decisions and
credentials that aren't mine to make: **making the repository public** and **generating a
signing key**.

Read this end to end before starting. Step 2 is irreversible.

---

## What has already been verified

- `goreleaser check` passes, and a full `goreleaser release --snapshot` builds all
  **13 OS/arch archives** plus the `_SHA256SUMS` file.
- The binary inside each archive is named `terraform-provider-openinfra_v<version>`,
  which is the name the Registry requires.
- The built binary starts and correctly refuses direct execution
  (*"This binary is a plugin…"*), so the plugin handshake is intact.
- `terraform-registry-manifest.json` declares **protocol 6.0**. This matters: the
  Registry assumes 5.0 when the manifest is absent, and terraform-plugin-framework
  serves 6.0, so without it every `terraform init` would fail on a protocol mismatch.
- The repository history contains no credentials, no email addresses in file content,
  no private hostnames, and no GitHub owner other than `harn3ss` and upstream vendors.
- Acceptance tests drive a real `terraform` CLI through create / update / import /
  destroy against a live open-infra cluster.

## What is NOT yet verified

`release.extra_files` only runs during a real publish, so the manifest's presence in the
release cannot be proven by a snapshot build. **After the first tag, check the GitHub
release page lists `terraform-provider-openinfra_<version>_manifest.json`.** If it is
missing, the Registry will assume protocol 5.0 and `terraform init` will fail for
everyone — fix the glob in `.goreleaser.yml` and re-tag.

---

## Step 1 — Generate a signing key

The Registry verifies the `_SHA256SUMS` signature against a public key you upload. Use a
key dedicated to this; do not reuse a personal one.

```bash
gpg --full-generate-key      # RSA 4096, no expiry is fine for a signing key
gpg --list-secret-keys --keyid-format=long
```

Take the long key id from the `sec` line, then:

```bash
# Private half → GitHub secret (armored, INCLUDING the BEGIN/END lines)
gpg --armor --export-secret-keys <KEY_ID>

# Public half → uploaded to the Terraform Registry in step 4
gpg --armor --export <KEY_ID>
```

Add two **repository secrets** under Settings → Secrets and variables → Actions:

| Secret            | Value                                            |
|-------------------|--------------------------------------------------|
| `GPG_PRIVATE_KEY` | the armored private key, including BEGIN/END      |
| `PASSPHRASE`      | the passphrase you set on it                      |

Keep the private key somewhere you will still have it in a year. Losing it means every
future release has to be signed by a new key, which the Registry treats as a new
identity.

## Step 2 — Make the repository public (irreversible)

Settings → General → Danger Zone → Change visibility → Public.

The Registry only ingests public repositories. Once public, the full history is
permanently visible and may be forked or cached by third parties — the audit above found
nothing sensitive, but this is the point of no return.

## Step 3 — Tag a release

```bash
git tag v0.1.0
git push origin v0.1.0
```

That triggers `.github/workflows/release.yml`, which imports the GPG key and runs
GoReleaser. Watch it:

```bash
gh run watch --repo harn3ss/terraform-provider-openinfra
```

Then confirm the release page lists, at minimum:

- 13 `.zip` archives
- `terraform-provider-openinfra_0.1.0_SHA256SUMS`
- `terraform-provider-openinfra_0.1.0_SHA256SUMS.sig`
- `terraform-provider-openinfra_0.1.0_manifest.json`  ← the one to check for

## Step 4 — Publish on the Registry

1. Sign in at <https://registry.terraform.io/> with the GitHub account that owns the repo.
2. Publish → Provider → pick `terraform-provider-openinfra`.
3. Paste the **public** GPG key from step 1 when asked.
4. The Registry ingests the existing tag; subsequent tags are picked up automatically.

It will appear as `harn3ss/openinfra`, matching the `Address` in [`main.go`](main.go).

## Step 5 — Verify as a consumer would

From a clean directory, with no dev override in effect:

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

```bash
terraform init      # must download and verify the signature
terraform plan
```

A signature error here means the public key uploaded in step 4 doesn't match the private
key in `GPG_PRIVATE_KEY`.

---

## Using the provider before it is published

You don't have to wait. A dev override makes Terraform use a locally built binary and
skip the Registry entirely.

```bash
go build -o ~/go/bin/terraform-provider-openinfra .
```

Then in `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "harn3ss/openinfra" = "/home/<you>/go/bin"
  }
  direct {}
}
```

With an override in place **`terraform init` is skipped** — run `terraform plan` directly.
Terraform prints a warning on every command to remind you the override is active; that
warning is the intended behaviour, not a problem.
