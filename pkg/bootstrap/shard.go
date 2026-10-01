package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/reddit/achilles-sdk/pkg/shard"
)

const (
	errShardTypesUnset = "Shard.Types must be set when shard-values is set, otherwise no object " +
		"would belong to a shard"
	errShardLeaderElectionIDSet = "leader-election-id must not be set when shard-values is set: " +
		"the lock is named after this instance's own workload, so that two instances cannot share one"
	errShardLeaderElectionDisabled = "leader-election must be enabled when shard-values is set, " +
		"since it is what keeps two replicas of one instance from both claiming its values"
)

// serviceAccountNamespacePath is where an in-cluster pod finds its own namespace.
const serviceAccountNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// ShardOptions restricts an instance to a subset of the objects it would otherwise reconcile, so
// that several instances of one controller can run in a cluster over mutually exclusive slices.
//
// Its zero value disables sharding, leaving the controller to reconcile every object it manages.
type ShardOptions struct {
	// Values are the concrete shard values this instance serves. Every value in use must be served
	// by some instance; one no instance serves is reconciled by nobody.
	Values []string

	// Types are the partitioned types, whose objects are divided between instances. A type not
	// listed here is reconciled by every instance.
	Types []client.Object

	// InstanceName overrides the name derived from this pod's workload. Set it only when running
	// outside a Kubernetes workload, such as in tests.
	InstanceName string
}

// sharding is what sharding contributes to manager setup, resolved before the manager exists
// because it names the leader election lock.
type sharding struct {
	shard *shard.Shard

	// client is uncached: it reads this pod's own workload before the manager exists, and caching
	// Leases cluster-wide would pull in one per node to watch a handful.
	client client.Client

	// instance names this controller instance; identity names this process within it.
	instance string
	identity string

	namespace string
}

// resolveSharding prepares sharding and names the leader election lock after this instance,
// returning nil when sharding is disabled.
func resolveSharding(ctx context.Context, cfg *rest.Config, o *Options) (*sharding, error) {
	s, err := resolveShard(o)
	if err != nil || s == nil {
		return nil, err
	}

	c, err := directClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("building uncached client: %w", err)
	}

	namespace, err := shardNamespace(o)
	if err != nil {
		return nil, err
	}

	identity, err := processIdentity()
	if err != nil {
		return nil, err
	}

	instance := o.Shard.InstanceName
	if instance == "" {
		if instance, err = instanceName(ctx, c, namespace, identity); err != nil {
			return nil, err
		}
	}

	// Each instance elects a leader among its own replicas, which is what keeps two replicas of
	// one instance from both claiming its values.
	o.LeaderElectionID = instance

	return &sharding{
		shard:     s,
		client:    c,
		instance:  instance,
		identity:  identity,
		namespace: namespace,
	}, nil
}

// resolveShard parses the configured values and checks the options sharding depends on, returning
// nil when sharding is disabled.
func resolveShard(o *Options) (*shard.Shard, error) {
	s, err := shard.Parse(shard.DefaultKey, o.Shard.Values)
	if err != nil || s == nil {
		return nil, err
	}

	if len(o.Shard.Types) == 0 {
		return nil, errors.New(errShardTypesUnset)
	}
	if o.LeaderElectionID != "" {
		return nil, errors.New(errShardLeaderElectionIDSet)
	}
	if !o.LeaderElection {
		return nil, errors.New(errShardLeaderElectionDisabled)
	}

	return s, nil
}

// setup starts the loop that claims this instance's shard values, returning the Owner that gates
// reconciliation.
func (sh *sharding) setup(mgr manager.Manager, o *Options, log *zap.SugaredLogger) (*shard.Owner, error) {
	gvks, err := shardedGVKs(mgr.GetScheme(), o.Shard.Types)
	if err != nil {
		return nil, err
	}

	owner := shard.NewOwner(sh.client, shard.OwnerConfig{
		Shard:     sh.shard,
		Instance:  sh.instance,
		Namespace: sh.namespace,
		Identity:  sh.identity,
		List:      inventoryFunc(mgr.GetClient(), mgr.GetScheme(), sh.shard.Key(), gvks),
		Log:       log,
	})
	if err := mgr.Add(owner); err != nil {
		return nil, fmt.Errorf("registering shard ownership: %w", err)
	}

	log.Infow("sharding enabled",
		"values", sh.shard.String(), "instance", sh.instance, "namespace", sh.namespace)
	return owner, nil
}

// instanceName reports the name of the workload running this pod, which names both the leader
// election lock and the holder of this instance's shard Leases.
//
// It is read from the pod's owner rather than configured because two workloads in a namespace
// cannot share a name. That makes it impossible for two instances to share a leader election lock,
// and so impossible for two of them to be active at once.
//
// A Deployment interposes a per-revision ReplicaSet between itself and its pods, so that chain
// needs one extra hop; no other workload type interposes anything.
func instanceName(ctx context.Context, c client.Reader, namespace, podName string) (string, error) {
	var pod corev1.Pod
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: podName}, &pod); err != nil {
		return "", fmt.Errorf("reading own pod %s/%s: %w", namespace, podName, err)
	}

	owner := controllerOf(pod.GetOwnerReferences())
	if owner == nil {
		return "", fmt.Errorf(
			"pod %s/%s is not managed by a workload, so this instance cannot be named; set Shard.InstanceName",
			namespace, podName,
		)
	}
	if owner.Kind != "ReplicaSet" {
		return owner.Name, nil
	}

	var rs appsv1.ReplicaSet
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: owner.Name}, &rs); err != nil {
		return "", fmt.Errorf("reading own ReplicaSet %s/%s: %w", namespace, owner.Name, err)
	}
	if deployment := controllerOf(rs.GetOwnerReferences()); deployment != nil {
		return deployment.Name, nil
	}
	return owner.Name, nil
}

func controllerOf(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return nil
}

// shardedGVKs resolves the partitioned types, which bound the inventory scan.
func shardedGVKs(scheme *runtime.Scheme, types []client.Object) ([]schema.GroupVersionKind, error) {
	gvks := make([]schema.GroupVersionKind, 0, len(types))
	for _, obj := range types {
		gvk, err := apiutil.GVKForObject(obj, scheme)
		if err != nil {
			return nil, fmt.Errorf("resolving GVK for partitioned type %T: %w", obj, err)
		}
		gvks = append(gvks, gvk)
	}
	return gvks, nil
}

// inventoryFunc reports which shard values objects actually carry, which is what the report of
// unserved values is computed against.
//
// It reads the manager's cache, which already holds these types because the controllers watch
// them, so a scan costs no API traffic and is proportional to the number of objects rather than to
// the number of shards.
func inventoryFunc(
	c client.Reader,
	scheme *runtime.Scheme,
	key string,
	gvks []schema.GroupVersionKind,
) shard.ListFunc {
	return func(ctx context.Context) (shard.Inventory, error) {
		inventory := shard.Inventory{Values: sets.New[string]()}

		for _, gvk := range gvks {
			obj, err := scheme.New(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
			if err != nil {
				return inventory, fmt.Errorf("constructing %s list: %w", gvk, err)
			}
			list, ok := obj.(client.ObjectList)
			if !ok {
				return inventory, fmt.Errorf("%s list is not a client.ObjectList", gvk)
			}
			// Only labels are read and nothing is retained, so the cache's defensive copy of
			// every object would be wasted work on a scan that repeats every few seconds.
			if err := c.List(ctx, list, client.UnsafeDisableDeepCopy); err != nil {
				return inventory, fmt.Errorf("listing %s: %w", gvk, err)
			}

			if err := apimeta.EachListItem(list, func(item runtime.Object) error {
				o, ok := item.(client.Object)
				if !ok {
					return fmt.Errorf("%T is not a client.Object", item)
				}
				// An empty value counts as missing, matching how ownership reads it: neither can
				// be configured on an instance, so neither is reconcilable.
				if value := o.GetLabels()[key]; value != "" {
					inventory.Values.Insert(value)
				} else {
					inventory.Missing = true
				}
				return nil
			}); err != nil {
				return inventory, err
			}
		}

		return inventory, nil
	}
}

// shardNamespace is where a shard's Leases live, mirroring how leader election picks its namespace.
func shardNamespace(o *Options) (string, error) {
	if o.LeaderElectionNamespace != "" {
		return o.LeaderElectionNamespace, nil
	}

	ns, err := os.ReadFile(serviceAccountNamespacePath)
	if err != nil {
		return "", fmt.Errorf("determining the namespace for shard leases, set leader-election-namespace: %w", err)
	}
	return strings.TrimSpace(string(ns)), nil
}

// processIdentity distinguishes this process from the other replicas of its instance.
func processIdentity() (string, error) {
	if pod := os.Getenv("POD_NAME"); pod != "" {
		return pod, nil
	}

	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("determining this process's identity: %w", err)
	}
	return host, nil
}

// directClient reads this pod's own workload and the shard Leases, both of which are needed before
// the manager's cache exists.
func directClient(cfg *rest.Config) (client.Client, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding Kubernetes schemes: %w", err)
	}
	return client.New(cfg, client.Options{Scheme: scheme})
}
