package manifest

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func Test_DecodeObjects(t *testing.T) {
	testYaml := `
---
# Source: cilium/templates/cilium-agent/serviceaccount.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: "cilium"
  namespace: kube-system
---
# Source: cilium/templates/cilium-operator/serviceaccount.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: "cilium-operator"
  namespace: kube-system
---
---
# Source: cilium/templates/cilium-secrets-namespace.yaml
# Only create the namespace if it's different from Ingress secret namespace or Ingress is not enabled.

# Only create the namespace if it's different from Ingress and Gateway API secret namespaces (if enabled).
---
`
	r := strings.NewReader(testYaml)

	actualObjs, err := DecodeObjects(r)
	if err != nil {
		t.Errorf("unexpected error: %s", err)
	}

	var expectedObjs []*unstructured.Unstructured
	for _, o := range []client.Object{
		&v1.ServiceAccount{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "ServiceAccount",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cilium",
				Namespace: "kube-system",
			},
		},
		&v1.ServiceAccount{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "ServiceAccount",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cilium-operator",
				Namespace: "kube-system",
			},
		},
	} {
		u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o)
		assert.NoError(t, err)

		// NOTE: deletes `"creationTimestamp": nil`
		delete(u["metadata"].(map[string]interface{}), "creationTimestamp")
		expectedObjs = append(expectedObjs, &unstructured.Unstructured{Object: u})
	}

	if diff := cmp.Diff(actualObjs, expectedObjs); diff != "" {
		t.Errorf("result differs from expected: (-got +want):\n%s", diff)
	}
}
