package kubeconfig

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// KeyNotFound is returned if the Secret does not contain the kubeConfigDataKey key.
type KeyNotFound struct {
	ObjectKey client.ObjectKey
}

func (r KeyNotFound) Error() string {
	return fmt.Sprintf("missing key %q in data for Secret %q", kubeConfigDataKey, r.ObjectKey)
}

type SerializedKubeconfigInvalid struct {
	ObjectKey client.ObjectKey
	Err       error
}

func (r SerializedKubeconfigInvalid) Error() string {
	return fmt.Errorf("building client config from serialized config: %w", r.Err).Error()
}
