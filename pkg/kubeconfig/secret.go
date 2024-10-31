package kubeconfig

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const kubeConfigDataKey = "kubeconfig"

// KubeConfigToSecret returns a Secret with the given object key
// containing a kubeconfig serialized from the given *api.Config
func KubeConfigToSecret(
	objKey client.ObjectKey,
	cfg *api.Config,
) (*corev1.Secret, error) {
	out, err := clientcmd.Write(*cfg)
	if err != nil {
		return nil, fmt.Errorf("serializing kubeconfig to yaml: %w", err)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objKey.Name,
			Namespace: objKey.Namespace,
		},
		Data: map[string][]byte{
			kubeConfigDataKey: out,
		},
	}, nil
}

// SecretToKubeConfig returns clientcmd.ClientConfig from a Secret containing a serialized kubeconfig
func SecretToKubeConfig(
	secret *corev1.Secret,
) (clientcmd.ClientConfig, error) {
	return SecretToKubeConfigByKey(secret, kubeConfigDataKey)
}

// SecretToKubeConfigByKey returns clientcmd.ClientConfig from a Secret containing a serialized kubeconfig as the value for the provided key.
func SecretToKubeConfigByKey(
	secret *corev1.Secret,
	key string,
) (clientcmd.ClientConfig, error) {
	data, ok := secret.Data[key]

	secretObjKey := client.ObjectKeyFromObject(secret)
	if !ok {
		return nil, KeyNotFound{ObjectKey: secretObjKey}
	}

	cfg, err := clientcmd.NewClientConfigFromBytes(data)
	if err != nil {
		return nil, SerializedKubeconfigInvalid{
			ObjectKey: secretObjKey,
			Err:       err,
		}
	}

	return cfg, nil
}
