package provider

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// Acceptance tests talk to a REAL cluster. They only run when TF_ACC is set
// (the standard Terraform convention), so `go test ./...` in CI stays hermetic:
//
//	TF_ACC=1 KUBECONFIG=/etc/rancher/k3s/k3s.yaml go test ./internal/provider/ -v
//
// Each test must clean up what it creates — these run against a live open-infra
// install, not a sandbox.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"openinfra": providerserver.NewProtocol6WithError(New("test")()),
}

// testAccPreCheck fails fast with a useful message rather than letting the
// provider error deep inside a plan.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("KUBECONFIG") == "" {
		if _, err := os.Stat(os.Getenv("HOME") + "/.kube/config"); err != nil {
			t.Fatal("acceptance tests need a cluster: set KUBECONFIG (or provide ~/.kube/config)")
		}
	}
}

// TestProviderSchema is a unit test — it runs without a cluster and catches the
// most common self-inflicted break: a schema that doesn't compile or a resource
// registered twice.
func TestProviderSchema(t *testing.T) {
	p := New("test")()
	if p == nil {
		t.Fatal("New returned nil provider")
	}

	resources := p.Resources(t.Context())
	if len(resources) == 0 {
		t.Fatal("provider registers no resources")
	}
	dataSources := p.DataSources(t.Context())
	if len(dataSources) != len(crdKinds) {
		t.Errorf("data sources = %d, want one per CRD (%d)", len(dataSources), len(crdKinds))
	}
}

// TestCRDKindsUnique guards the hand-maintained crdKinds table: a duplicated
// type name would silently shadow a data source at registration time.
func TestCRDKindsUnique(t *testing.T) {
	seenType := map[string]bool{}
	seenPlural := map[string]bool{}
	for _, k := range crdKinds {
		if seenType[k.typeName] {
			t.Errorf("duplicate terraform type name %q", k.typeName)
		}
		if seenPlural[k.plural] {
			t.Errorf("duplicate CRD plural %q", k.plural)
		}
		if k.typeName == "" || k.plural == "" || k.kind == "" {
			t.Errorf("incomplete entry: %+v", k)
		}
		seenType[k.typeName] = true
		seenPlural[k.plural] = true
	}
}

// TestConnectionSecretName pins the per-engine naming this provider duplicates
// from open-infra's composition. If the composition renames a connection Secret,
// this test still passes but the provider is WRONG — see the cross-repo note in
// the README. It exists to make the coupling visible, not to prove correctness.
func TestConnectionSecretName(t *testing.T) {
	cases := map[string]string{
		"postgres":  "orders-db-app",
		"mysql":     "orders-mysql-app",
		"mongo":     "orders-mongo-app",
		"babelfish": "orders-babelfish",
		"unknown":   "",
	}
	for engine, want := range cases {
		if got := connectionSecretName(engine, "orders"); got != want {
			t.Errorf("connectionSecretName(%q) = %q, want %q", engine, got, want)
		}
	}
}
