package meta

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ApplicationNameKey represents the name of the application
	ApplicationNameKey = "infrared.reddit.com/name"

	// ApplicationVersionKey represents the version of the application
	ApplicationVersionKey = "infrared.reddit.com/version"

	// ComponentNameKey represents the name of the specific component in the application
	ComponentNameKey = "infrared.reddit.com/component"

	// ManagedByKey represents the name of the controller managing the resource
	ManagedByKey = "infrared.reddit.com/managed-by"

	// SuspendKey is the label key on an object that should be used to temporarily suspend reconciliation on
	// an object.
	SuspendKey = "infrared.reddit.com/suspend"
)

var (
	// these variables should be overridden at program initialization time
	ApplicationName    = ""
	ApplicationVersion = ""
	ComponentName      = ""
)

// InitRedditLabels must be invoked at application start to initialize labels.
func InitRedditLabels(applicationName, applicationVersion, componentName string) {
	ApplicationName = applicationName
	ApplicationVersion = applicationVersion
	ComponentName = componentName
}

// RedditLabels is the set of labels common to all resources managed by an application
func RedditLabels(controllerName string) map[string]string {
	return map[string]string{
		ApplicationNameKey:    ApplicationName,
		ApplicationVersionKey: ApplicationVersion,
		ComponentNameKey:      ComponentName,
		ManagedByKey:          controllerName,
	}
}

// SetRedditLabels updates an object's meta.labels with common reddit labels.
// Must be invoked inside the mutateFn of controllerutil.CreateOrUpdate or controllerutil.CreateOrPatch
func SetRedditLabels(obj client.Object, controllerName string) {
	// initialize labels map if nil
	if obj.GetLabels() == nil {
		obj.SetLabels(map[string]string{})
	}

	// merge in reddit labels against existing labels
	objLabels := obj.GetLabels()
	for k, v := range RedditLabels(controllerName) {
		objLabels[k] = v
	}
}

// HasSuspendLabel checks if the label `SuspendKey` has been set in the object's meta.labels.
func HasSuspendLabel(o client.Object) bool {
	labels := o.GetLabels()
	if labels == nil {
		return false
	}

	return labels[SuspendKey] != ""
}
