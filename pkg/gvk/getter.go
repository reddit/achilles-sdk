package gvk

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/gobuffalo/flect"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResourceGetter allows retrieval of Kubernetes API resource (including CRDs) metadata
// with optional filters. The returned metadata associates the concepts of
// group, version, kind, resource, client.Object, and client.ObjectList.
// The implementation uses the kube-apiserver resource discovery API.
type ResourceGetter struct {
	discoveryClient discovery.DiscoveryInterface
	scheme          *runtime.Scheme
	opts            filterOptions
}

// NewResourceGetter returns a new ResourceGetter.
func NewResourceGetter(
	scheme *runtime.Scheme,
	discoveryClient discovery.DiscoveryInterface,
) *ResourceGetter {
	return &ResourceGetter{
		scheme:          scheme,
		discoveryClient: discoveryClient,
	}
}

type filterOptions struct {
	// filter by cluster/namespace scope if non-empty
	scope meta.RESTScopeName

	// filter by resource category if non-empty, uses OR semantics
	categories sets.Set[string]

	// filter by group if non-empty, uses OR semantics
	groups sets.Set[string]

	// filter by version if non-empty, uses OR semantics
	versions sets.Set[string]
}

// Resource represents Kubernetes API resource metadata,
// and associates the concepts of group, version, kind, resource, client.Object, and client.ObjectList.
type Resource struct {
	Group      string
	Version    string
	Kind       string
	Resource   string
	Object     client.Object
	ObjectList client.ObjectList
}

// WithCategories filters for resources with the given categories.
// Reference: https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#categories.
func (r *ResourceGetter) WithCategories(categories ...string) *ResourceGetter {
	r.opts.categories = sets.New[string](categories...)
	return r
}

// WithScope filters for resources with the specified scope (either namespace or cluster scoped).
func (r *ResourceGetter) WithScope(scope meta.RESTScopeName) *ResourceGetter {
	r.opts.scope = scope
	return r
}

// WithGroups filters for resources with the specified groups.
// Reference: https://kubernetes.io/docs/concepts/overview/kubernetes-api/#api-groups-and-versioning.
func (r *ResourceGetter) WithGroups(groups ...string) *ResourceGetter {
	r.opts.groups = sets.New[string](groups...)
	return r
}

// WithVersions filters for resources with the specified versions.
// Reference: https://kubernetes.io/docs/concepts/overview/kubernetes-api/#api-groups-and-versioning.
func (r *ResourceGetter) WithVersions(versions ...string) *ResourceGetter {
	r.opts.versions = sets.New[string](versions...)
	return r
}

// Get returns the resources filtered by the specified criteria.
func (r *ResourceGetter) Get() ([]Resource, error) {
	apiGroupResources, err := restmapper.GetAPIGroupResources(r.discoveryClient)
	if err != nil {
		return nil, fmt.Errorf("getting api group resources: %w", err)
	}

	selectedResources := map[schema.GroupVersionKind]metav1.APIResource{}

	for _, apiGroupResource := range apiGroupResources {
		for version, versionedResource := range apiGroupResource.VersionedResources {
			for _, resource := range versionedResource {
				// NOTE: these data must be sourced from the following structs, they can be empty on the metav1.APIResource
				group := apiGroupResource.Group.Name
				version := version
				kind := resource.Kind

				// omit status and scale subresources
				// NOTE: we can't infer subresources from the presence of "/" because that captures non-CRD subresources (like "namespaces/finalizer"),
				// 	     so in the event that future versions of k8s add new CRD subresources, we need to update this conditional.
				if strings.HasSuffix(resource.Name, "/status") || strings.HasSuffix(resource.Name, "/scale") {
					continue
				}

				// filter by versions
				if r.opts.versions != nil && !r.opts.versions.Has(version) {
					continue
				}

				// filter by scope if provided
				scope := meta.RESTScopeNameRoot
				if resource.Namespaced {
					scope = meta.RESTScopeNameNamespace
				}
				if r.opts.scope != "" && r.opts.scope != scope {
					continue
				}

				// filter by categories if provided
				categories := sets.New[string](resource.Categories...)
				if r.opts.categories != nil && len(r.opts.categories.Intersection(categories)) == 0 {
					continue
				}

				gvk := schema.GroupVersionKind{
					Group:   group,
					Version: version,
					Kind:    kind,
				}
				selectedResources[gvk] = resource
			}
		}
	}

	resourcesByGVK := map[schema.GroupVersionKind]*Resource{}
	for gvk, t := range r.scheme.AllKnownTypes() {
		var isListKind bool
		if strings.HasSuffix(gvk.Kind, "List") {
			isListKind = true
			gvk.Kind = strings.TrimSuffix(gvk.Kind, "List")
		}

		apiResource, ok := selectedResources[gvk]
		if !ok {
			continue
		}

		// must use same pluralization logic as controller-gen, https://github.com/kubernetes-sigs/controller-tools/blob/8cb5ce83c3cca425a4de0af3d2578e31a3cd6a48/pkg/crd/spec.go#L23
		if isListKind {
			apiResource.Name = flect.Pluralize(strings.TrimSuffix(apiResource.Name, "lists"))
		}

		resource, ok := resourcesByGVK[gvk]
		if !ok {
			resource = &Resource{
				Group:    gvk.Group,
				Version:  gvk.Version,
				Kind:     gvk.Kind,
				Resource: apiResource.Name,
			}
			resourcesByGVK[gvk] = resource
		}

		if isListKind {
			objList, ok := reflect.New(t).Interface().(client.ObjectList)
			if ok {
				resource.ObjectList = objList
			}
		} else {
			obj, ok := reflect.New(t).Interface().(client.Object)
			if ok {
				resource.Object = obj
			}
		}
	}

	var resources []Resource
	for _, resource := range resourcesByGVK {
		resources = append(resources, *resource)
	}

	return resources, nil
}
