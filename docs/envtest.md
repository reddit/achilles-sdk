# envtest

This document describes what `envtest` integration tests are, why they are valuable for testing Kubernetes controllers,
and how to write them.

## What is `envtest`?

`envtest` is a binary that runs an instance of `kube-apiserver` and `etcd`, enabling integration testing of Kubernetes
controllers and its interaction against the Kubernetes control plane. It is part of the [Kubebuilder](https://book.kubebuilder.io/) project,
and the `envtest` docs can be [found here](https://book.kubebuilder.io/reference/envtest.html).

The Achilles SDK provides a Go wrapper around `envtest`, making it easy to programmatically start, stop, and integrate controllers being tested against 
the test environment.

## Why Use `envtest`?

The bulk of the implementation of any Kubernetes controller, regardless of business logic, reduces down to CRUD operations 
against the Kubernetes API server. Therefore, a controller's core correctness can be exercised as asserting that the
controller instantiates the expected Kubernetes state (i.e. child objects) in response to changes in the declared state (i.e. parent objects).

Furthermore, the Kubernetes API server imposes a number of constraints that only surface at runtime when the client (i.e. the controller) sends the request to the server.

Examples of these constraints include:

1. RBAC control: Is your controller configured with the correct Kubernetes RBAC for CRUDing the resources it needs to?
2. Core API semantics
   1. Does your controller respect [Kubernetes naming constraints](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/)?
   2. Does your controller send updates correctly? For example, does it specify the [`metadata.resourceVersion`](https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions) of the objects it reads and writes?
3. Update semantics: Is your controller serializing Go structs to YAML correctly?

Lastly, Kubernetes controllers are dynamic processes that communicate asynchronously with the kube-apiserver. The actual
state instantiated by the controller and the kube-apiserver are _eventually consistent_, meaning that the actual state
eventually converges to the desired state.  It is difficult to faithfully reproduce an eventually consistent environment
without running these asynchronous processes in your test environment.

## How to write `envtest` integration tests

### Exercise a Single Controller Per Test

We recommend writing a single `envtest` integration test for each controller you write. This test should only exercise
a single controller's logic, and should not test the interaction between multiple controllers.

If your controller interacts with other controllers or other APIs, your test should _mock_ the behavior of those external
systems, similar to classical unit testing methodology. For instance, if your controller creates a Deployment object
and waits until that Deployment becomes ready, you would insert test logic that emulates the Kubernetes control plane
processing the Deployment and setting its status to `Ready` or `Failed`.

### Setting Up Your Test

We use the [Ginkgo](https://onsi.github.io/ginkgo/) testing framework to write our tests. Ginkgo is a BDD-style testing
framework that allows us to write tests in a behavior-driven format.

We use the [Gomega](https://onsi.github.io/gomega/) matcher library to write assertions in our tests. It's especially
useful for ergonomically making asynchronous assertions, required in eventually consistent systems.  In most cases you'll
use the `Eventually` matcher to assert that a condition will eventually be true, and the `Consistently`
matcher to assert that a condition will remain true for a period of time.

To get started, install the `ginkgo` CLI:

```shell
go install github.com/onsi/ginkgo/v2/ginkgo@latest
```

If ginkgo is already installed, make sure you are running ginkgo v2. If you are running v1, upgrade to ginkgo v2 by following [this guide](https://github.com/onsi/ginkgo/blob/ver2/docs/MIGRATING_TO_V2.md#upgrading-to-ginkgo-20). 

Next, create a new test suite by running:

```shell
cd /path/to/your/controller/package
ginkgo bootstrap .
```

This will create a `*_suite_test.go` file, which will contain the setup for the test suite.
Inside of this file, we scaffold a new `envtest` IT with the following Go code:

```golang
package mycontroller_test // we recommend using a different package than your controller package so your test exercises only the public interface of the controller package

import (
	"context"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/fgrosse/zaptest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.snooguts.net/reddit/achilles-sdk/pkg/fsm/metrics"
	"github.snooguts.net/reddit/achilles-sdk/pkg/logging"
	libratelimiter "github.snooguts.net/reddit/achilles-sdk/pkg/ratelimiter"
	"github.snooguts.net/reddit/achilles-sdk/pkg/test"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	ctrlzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.snooguts.net/reddit/mycontroller/internal/controllers/mycontroller"
	"github.snooguts.net/reddit/mycontroller/internal/controlplane"
	libtest "github.snooguts.net/reddit/mycontroller/internal/test"
	ctrlscheme "github.snooguts.net/reddit/mycontroller/pkg/scheme"
)

var (
	ctx        context.Context
	scheme     *runtime.Scheme
	log        *zap.SugaredLogger
	testEnv    *test.TestEnv
	testClient client.Client
)

func TestMyController(t *testing.T) {
	RegisterFailHandler(Fail)

	log = zaptest.LoggerWriter(GinkgoWriter).Sugar() // wires up the controller output to the Ginkgo test runner so logs show up in the shell output
	ctrllog.SetLogger(ctrlzap.New(ctrlzap.WriteTo(GinkgoWriter), ctrlzap.UseDevMode(true)))

	RunSpecs(t, "mycontroller Suite")
}

var _ = BeforeSuite(func() {
	// default timeouts for "eventually" and "consistently" Ginkgo matchers
	SetDefaultEventuallyTimeout(40 * time.Second)
	SetDefaultEventuallyPollingInterval(200 * time.Millisecond)
	SetDefaultConsistentlyDuration(3 * time.Second)
	SetDefaultConsistentlyPollingInterval(200 * time.Millisecond)

	ctx = logging.NewContext(context.Background(), log)
	rl := libratelimiter.NewDefaultProviderRateLimiter(libratelimiter.DefaultProviderRPS)

	// add test CRD schemes
	scheme = ctrlscheme.MustNewScheme()

	// fetch external CRDs.
    // TODO: this is optional. You only need this if your controller makes use of
    // other codebases' resources.
	externalCRDDirectories, err := test.ExternalCRDDirectoryPaths(map[string][]string{
		"github.com/some/other/repo/apis/v1alpha1": {
			path.Join("config", "crd", "bases"),
		},
	}, libtest.RootDir())
	Expect(err).ToNot(HaveOccurred())

	testEnv, err = test.NewEnvTestBuilder(ctx).
		WithCRDDirectoryPaths(
			append(externalCRDDirectories,
				// enumerate all directories containing CRD YAMLs
				filepath.Join(libtest.RootDir(), "manifests", "base", "crd", "bases"),
			)).
		WithScheme(scheme).
		WithLog(log.Desugar()).
		WithManagerSetupFns(
			func(mgr manager.Manager) error {
				return mycontroller.SetupController(
					ctx,
					controlplane.Context{
						Metrics: metrics.MustMakeMetrics(scheme, prometheus.NewRegistry()),
					},
					mgr,
					rl,
				) // wires up controller being tested to the kube-apiserver
			},
		).
		WithKubeConfigFile("./"). // test suite will output a kubeconfig file located in the specified directory
		Start()                   // start invokes the `envtest` binary to start the `kube-apiserver` and `etcd` processes on the host
	Expect(err).ToNot(HaveOccurred())
	testClient = testEnv.Client
})

// AfterSuite tears down the test environment by terminating the `envtest` processes once the test finishes
// Without this, the host will have orphaned `kube-apiserver` and `etcd` processes that will require manual cleanup
var _ = AfterSuite(func() {
	By("tearing down the test environment", func() {
		if testEnv != nil {
			Expect(testEnv.Stop()).To(Succeed())
		}
	})
})

```

Now, you can set up envtests in `_test.go` files, just like other Go tests. Here's an example:

```golang
package mycontroller_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TODO: Put your env tests here.
var _ = Describe("mycontroller", Ordered, func() {
	It("should work", func() {
		Eventually(func(g Gomega) {
			g.Expect(true).To(Equal(true))
		}).Should(Succeed())
	})
})

```

When executing this test via `go test`, the `envtest` binary will start the `kube-apiserver` and `etcd` processes on the host.
The controller being tested will be wired up to the `kube-apiserver` and will be able to interact with the Kubernetes control plane.

### Writing Your Test

`envtest` ITs should be expressed in a behavioral manner, which is higher level than how you might express a unit test.
For controller automation, the general structure of a test would be to mimic how an actor would use the custom resource:

1. Actor (human or program) creates the custom resource
2. Test asserts that the controller processes the custom resource and performs the expected actions:
   1. Creates expected child resources
   2. Updates the custom resource's status
   3. Performs expected actions against other integrated external systems (e.g. issues a request to a REST API)
3. Actor updates the custom resource
4. Test asserts that the controller processes the update and performs the expected actions
5. Actor deletes the custom resource
6. Test asserts that the controller performs expected cleanup actions

Here is an example of a test that exercises the controller's behavior when a custom resource is created:

```golang
var _ = Describe("MyController", func() {
    Context("when a MyResource is created", func() {
        It("should create a MyChildResource", func() {
            By("creating a MyResource", func() {
                myResource := &mycontroller.MyResource{
                    ObjectMeta: metav1.ObjectMeta{
                        Name:      "my-resource",
                        Namespace: "default",
                    },
                    Spec: mycontroller.MyResourceSpec{
                        // fill in the spec fields
                    },
                }
                Expect(orchClient.Create(ctx, myResource)).To(Succeed())
            })

            By("waiting for the MyChildResource to be created", func() {
                expected := &mycontroller.MyChildResource{
                    // fill in expected state
                }
                
                Eventually(func(g Gomega) {
                    actual := &mycontroller.MyChildResource{}
                    g.Expect(orchClient.Get(ctx, client.ObjectKey{Name: "my-child-resource", Namespace: "default"}, actual)).To(Succeed())
                    g.Expect(actual).To(Equal(expected))
                }).Should(Succeed())
            })
        })
    })
})
```

Notice that we use an "Eventually" assertion. Because Kubernetes is an eventually consistent system, the test assertion
must poll for the expected state, with a user-configured timeout and polling interval.

`envtest` ITs can also exercise controller failures modes by emulating conditions under which your controller will error
out.

To see a full example of a `envtest` IT, refer to the [Achilles Federation controller test](https://github.snooguts.net/reddit/achilles/blob/f5f453b25216fd25f68c22b453345eb6777efbf0/orchestration-controller-manager/internal/controllers/federation/federation_reconciler_test.go).

### Running Your Test

To run your test, execute the following command:

```shell
go test ./path/to/your/controller/package
```

### Debugging Your Test

Controllers are more difficult to debug that single-threaded in-memory tests, both because of the asynchronous nature of
the controller and control plane, and because `envtest` runs the control plane as an out-of-band process (rather than embedded
in the test process).

That being said, we have some extremely helpful methods for debugging controllers.

#### Manually Interact with the Envtest Control Plane

By calling `.WithKubeConfigFile(/path/to/kubeconfig)` in your envtest builder, the test suite will output a kubeconfig file that you can
use to interact with the control plane using `kubectl`. As long as the test suite is still running, the envtest control plane
will be accessible via `kubectl`.

The recommended workflow is to execute the test in a debugger and pause the test execution at the point where you want to
inspect the control plane. Then, pass the kubeconfig to `kubectl` to inspect the state of the control plane:

```shell
kubectl --kubeconfig /path/to/kubeconfig get pods -A
```

This allows the test author to inspect arbitrary state on the control plane, which is much more efficient than
instrumenting the test logic or controller logic to output all relevant state.

#### Run the Tests in a Debugger

Running tests in an interactive debugger allows you to pause the test execution and inspect the _in-memory_ state
of the controller, the control plane, and the test environment.

If using Jetbrains GoLand, you can set breakpoints in your test code and run the test with the debugger. The test will pause
at the breakpoint, and you can inspect the state of the controller, the control plane, and the test environment.
