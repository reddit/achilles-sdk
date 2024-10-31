package test

import (
	"context"
	"time"

	"github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/manifest"
)

// AssertManifestExistence asserts the existence of all objects in a YAML manifest on the
// kube-apiserver referenced by the given client. Does not check object data.
func AssertManifestExistence(
	ctx context.Context,
	c client.Client,
	manifestYAML string,
	timeout time.Duration,
	pollingInterval time.Duration,
) {
	objs, err := manifest.FetchObjectsFromYaml(manifestYAML)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	for _, obj := range objs {
		// uses the default gomega timeout and polling interval
		gomega.Eventually(func(g gomega.Gomega) {
			obj := obj.DeepCopy()
			err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj)
			g.Expect(err).ToNot(gomega.HaveOccurred())
		}).
			WithTimeout(timeout).
			WithPolling(pollingInterval).
			WithOffset(1). // reflect the line of the caller's stack
			Should(gomega.Succeed())
	}
}
