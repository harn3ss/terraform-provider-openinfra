package client

import apierrors "k8s.io/apimachinery/pkg/api/errors"

// IsNotFound reports whether err is a Kubernetes 404. Terraform resources use
// this to remove an object from state when it has been deleted out-of-band,
// instead of failing the plan.
func IsNotFound(err error) bool {
	return apierrors.IsNotFound(err)
}
