/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	apimachineryv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:object:root=true

// ServiceExport declares that the Service with the same name and namespace
// as this export should be consumable from other clusters.
type ServiceExport struct {
	apimachineryv1.TypeMeta `json:",inline"`
	// +optional
	apimachineryv1.ObjectMeta `json:"metadata,omitempty"`
	// spec defines the desired state of ServiceExport
	// +optional
	Spec ServiceExportSpec `json:"spec,omitempty"`
	// status describes the current state of an exported service.
	// Service configuration comes from the Service that had the same
	// name and namespace as this ServiceExport.
	// Populated by the multi-cluster service implementation's controller.
	// +optional
	Status ServiceExportStatus `json:"status,omitempty"`
}

// ServiceExportSpec defines the desired state of ServiceExport
type ServiceExportSpec struct {
	// exportedPorts defines which ports of the service should be exported and what route types they should be used with.
	// If not specified, the controller will use the port from the annotation "application-networking.k8s.aws/port"
	// and create HTTP target groups for backward compatibility.
	// +optional
	ExportedPorts []ExportedPort `json:"exportedPorts,omitempty"`
}

// ExportedPort defines a port to be exported and the route type it should be used with
type ExportedPort struct {
	// port is the port number to export
	Port int32 `json:"port"`
	// routeType is the type of route this port should be used with
	// Valid values are "HTTP", "GRPC", "TLS"
	// +kubebuilder:validation:Enum=HTTP;GRPC;TLS
	RouteType string `json:"routeType"`
}

// ServiceExportStatus contains the current status of an export.
type ServiceExportStatus struct {
	// +optional
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +listType=map
	// +listMapKey=type
	Conditions []ServiceExportCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// ServiceExportConditionType identifies a specific condition.
type ServiceExportConditionType string

const (
	// ServiceExportValid means that the service referenced by this
	// service export has been recognized as valid by a controller.
	// This will be false if the service is found to be unexportable
	// (ExternalName, not found).
	ServiceExportValid ServiceExportConditionType = "Valid"
	// ServiceExportConflict means that there is a conflict between two
	// exports for the same Service. When "True", the condition message
	// should contain enough information to diagnose the conflict:
	// field(s) under contention, which cluster won, and why.
	// Users should not expect detailed per-cluster information in the
	// conflict message.
	ServiceExportConflict ServiceExportConditionType = "Conflict"
)

// ServiceExportCondition contains details for the current condition of this
// service export.
//
// Once [KEP-1623](https://github.com/kubernetes/enhancements/tree/master/keps/sig-api-machinery/1623-standardize-conditions) is
// implemented, this will be replaced by metav1.Condition.
type ServiceExportCondition struct {
	Type ServiceExportConditionType `json:"type"`
	// Status is one of {"True", "False", "Unknown"}
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status corev1.ConditionStatus `json:"status"`
	// +optional
	LastTransitionTime *apimachineryv1.Time `json:"lastTransitionTime,omitempty"`
	// +optional
	Reason *string `json:"reason,omitempty"`
	// +optional
	Message *string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceExportList represents a list of endpoint slices
type ServiceExportList struct {
	apimachineryv1.TypeMeta `json:",inline"`
	// Standard list metadata.
	// +optional
	apimachineryv1.ListMeta `json:"metadata,omitempty"`
	// List of endpoint slices
	// +listType=set
	Items []ServiceExport `json:"items"`
}
