package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/reddit/achilles-sdk/pkg/shard"
)

const (
	errShardTypesUnset = "sharded-types must be set when watch-label-selector is set, otherwise the " +
		"selector would filter nothing"
	errShardLeaderElectionIDUnset = "leader-election-id must be set when watch-label-selector is set, " +
		"since it names both the per-shard leader election lock and the shard advertisement"
)

// serviceAccountNamespacePath is where an in-cluster pod finds its own namespace.
const serviceAccountNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// resolveShard parses the configured selector and validates the options sharding depends on.
// Returns a nil Shard when sharding is disabled.
func resolveShard(o *Options) (*shard.Shard, error) {
	s, err := shard.Parse(shard.DefaultKey, o.WatchLabelSelector)
	if err != nil || s == nil {
		return nil, err
	}

	if len(o.ShardedTypes) == 0 {
		return nil, errors.New(errShardTypesUnset)
	}
	if o.LeaderElectionID == "" {
		return nil, errors.New(errShardLeaderElectionIDUnset)
	}

	return s, nil
}

// shardedLeaderElectionID scopes the leader election lock to the shard. Without this every shard
// would contend for one lock, so exactly one replica across all shards would be active and the
// rest would idle — sharding would silently do nothing.
func shardedLeaderElectionID(id string, s *shard.Shard) string {
	if s == nil {
		return id
	}
	return fmt.Sprintf("%s-%s", id, s.ID())
}

// applyShardToCache restricts the informers backing the sharded types to this shard's slice.
//
// Only the declared root types are filtered. Children are labelled by the SDK (see shard.Propagate)
// but left unfiltered, so enabling sharding does not strand children created before the label
// existed. Entries are matched by GVK rather than by map key, since the caller may already have
// registered the same type through a different pointer.
func applyShardToCache(
	cacheOpts cache.Options,
	scheme *runtime.Scheme,
	s *shard.Shard,
	shardedTypes []client.Object,
) (cache.Options, error) {
	if s == nil {
		return cacheOpts, nil
	}

	byObject := map[client.Object]cache.ByObject{}
	existingKeys := map[schema.GroupVersionKind]client.Object{}
	for obj, opts := range cacheOpts.ByObject {
		byObject[obj] = opts
		gvk, err := apiutil.GVKForObject(obj, scheme)
		if err != nil {
			return cacheOpts, fmt.Errorf("resolving GVK for cached type %T: %w", obj, err)
		}
		existingKeys[gvk] = obj
	}

	for _, obj := range shardedTypes {
		gvk, err := apiutil.GVKForObject(obj, scheme)
		if err != nil {
			return cacheOpts, fmt.Errorf("resolving GVK for sharded type %T: %w", obj, err)
		}

		key := obj
		if existing, ok := existingKeys[gvk]; ok {
			key = existing
		}

		opts := byObject[key]
		selector := opts.Label
		if selector == nil {
			selector = labels.Everything()
		}
		// Add ANDs the requirements, so a caller's own scoping is narrowed rather than replaced.
		opts.Label = selector.Add(s.Requirements()...)
		byObject[key] = opts
	}

	cacheOpts.ByObject = byObject
	return cacheOpts, nil
}

// setupSharding verifies no other shard claims an overlapping slice and keeps this shard's
// advertisement alive for the life of the process.
//
// The verification runs against a direct client rather than the manager's: the manager's client is
// backed by a cache whose informers have not synced before Start, and the shard-filtered cache
// would not match these Leases in any case.
func setupSharding(
	ctx context.Context,
	cfg *rest.Config,
	mgr manager.Manager,
	opts *Options,
	s *shard.Shard,
	log *zap.SugaredLogger,
) error {
	namespace, err := shardNamespace(opts)
	if err != nil {
		return err
	}

	identity, err := processIdentity()
	if err != nil {
		return err
	}

	direct, err := client.New(cfg, client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return fmt.Errorf("building uncached client for shard verification: %w", err)
	}

	advertiser := shard.NewAdvertiser(direct, shard.Config{
		Shard:     s,
		Group:     opts.LeaderElectionID,
		Namespace: namespace,
		Identity:  identity,
		Log:       log,
	})

	if !opts.DisableShardOverlapCheck {
		if err := advertiser.Claim(ctx); err != nil {
			return err
		}
		if err := mgr.Add(advertiser); err != nil {
			return fmt.Errorf("registering shard advertisement renewal: %w", err)
		}
	}

	log.Infow("sharding enabled",
		"selector", s.String(),
		"shard", s.ID(),
		"leaderElectionID", shardedLeaderElectionID(opts.LeaderElectionID, s),
	)
	return nil
}

// shardNamespace is where advertisement Leases live, mirroring how leader election picks its
// namespace.
func shardNamespace(o *Options) (string, error) {
	if o.LeaderElectionNamespace != "" {
		return o.LeaderElectionNamespace, nil
	}

	ns, err := os.ReadFile(serviceAccountNamespacePath)
	if err != nil {
		return "", fmt.Errorf("determining namespace for shard advertisement, set leader-election-namespace: %w", err)
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
