package kubeconfig_test

import (
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/kubeconfig"
)

var _ = Describe("kubeconfig secret", func() {
	objKey := client.ObjectKey{
		Namespace: "ns",
		Name:      "name",
	}

	cfg := &api.Config{
		Preferences: api.Preferences{Extensions: map[string]runtime.Object{}},
		Clusters: map[string]*api.Cluster{
			"orchestration-cluster": {
				Server:                   "host",
				CertificateAuthorityData: []byte("ca"),
				Extensions:               map[string]runtime.Object{}, // need to include initialized but empty maps for equality
			},
		},
		AuthInfos: map[string]*api.AuthInfo{
			"orchestration-cluster": {
				Token:      "token",
				Extensions: map[string]runtime.Object{},
			},
		},
		Contexts: map[string]*api.Context{
			"orchestration-cluster": {
				Cluster:    "orchestration-cluster",
				AuthInfo:   "orchestration-cluster",
				Namespace:  "federationIONamespace",
				Extensions: map[string]runtime.Object{},
			},
		},
		CurrentContext: "orchestration-cluster",
		Extensions:     map[string]runtime.Object{},
	}
	serializedCfg, err := clientcmd.Write(*cfg)
	Expect(err).ToNot(HaveOccurred())

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objKey.Name,
			Namespace: objKey.Namespace,
		},
		Data: map[string][]byte{
			"kubeconfig": serializedCfg,
		},
	}

	It("KubeConfigToSecret should convert a kubeconfig to a Secret", func() {
		actualSecret, err := kubeconfig.KubeConfigToSecret(objKey, cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(actualSecret).To(Equal(secret))
	})

	It("SecretToKubeConfig should convert a Secret to a kubeconfig", func() {
		actualClientCfg, err := kubeconfig.SecretToKubeConfig(secret)
		Expect(err).ToNot(HaveOccurred())

		actualCfg, err := actualClientCfg.RawConfig()
		Expect(err).ToNot(HaveOccurred())

		Expect(&actualCfg).To(Equal(cfg))
	})

	It("SecretToKubeConfig should error when reading Secret without kubeconfig key", func() {
		secretWithMissingKey := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      objKey.Name,
				Namespace: objKey.Namespace,
			},
			Data: map[string][]byte{},
		}

		_, err := kubeconfig.SecretToKubeConfig(secretWithMissingKey)
		Expect(errors.Is(
			err,
			kubeconfig.KeyNotFound{ObjectKey: client.ObjectKeyFromObject(secretWithMissingKey)},
		)).To(BeTrue())
	})

	It("SecretToKubeConfig should error when reading Secret with invalid kubeconfig data", func() {
		secretWithInvalidData := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      objKey.Name,
				Namespace: objKey.Namespace,
			},
			Data: map[string][]byte{
				"kubeconfig": []byte("hello world?"),
			},
		}

		_, err := kubeconfig.SecretToKubeConfig(secretWithInvalidData)
		Expect(errors.Is(err, kubeconfig.SerializedKubeconfigInvalid{
			ObjectKey: client.ObjectKeyFromObject(secretWithInvalidData),
			Err:       err,
		}))
	})
})

func TestKubeConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "kubeconfig")
}
