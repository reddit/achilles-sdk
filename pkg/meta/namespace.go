package meta

const (
	// InfraredSystemNamespace is the namespace containing all Achilles related workloads.
	// TODO create single source of truth for this namespace, generate into controller manifests
	// the namespace in which reddit control-plane resources reside
	InfraredSystemNamespace = "infrared-system"

	// ClusterComponentsNamespace is the namespace containing ClusterComponentSets and related resources.
	ClusterComponentsNamespace = "infrared-cluster-components"
)
