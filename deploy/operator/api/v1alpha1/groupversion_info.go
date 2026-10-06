// Package v1alpha1 holds the Stampede operator API: StampedeCluster and
// StampedeRun in the stampede.dev group.
//
// +kubebuilder:object:generate=true
// +groupName=stampede.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is the API group and version of these types.
	GroupVersion = schema.GroupVersion{Group: "stampede.dev", Version: "v1alpha1"}

	// SchemeBuilder registers the types with a runtime scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion,
		&StampedeCluster{}, &StampedeClusterList{},
		&StampedeRun{}, &StampedeRunList{},
	)
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}
