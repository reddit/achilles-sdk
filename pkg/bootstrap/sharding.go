package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/reddit/achilles-sdk/pkg/shard"
)

const (
	errShardTypesUnset = "Shard.Types must be set when shard-values is set, otherwise no object " +
		"would ever be assigned to a shard"
	errShardInstanceUnset = "instance-name must be set when shard-values is set, and must be the " +
		"name of this controller's own Deployment, since it names the leader election lock that " +
		"separates this instance from the others"
	errShardLeaderElectionIDSet = "leader-election-id must not be set when shard-values is set: " +
		"the lock is named after instance-name instead, so that two instances cannot share one lock"
	errShardLeaderElectionDisabled = "leader-election must be enabled when shard-values is set, " +
		"since it is what keeps two replicas of one instance from both claiming its values"
)

// serviceAccountNamespacePath is where an in-cluster pod finds its own namespace.
const serviceAccountNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// resolveShard parses the configured shard values and validates the options sharding depends on.
// Returns a nil Shard when sharding is disabled.
func resolveShard(o *Options) (*shard.Shard, error) {
	s, err := shard.Parse(shard.DefaultKey, o.Shard.Values)
	if err != nil || s == nil {
		return nil, err
	}

	if len(o.Shard.Types) == 0 {
		return nil, errors.New(errShardTypesUnset)
	}
	if o.Shard.InstanceName == "" {
		return nil, errors.New(errShardInstanceUnset)
	}
	if o.LeaderElectionID != "" {
		return nil, errors.New(errShardLeaderElectionIDSet)
	}
	if !o.LeaderElection {
		return nil, errors.New(errShardLeaderElectionDisabled)
	}

	return s, nil
}

// leaderElectionID names the lock the manager elects on.
//
// A sharded controller elects on its instance name, i.e. its own Deployment name, so each instance
// elects a leader among its own replicas. Deriving the name from the shard values instead would
// make it change whenever they are edited, briefly leaving the outgoing and incoming pods of one
// instance holding different locks and both acting as leader.
func leaderElectionID(o *Options, s *shard.Shard) string {
	if s == nil {
		return o.LeaderElectionID
	}
	return o.Shard.InstanceName
}

// setupSharding advertises this instance's shard values and starts the loop that acquires ownership
// of them. Returns the Owner, which gates reconciliation.
//
// Both run against a direct client rather than the manager's: the advertisement is written before
// the manager starts, and caching Leases cluster-wide would pull in one Lease per node.
func setupSharding(
	ctx context.Context,
	cfg *rest.Config,
	mgr manager.Manager,
	opts *Options,
	s *shard.Shard,
	log *zap.SugaredLogger,
) (*shard.Owner, error) {
	namespace, err := shardNamespace(opts)
	if err != nil {
		return nil, err
	}

	identity, err := processIdentity()
	if err != nil {
		return nil, err
	}

	direct, err := client.New(cfg, client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return nil, fmt.Errorf("building uncached client for shard leases: %w", err)
	}

	gvks, err := shardedGVKs(mgr.GetScheme(), opts.Shard.Types)
	if err != nil {
		return nil, err
	}
	group, err := shard.GroupFor(gvks)
	if err != nil {
		return nil, err
	}

	advertiser := shard.NewAdvertiser(direct, shard.Config{
		Shard:     s,
		Group:     group,
		Instance:  opts.Shard.InstanceName,
		Namespace: namespace,
		Identity:  identity,
		Log:       log,
	})
	if err := advertiser.Advertise(ctx); err != nil {
		return nil, err
	}
	if err := mgr.Add(advertiser); err != nil {
		return nil, fmt.Errorf("registering shard advertisement renewal: %w", err)
	}

	owner := shard.NewOwner(direct, shard.OwnerConfig{
		Shard:     s,
		Group:     group,
		Instance:  opts.Shard.InstanceName,
		Namespace: namespace,
		Identity:  identity,
		List:      inventoryFunc(mgr, s.Key(), gvks),
		Log:       log,
	})
	if err := mgr.Add(owner); err != nil {
		return nil, fmt.Errorf("registering shard ownership: %w", err)
	}

	log.Infow("sharding enabled",
		"values", s.String(),
		"shard", s.ID(),
		"instance", opts.Shard.InstanceName,
		"group", group.Name,
	)
	return owner, nil
}

// shardedGVKs resolves the declared partitioned GVKs, which both scope the shard's Leases and
// bound the inventory scan.
func shardedGVKs(scheme *runtime.Scheme, shardedTypes []client.Object) ([]schema.GroupVersionKind, error) {
	gvks := make([]schema.GroupVersionKind, 0, len(shardedTypes))
	for _, obj := range shardedTypes {
		gvk, err := apiutil.GVKForObject(obj, scheme)
		if err != nil {
			return nil, fmt.Errorf("resolving GVK for sharded type %T: %w", obj, err)
		}
		gvks = append(gvks, gvk)
	}
	return gvks, nil
}

// inventoryFunc reports which shard values objects actually carry, which is what the ignored-value
// metric is computed against. Ownership itself is configured, not discovered from this.
//
// It reads the manager's cache, which already holds these types because the controllers watch them,
// so a scan costs no API traffic. The scan is proportional to the number of objects, not to the
// number of shards, and it runs on every ownership sync.
func inventoryFunc(mgr manager.Manager, key string, gvks []schema.GroupVersionKind) shard.ListFunc {
	scheme := mgr.GetScheme()

	listGVKs := make([]schema.GroupVersionKind, 0, len(gvks))
	for _, gvk := range gvks {
		listGVKs = append(listGVKs, gvk.GroupVersion().WithKind(gvk.Kind+"List"))
	}

	return func(ctx context.Context) (shard.Inventory, error) {
		inventory := shard.Inventory{Values: sets.New[string]()}

		for _, gvk := range listGVKs {
			obj, err := scheme.New(gvk)
			if err != nil {
				return inventory, fmt.Errorf("constructing %s: %w", gvk, err)
			}
			list, ok := obj.(client.ObjectList)
			if !ok {
				return inventory, fmt.Errorf("%s is not a client.ObjectList", gvk)
			}
			if err := mgr.GetClient().List(ctx, list); err != nil {
				return inventory, fmt.Errorf("listing %s: %w", gvk, err)
			}

			if err := apimeta.EachListItem(list, func(item runtime.Object) error {
				o, ok := item.(client.Object)
				if !ok {
					return fmt.Errorf("%T is not a client.Object", item)
				}
				// An empty value counts as unlabeled, matching how ownership reads it: neither
				// can be declared on an instance, so neither is reconcilable.
				if value := o.GetLabels()[key]; value != "" {
					inventory.Values.Insert(value)
				} else {
					inventory.Unlabeled = true
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
		return "", fmt.Errorf("determining namespace for shard leases, set leader-election-namespace: %w", err)
	}
	return strings.TrimSpace(string(ns)), nil
}

// processIdentity distinguishes this process from others serving the same shard.
func processIdentity() (string, error) {
	if pod := os.Getenv("POD_NAME"); pod != "" {
		return pod, nil
	}
	return os.Hostname()
}
