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
	errShardTypesUnset = "sharded-types must be set when shard-selector is set, otherwise no object " +
		"would ever be assigned to a shard"
	errShardLeaderElectionIDUnset = "leader-election-id must be set when shard-selector is set, " +
		"since it names the per-shard leader election lock and scopes the shard's Leases"
	errShardLeaderElectionDisabled = "leader-election must be enabled when shard-selector is set, " +
		"since it is what keeps two replicas of one shard from both claiming its values"
)

// serviceAccountNamespacePath is where an in-cluster pod finds its own namespace.
const serviceAccountNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// resolveShard parses the configured selector and validates the options sharding depends on.
// Returns a nil Shard when sharding is disabled.
func resolveShard(o *Options) (*shard.Shard, error) {
	s, err := shard.Parse(shard.DefaultKey, o.ShardSelector)
	if err != nil || s == nil {
		return nil, err
	}

	if len(o.ShardedTypes) == 0 {
		return nil, errors.New(errShardTypesUnset)
	}
	if o.LeaderElectionID == "" {
		return nil, errors.New(errShardLeaderElectionIDUnset)
	}
	if !o.LeaderElection {
		return nil, errors.New(errShardLeaderElectionDisabled)
	}

	return s, nil
}

// shardedLeaderElectionID scopes the leader election lock to the shard, so that each shard elects a
// leader among its own replicas. Sharing one lock would instead elect one leader across all shards,
// leaving every other shard's objects unreconciled while its pods stayed healthy.
func shardedLeaderElectionID(id string, s *shard.Shard) string {
	if s == nil {
		return id
	}
	return fmt.Sprintf("%s-%s", id, s.ID())
}

// setupSharding advertises this shard's selector and starts the loop that acquires ownership of the
// shard values it selects. Returns the Owner, which gates reconciliation.
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

	advertiser := shard.NewAdvertiser(direct, shard.Config{
		Shard:     s,
		Group:     opts.LeaderElectionID,
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

	list, err := inventoryFunc(mgr, s.Key(), opts.ShardedTypes)
	if err != nil {
		return nil, err
	}

	owner := shard.NewOwner(direct, shard.OwnerConfig{
		Shard:     s,
		Group:     opts.LeaderElectionID,
		Namespace: namespace,
		Identity:  identity,
		List:      list,
		Log:       log,
	})
	if err := mgr.Add(owner); err != nil {
		return nil, fmt.Errorf("registering shard ownership: %w", err)
	}

	log.Infow("sharding enabled",
		"selector", s.String(),
		"shard", s.ID(),
		"leaderElectionID", shardedLeaderElectionID(opts.LeaderElectionID, s),
	)
	return owner, nil
}

// inventoryFunc reports which shard values objects actually carry, which is the set ownership is
// arbitrated over.
//
// It reads the manager's cache, which already holds these types because the controllers watch them,
// so a scan costs no API traffic. The scan is proportional to the number of objects, not to the
// number of shards, and it runs on every ownership sync.
func inventoryFunc(mgr manager.Manager, key string, shardedTypes []client.Object) (shard.ListFunc, error) {
	scheme := mgr.GetScheme()

	listGVKs := make([]schema.GroupVersionKind, 0, len(shardedTypes))
	for _, obj := range shardedTypes {
		gvk, err := apiutil.GVKForObject(obj, scheme)
		if err != nil {
			return nil, fmt.Errorf("resolving GVK for sharded type %T: %w", obj, err)
		}
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
				if value, labelled := o.GetLabels()[key]; labelled {
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
	}, nil
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
