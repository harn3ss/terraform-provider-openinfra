package main

import (
	"os"
	"testing"

	"sigs.k8s.io/yaml"
)

// The Terraform Registry rejects a release in which any published asset is missing from
// the checksum file:
//
//	Could not find all required assets for this release yet (0.1.0)
//	missing SHA256 checksum for ["terraform-provider-openinfra_0.1.0_manifest.json"]
//
// terraform-registry-manifest.json has to appear in BOTH `release.extra_files` (which
// uploads it) and `checksum.extra_files` (which hashes it), under the SAME name template
// so the checksum entry matches the uploaded asset. Listing it in only one is an easy
// mistake, produces a release that looks complete on the GitHub side, and fails at the
// Registry long after the tag is pushed — where re-tagging is the only fix.
//
// This runs in `go test ./...`, so the regression is caught before anything is tagged.
func TestGoreleaserPublishesAndChecksumsTheRegistryManifest(t *testing.T) {
	const (
		manifestFile = "terraform-registry-manifest.json"
		wantName     = "{{ .ProjectName }}_{{ .Version }}_manifest.json"
	)

	if _, err := os.Stat(manifestFile); err != nil {
		t.Fatalf("%s is missing — without it the Registry assumes plugin protocol 5.0 "+
			"and every `terraform init` fails on a protocol mismatch: %v", manifestFile, err)
	}

	raw, err := os.ReadFile(".goreleaser.yml")
	if err != nil {
		t.Fatal(err)
	}

	type extraFile struct {
		Glob         string `json:"glob"`
		NameTemplate string `json:"name_template"`
	}
	var cfg struct {
		Checksum struct {
			ExtraFiles []extraFile `json:"extra_files"`
		} `json:"checksum"`
		Release struct {
			ExtraFiles []extraFile `json:"extra_files"`
		} `json:"release"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yml: %v", err)
	}

	for _, section := range []struct {
		name  string
		files []extraFile
		why   string
	}{
		{"checksum.extra_files", cfg.Checksum.ExtraFiles,
			"the Registry rejects the release: missing SHA256 checksum for the manifest"},
		{"release.extra_files", cfg.Release.ExtraFiles,
			"the manifest is never uploaded, so the Registry assumes protocol 5.0"},
	} {
		var found *extraFile
		for i := range section.files {
			if section.files[i].Glob == manifestFile {
				found = &section.files[i]
				break
			}
		}
		if found == nil {
			t.Errorf("%s does not include %q — %s", section.name, manifestFile, section.why)
			continue
		}
		// The names must agree, or the checksum entry refers to a file that was never
		// published under that name — which the Registry reports the same way as absent.
		if found.NameTemplate != wantName {
			t.Errorf("%s uses name_template %q, want %q (the checksum entry and the "+
				"uploaded asset must have identical names)",
				section.name, found.NameTemplate, wantName)
		}
	}
}
