package io_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/test"
)

func TestIo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "IO Suite")
}

var (
	testEnv *test.TestEnv
	c       client.Client

	ctx context.Context
)

var _ = BeforeSuite(func() {
	ctx = context.Background()

	var err error
	testEnv, err = test.NewEnvTestBuilder(ctx).Start()
	Expect(err).ToNot(HaveOccurred())

	c = testEnv.Client
})

var _ = AfterSuite(func() {
	By("tearing down the test environment", func() {
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	})
})
