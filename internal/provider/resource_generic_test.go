package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	openinfra "github.com/harn3ss/terraform-provider-openinfra/internal/client"
)

// Cluster-backed acceptance tests for the table-driven resources.
//
// These create real objects in a real open-infra install and are skipped unless TF_ACC
// is set:
//
//	TF_ACC=1 KUBECONFIG=/etc/rancher/k3s/k3s.yaml go test ./internal/provider/ -run TestAcc -v
//
// The kinds chosen here are the ones with no side effects beyond their own existence.
// FaultInjection is deliberately NOT covered: applying one deliberately breaks running
// workloads, which is not something a test suite should do to a live cluster by
// default. Query is excluded for the same reason — it launches a job.
//
// Between them, Volume, SecurityGroup and Function exercise every part of the generic
// machinery: scalars with and without defaults, nested objects, lists of nested objects,
// nested-inside-nested objects, string lists and integer lists.

const accNamespace = "default"

// testAccClient builds a client the checks can use to inspect the cluster directly,
// so an assertion proves the object really exists rather than only that Terraform
// believes it does.
func testAccClient(t *testing.T) *openinfra.Client {
	t.Helper()
	c, err := openinfra.New(openinfra.Config{Kubeconfig: os.Getenv("KUBECONFIG")})
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	return c
}

// checkAbsent is a CheckDestroy: Terraform reporting a successful destroy is not the
// same as the object being gone.
//
// It POLLS rather than checking once. A Crossplane claim carries a finalizer, so the
// DELETE call returns while the object is still present with a deletionTimestamp, and
// the composed resources are torn down afterwards. Asserting immediately fails on a
// race, not on a bug — which is a worse test than none, because it trains you to
// ignore the failure.
func checkAbsent(t *testing.T, plural, name string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		c := testAccClient(t)
		deadline := time.Now().Add(2 * time.Minute)
		for {
			_, err := c.Get(context.Background(), plural, accNamespace, name)
			if openinfra.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("checking %s/%s: %w", plural, name, err)
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s/%s still exists two minutes after destroy — "+
					"a finalizer is probably stuck", plural, name)
			}
			time.Sleep(2 * time.Second)
		}
	}
}

// checkSpec asserts a value at a path under the LIVE object's spec. This is the check
// that matters most: it proves the snake_case → camelCase translation produced a field
// the API server actually kept, rather than one it silently pruned.
func checkSpec(t *testing.T, plural, name string, want any, path ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		c := testAccClient(t)
		u, err := c.Get(context.Background(), plural, accNamespace, name)
		if err != nil {
			return err
		}
		got := walk(u.Object, append([]string{"spec"}, path...)...)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			return fmt.Errorf("spec.%v = %#v, want %#v", path, got, want)
		}
		return nil
	}
}

// walk is nested() plus array indexing, since the live spec has lists in it and the
// checks need to reach inside them.
func walk(obj map[string]any, path ...string) any {
	cur := any(obj)
	for _, p := range path {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[p]
			if !ok {
				return nil
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(c) {
				return nil
			}
			cur = c[i]
		default:
			return nil
		}
	}
	return cur
}

func TestAccVolume(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test; set TF_ACC=1 and point KUBECONFIG at a cluster")
	}
	const name = "tfacc-volume"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkAbsent(t, "volumes", name),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "openinfra_volume" "test" {
  name = %q
  size = "1Gi"
}`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("openinfra_volume.test", "size", "1Gi"),
					resource.TestCheckResourceAttr("openinfra_volume.test", "namespace", "default"),
					resource.TestCheckResourceAttr("openinfra_volume.test", "id", "default/"+name),
					// migratable has an XRD default, so it must be populated rather
					// than left unknown — that pairing is the easiest thing to get wrong.
					resource.TestCheckResourceAttr("openinfra_volume.test", "migratable", "false"),
					checkSpec(t, "volumes", name, "1Gi", "size"),
				),
			},
			{
				// Expansion is an in-place update, not a replacement.
				Config: fmt.Sprintf(`
resource "openinfra_volume" "test" {
  name = %q
  size = "2Gi"
}`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("openinfra_volume.test", "size", "2Gi"),
					checkSpec(t, "volumes", name, "2Gi", "size"),
				),
			},
			{
				ResourceName:      "openinfra_volume.test",
				ImportState:       true,
				ImportStateId:     "default/" + name,
				ImportStateVerify: false, // import seeds identity only; Read fills the rest
			},
		},
	})
}

func TestAccSecurityGroup(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test; set TF_ACC=1 and point KUBECONFIG at a cluster")
	}
	const name = "tfacc-sg"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkAbsent(t, "securitygroups", name),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "openinfra_security_group" "test" {
  name = %q

  ingress = [{
    protocol    = "TCP"
    description = "postgres from the app tier"
    ports       = [5432]
    from = [
      { cidr = "10.0.0.0/8" },
      { namespace = "default" },
    ]
  }]

  egress = [{
    protocol = "TCP"
    ports    = [443]
    to       = [{ cidr = "0.0.0.0/0" }]
  }]
}`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("openinfra_security_group.test", "ingress.0.ports.0", "5432"),
					resource.TestCheckResourceAttr("openinfra_security_group.test", "ingress.0.from.1.namespace", "default"),
					// The live object is what proves the nested lists survived the
					// round trip into the CRD's own field names.
					checkSpec(t, "securitygroups", name, "5432", "ingress", "0", "ports", "0"),
					checkSpec(t, "securitygroups", name, "10.0.0.0/8", "ingress", "0", "from", "0", "cidr"),
					checkSpec(t, "securitygroups", name, "0.0.0.0/0", "egress", "0", "to", "0", "cidr"),
				),
			},
		},
	})
}

func TestAccFunction(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test; set TF_ACC=1 and point KUBECONFIG at a cluster")
	}
	const name = "tfacc-function"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkAbsent(t, "functions", name),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "openinfra_function" "test" {
  name  = %q
  image = "ghcr.io/knative/helloworld-go:latest"

  scaling = {
    min = 0
    max = 3
  }

  env = [
    { name = "GREETING", value = "hello" },
  ]
}`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					// XRD defaults must land in state, not stay unknown.
					resource.TestCheckResourceAttr("openinfra_function.test", "port", "8080"),
					resource.TestCheckResourceAttr("openinfra_function.test", "gpu", "0"),
					resource.TestCheckResourceAttr("openinfra_function.test", "expose", "true"),
					resource.TestCheckResourceAttr("openinfra_function.test", "scaling.max", "3"),
					checkSpec(t, "functions", name, "3", "scaling", "max"),
					checkSpec(t, "functions", name, "GREETING", "env", "0", "name"),
				),
			},
			{
				// Changing an env var is an in-place update of a list of objects.
				Config: fmt.Sprintf(`
resource "openinfra_function" "test" {
  name  = %q
  image = "ghcr.io/knative/helloworld-go:latest"

  scaling = {
    min = 0
    max = 3
  }

  env = [
    { name = "GREETING", value = "goodbye" },
  ]
}`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkSpec(t, "functions", name, "goodbye", "env", "0", "value"),
				),
			},
		},
	})
}
