// Package client is a thin dynamic Kubernetes client for open-infra's CRDs.
//
// Every open-infra resource (Application, VirtualMachine, …) is a namespaced
// custom resource under the openinfra.dev API group, so one dynamic client can
// serve every Terraform resource type — we only vary the GroupVersionResource.
package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Group and version shared by all open-infra custom resources.
const (
	Group   = "openinfra.dev"
	Version = "v1"
)

// Client wraps the dynamic client with open-infra defaults.
type Client struct {
	dyn dynamic.Interface
}

// Config describes how to reach the cluster. Empty fields fall back to the
// ambient environment: in-cluster config first, then $KUBECONFIG, then
// ~/.kube/config — the same precedence kubectl uses.
type Config struct {
	Kubeconfig string
	Context    string
}

// New builds a client from the given config.
func New(cfg Config) (*Client, error) {
	rc, err := restConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	return &Client{dyn: dyn}, nil
}

func restConfig(cfg Config) (*rest.Config, error) {
	// Explicit kubeconfig (or context) wins.
	if cfg.Kubeconfig != "" || cfg.Context != "" {
		path := cfg.Kubeconfig
		if path == "" {
			path = defaultKubeconfigPath()
		}
		rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: expandHome(path)}
		overrides := &clientcmd.ConfigOverrides{}
		if cfg.Context != "" {
			overrides.CurrentContext = cfg.Context
		}
		rc, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("load kubeconfig %q: %w", path, err)
		}
		return rc, nil
	}

	// In-cluster (e.g. Terraform running as a Job) …
	if rc, err := rest.InClusterConfig(); err == nil {
		return rc, nil
	}
	// … otherwise the ambient kubeconfig.
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rc, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("no in-cluster config and no usable kubeconfig: %w", err)
	}
	return rc, nil
}

func defaultKubeconfigPath() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}

func expandHome(p string) string {
	if len(p) > 1 && p[0] == '~' {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// gvr builds the GroupVersionResource for an open-infra resource (plural form,
// e.g. "applications", "virtualmachines").
func gvr(resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: Group, Version: Version, Resource: resource}
}

// Create applies a new custom resource and returns the server's view of it.
func (c *Client) Create(ctx context.Context, resource, namespace string, obj map[string]any) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{Object: obj}
	out, err := c.dyn.Resource(gvr(resource)).Namespace(namespace).
		Create(ctx, u, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create %s/%s: %w", resource, u.GetName(), err)
	}
	return out, nil
}

// Get reads a resource. Callers should treat IsNotFound as "removed upstream"
// and drop it from state rather than erroring.
func (c *Client) Get(ctx context.Context, resource, namespace, name string) (*unstructured.Unstructured, error) {
	return c.dyn.Resource(gvr(resource)).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// Update replaces the resource's spec, preserving the current resourceVersion.
func (c *Client) Update(ctx context.Context, resource, namespace string, obj map[string]any) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{Object: obj}
	cur, err := c.Get(ctx, resource, namespace, u.GetName())
	if err != nil {
		return nil, err
	}
	u.SetResourceVersion(cur.GetResourceVersion())
	out, err := c.dyn.Resource(gvr(resource)).Namespace(namespace).
		Update(ctx, u, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("update %s/%s: %w", resource, u.GetName(), err)
	}
	return out, nil
}

// Delete removes the resource. Missing is not an error — the desired end state
// (absent) already holds.
func (c *Client) Delete(ctx context.Context, resource, namespace, name string) error {
	err := c.dyn.Resource(gvr(resource)).Namespace(namespace).
		Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !IsNotFound(err) {
		return fmt.Errorf("delete %s/%s: %w", resource, name, err)
	}
	return nil
}
