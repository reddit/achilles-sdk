package meta

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// SetControllerRef sets an owner reference on the given object that references the owner object with controller flag set to true
func SetControllerRef(o client.Object, owner client.Object, scheme *runtime.Scheme) error {
	// we set a controller reference to ensure that controller-runtime queues events when using `.Owns`
	if err := ctrl.SetControllerReference(owner, o, scheme); err != nil {
		gvkForObject := MustGVKForObject(o, scheme)
		return fmt.Errorf("setting controller reference on %s %q: %w", gvkForObject.Kind, client.ObjectKeyFromObject(o), err)
	}
	return nil
}

// SetOwnerRef appends an owner reference on the given object that references the owner object with controller flag set to false
func SetOwnerRef(o client.Object, owner client.Object, scheme *runtime.Scheme) error {
	// we set an owner reference to ensure that controller-runtime queues events when using `.Owns`
	if err := controllerutil.SetOwnerReference(owner, o, scheme); err != nil {
		gvkForObject := MustGVKForObject(o, scheme)
		return fmt.Errorf("setting owner reference on %s %q: %w", gvkForObject.Kind, client.ObjectKeyFromObject(o), err)
	}
	return nil
}
