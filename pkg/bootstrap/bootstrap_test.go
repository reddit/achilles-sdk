package bootstrap

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("buildRestConfig should fail",
	func(inCluster bool, kubeContext string, want string) {
		opts := &Options{
			InCluster:   inCluster,
			KubeContext: kubeContext,
		}
		_, err := buildRestConfig(opts)
		Expect(err).Should(MatchError(want))
	},
	Entry("implicitly", false, "", errNoValidKubeContext),
	Entry("when both inCluster and context are set",
		true, "foo", errKubeContextSetInCluster),
	Entry("nonexistent context", false, "foo", "context \"foo\" does not exist"),
)

func TestBootstrap(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bootstrap")
}
