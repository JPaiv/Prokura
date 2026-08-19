/*
Copyright 2026 Juho Päivärinta.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MandatePhase is the lifecycle phase of a mandate.
// +kubebuilder:validation:Enum=Pending;Active;Revoked;Expired
type MandatePhase string

const (
	// MandatePending means the mandate awaits approval; its token is not
	// yet usable.
	MandatePending MandatePhase = "Pending"
	// MandateActive means the token is valid.
	MandateActive MandatePhase = "Active"
	// MandateRevoked means the mandate was revoked before expiry; its
	// token is invalid.
	MandateRevoked MandatePhase = "Revoked"
	// MandateExpired means the mandate reached its expiry time.
	MandateExpired MandatePhase = "Expired"
)

// MandateScope is the scope the principal requested for one run. It must be
// within the envelope's scope.
type MandateScope struct {
	// Namespaces the run is limited to. Must be a subset of the
	// namespaces the envelope allows.
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`

	// Ticket is an optional reference to why the run was started, for
	// example an issue or incident id. Recorded in the mandate JWT as
	// the prokura.dev/ticket claim.
	// +optional
	Ticket string `json:"ticket,omitempty"`
}

// MandateSpec describes one run of one envelope.
type MandateSpec struct {
	// EnvelopeName is the envelope this mandate is a run of.
	// +required
	// +kubebuilder:validation:MinLength=1
	EnvelopeName string `json:"envelopeName"`

	// Principal is the authenticated identity that requested the
	// mandate, as recorded by the token service. For example
	// "system:serviceaccount:demo:demo-agent" or a SPIFFE ID.
	// +required
	// +kubebuilder:validation:MinLength=1
	Principal string `json:"principal"`

	// Scope the principal requested for this run.
	// +optional
	Scope MandateScope `json:"scope,omitempty"`
}

// MandateStatus is the observed state of a mandate.
type MandateStatus struct {
	// Phase of the mandate lifecycle.
	// +optional
	Phase MandatePhase `json:"phase,omitempty"`

	// IssuedAt is when the token service issued the mandate JWT.
	// +optional
	IssuedAt *metav1.Time `json:"issuedAt,omitempty"`

	// ExpiresAt is when the mandate JWT expires.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// Calls is the number of API calls the proxy has forwarded for this
	// run.
	// +optional
	Calls int32 `json:"calls,omitempty"`

	// Conditions describe the state of the mandate.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Envelope",type=string,JSONPath=`.spec.envelopeName`
// +kubebuilder:printcolumn:name="Principal",type=string,JSONPath=`.spec.principal`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.status.expiresAt`

// Mandate is one run of one envelope. The token service creates one for
// every token exchange; its name is the run id (the JWT's jti claim).
// Revoking a mandate invalidates its token immediately.
type Mandate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MandateSpec   `json:"spec,omitempty"`
	Status MandateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MandateList contains a list of Mandate.
type MandateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Mandate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Mandate{}, &MandateList{})
}
