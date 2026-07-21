# Releasing

Releases are cut by tagging. [`.github/workflows/release.yml`](.github/workflows/release.yml)
imports the signing key and runs [GoReleaser](https://goreleaser.com), which builds one
archive per OS/arch, a `_SHA256SUMS` file, and a GPG signature of that file — the three
things the Terraform Registry needs to accept a version.

```bash
git tag v0.2.0
git push origin v0.2.0
gh run watch
```

The Registry picks the tag up automatically. Versions are [semver](https://semver.org),
without exception: Terraform's `version = "~> 0.2"` constraints depend on it, so a
breaking schema change is a major bump even when the diff looks small.

## What a release must contain

Check the release page after the workflow finishes:

| Artifact | Why it matters |
|---|---|
| One `.zip` per OS/arch | The Registry serves these directly |
| `..._SHA256SUMS` | Integrity |
| `..._SHA256SUMS.sig` | Signed with the key registered on the Registry |
| `..._manifest.json` | **Declares plugin protocol 6.0** |

That last one is the easiest to lose and the worst to lose. It comes from
[`terraform-registry-manifest.json`](terraform-registry-manifest.json) via `release.extra_files`
in [`.goreleaser.yml`](.goreleaser.yml). Without it the Registry assumes protocol **5.0**,
while terraform-plugin-framework serves **6.0** — so `terraform init` fails for every user
with a protocol mismatch, and nothing in the build output hints at why.

## Testing a release without publishing one

```bash
goreleaser check                                       # config is valid
goreleaser release --snapshot --clean --skip=sign,publish
```

`dist/` then holds exactly what a real release would, minus the signature. Worth doing
after any change to the build matrix. Note that `release.extra_files` does **not** run in
snapshot mode, so this cannot tell you whether the manifest will be published — only a
real tag can.

## Signing keys

The workflow needs two repository secrets:

| Secret | Value |
|---|---|
| `GPG_PRIVATE_KEY` | Armored private key, including the `BEGIN`/`END` lines |
| `PASSPHRASE` | That key's passphrase |

The **public** half is uploaded to the Terraform Registry, which uses it to verify the
signature on `_SHA256SUMS`.

### Rotating the key

The Registry treats a new key as a new signing identity, so rotation is not free:

1. Generate the new key: `gpg --full-generate-key` (RSA 4096).
2. Upload its public half to the Registry **before** publishing anything signed with it —
   existing releases keep verifying against the old key, which stays valid.
3. Replace both repository secrets.
4. Cut a release and confirm `terraform init` verifies it from a clean directory.

Keep the private key somewhere you will still have in a year. Losing it doesn't break
published versions, but every future release then comes from a different identity.
