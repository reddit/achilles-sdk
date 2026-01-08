// Package federation provides helper functions for resolving federation resources
// to their associated networking information (CIDRs, security groups, etc.).
package federation

import (
	"context"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/reddit/achilles-sdk-api/api"
	cloudv1alpha1 "github.snooguts.net/reddit-go/infrared-crds/api/cloud.infrared.reddit.com/v1alpha1"
	clusterv1alpha1 "github.snooguts.net/reddit-go/infrared-crds/api/cluster.infrared.reddit.com/v1alpha1"
	federationv1alpha1 "github.snooguts.net/reddit-go/infrared-crds/api/federation.infrared.reddit.com/v1alpha1"
)

// TargetedClustersChangedPredicate is an event filter that only passes events when
// a Federation's targeted clusters have changed. Use this with Watches to avoid
// unnecessary reconciliations on Federation status updates that don't affect networking.
//
// Controllers that reference a Federation in their spec should set up a watch to reconcile
// when the Federation's targeted clusters change. Use the FSM builder's Watches method
// along with a field index for efficient lookups.
//
// First, create a field index for your CR's federation reference field:
//
//	if err := mgr.GetFieldIndexer().IndexField(
//		ctx,
//		&YourClaim{},
//		"spec.federationRef",
//		func(obj client.Object) []string {
//			claim := obj.(*YourClaim)
//			if claim.Spec.FederationRef == "" {
//				return nil
//			}
//			return []string{claim.Spec.FederationRef}
//		},
//	); err != nil {
//		return err
//	}
//
// Then add the watch to your FSM builder:
//
//	builder = builder.Watches(
//		&federationv1alpha1.Federation{},
//		handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
//			federation := object.(*federationv1alpha1.Federation)
//
//			// Find all CRs that reference this specific federation
//			var claims YourClaimList
//			if err := c.List(ctx, &claims,
//				client.MatchingFields{"spec.federationRef": federation.Name},
//			); err != nil {
//				return nil
//			}
//
//			requests := make([]reconcile.Request, 0, len(claims.Items))
//			for _, claim := range claims.Items {
//				requests = append(requests, reconcile.Request{
//					NamespacedName: client.ObjectKeyFromObject(&claim),
//				})
//			}
//			return requests
//		}),
//		fsmhandler.TriggerTypeRelative,
//		ctrlbuilder.WithPredicates(federation.TargetedClustersChangedPredicate),
//	)
var TargetedClustersChangedPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return true
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return true
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldFed, ok := e.ObjectOld.(*federationv1alpha1.Federation)
		if !ok {
			return true
		}
		newFed, ok := e.ObjectNew.(*federationv1alpha1.Federation)
		if !ok {
			return true
		}
		// Only reconcile if the targeted clusters changed
		return !reflect.DeepEqual(oldFed.Status.TargetedClusters, newFed.Status.TargetedClusters)
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return true
	},
}

// ClusterNetworkInfo contains networking information for a single RedditCluster.
type ClusterNetworkInfo struct {
	// ClusterName is the name of the RedditCluster.
	ClusterName string

	// ClusterID is the cluster ID (e.g., "core-prod-1").
	ClusterID string

	// AWSEnvironmentRef is the name of the AWSEnvironment associated with this cluster.
	AWSEnvironmentRef string

	// PodCIDR is the CIDR range for pods in this cluster.
	PodCIDR string

	// VPCCIDR is the VPC CIDR from the AWSEnvironment (only set if AWSEnvironment has managed networking).
	// This is the CIDR range where cluster nodes reside.
	VPCCIDR string
}

// FederationNetworkInfo contains networking information for all clusters in a federation.
type FederationNetworkInfo struct {
	// FederationName is the name of the Federation.
	FederationName string

	// Clusters contains networking information for each cluster in the federation.
	Clusters []ClusterNetworkInfo
}

// AllPodCIDRs returns a flat list of all pod CIDRs across all clusters in the federation.
func (f *FederationNetworkInfo) AllPodCIDRs() []string {
	var cidrs []string
	for _, c := range f.Clusters {
		if c.PodCIDR != "" {
			cidrs = append(cidrs, c.PodCIDR)
		}
	}
	return cidrs
}

// AllVPCCIDRs returns a flat list of all VPC CIDRs across all clusters in the federation.
func (f *FederationNetworkInfo) AllVPCCIDRs() []string {
	var cidrs []string
	for _, c := range f.Clusters {
		if c.VPCCIDR != "" {
			cidrs = append(cidrs, c.VPCCIDR)
		}
	}
	return cidrs
}

// GetFederationNetworkInfo resolves a Federation to networking information for all its targeted clusters.
// It fetches the Federation, then for each targeted cluster fetches the RedditCluster and optionally
// its associated AWSEnvironment to extract CIDR information.
//
// The includeVPCCIDR parameter controls whether to also fetch AWSEnvironment resources to get VPC CIDRs.
// This requires additional API calls but provides more complete networking information.
func GetFederationNetworkInfo(
	ctx context.Context,
	c client.Client,
	federationRef string,
	includeVPCCIDR bool,
) (*FederationNetworkInfo, error) {
	// Fetch the Federation
	federation := &federationv1alpha1.Federation{}
	if err := c.Get(ctx, client.ObjectKey{Name: federationRef}, federation); err != nil {
		return nil, fmt.Errorf("fetching Federation %s: %w", federationRef, err)
	}

	if !federationReady(federation) {
		return nil, fmt.Errorf("Federation %s is not ready", federationRef)
	}

	result := &FederationNetworkInfo{
		FederationName: federationRef,
		Clusters:       make([]ClusterNetworkInfo, 0, len(federation.Status.TargetedClusters)),
	}

	// For each targeted cluster, fetch the RedditCluster
	for _, target := range federation.Status.TargetedClusters {
		clusterInfo, err := getClusterNetworkInfo(ctx, c, target, includeVPCCIDR)
		if err != nil {
			return nil, fmt.Errorf("getting network info for cluster %s: %w", target.ClusterID, err)
		}
		result.Clusters = append(result.Clusters, *clusterInfo)
	}

	return result, nil
}

// getClusterNetworkInfo fetches networking information for a single RedditCluster.
func getClusterNetworkInfo(
	ctx context.Context,
	c client.Client,
	target *federationv1alpha1.Target,
	includeVPCCIDR bool,
) (*ClusterNetworkInfo, error) {
	// Fetch the RedditCluster
	cluster := &clusterv1alpha1.RedditCluster{}
	key := client.ObjectKey{
		Name:      target.RedditClusterRef.Name,
		Namespace: target.RedditClusterRef.Namespace,
	}
	if err := c.Get(ctx, key, cluster); err != nil {
		return nil, fmt.Errorf("fetching RedditCluster %s/%s: %w", key.Namespace, key.Name, err)
	}

	info := &ClusterNetworkInfo{
		ClusterName:       cluster.Name,
		ClusterID:         target.ClusterID,
		AWSEnvironmentRef: cluster.GetAWSEnvRef(),
	}

	// Extract networking CIDRs from the cluster spec
	if cluster.Spec.Cluster.Managed != nil {
		info.PodCIDR = cluster.Spec.Cluster.Managed.Networking.PodSubnet
	}

	// Optionally fetch AWSEnvironment for VPC CIDR
	if includeVPCCIDR && info.AWSEnvironmentRef != "" {
		awsEnv := &cloudv1alpha1.AWSEnvironment{}
		if err := c.Get(ctx, client.ObjectKey{Name: info.AWSEnvironmentRef}, awsEnv); err != nil {
			return nil, fmt.Errorf("fetching AWSEnvironment %s: %w", info.AWSEnvironmentRef, err)
		}

		if awsEnv.Spec.ManagedNetwork != nil {
			info.VPCCIDR = awsEnv.Spec.ManagedNetwork.VPCCIDR
		}
	}

	return info, nil
}

// federationReady checks if a Federation resource is ready by examining its Ready condition.
func federationReady(federation *federationv1alpha1.Federation) bool {
	readyCondition := federation.GetCondition(api.TypeReady)
	return readyCondition.Status == corev1.ConditionTrue &&
		readyCondition.ObservedGeneration == federation.GetGeneration()
}
