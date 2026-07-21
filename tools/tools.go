//go:build tools

// Package tools pins developer tooling so `go generate` uses a known version
// rather than whatever happens to be on PATH.
package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)
