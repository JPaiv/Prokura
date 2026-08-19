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
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EnvelopeIdentityPrefix is the prefix of the impersonated Kubernetes user
// name the proxy uses for a run of an envelope: "prokura:envelope:<name>".
const EnvelopeIdentityPrefix = "prokura:envelope:"

// EnvelopesGroup is the Kubernetes group every impersonated envelope
// identity belongs to. Admission policies can match on it.
const EnvelopesGroup = "prokura:envelopes"

// Tier classifies the risk of the work an envelope allows.
// +kubebuilder:validation:Enum=read;reversible;irreversible
type Tier string

const (
	// TierRead allows only reads.
	TierRead Tier = "read"
	// TierReversible allows writes whose effect can be undone.
	TierReversible Tier = "reversible"
	// TierIrreversible allows writes whose effect cannot be undone.
	TierIrreversible Tier = "irreversible"
)

// PrincipalKind is the type of credential a principal presents.
// +kubebuilder:validation:Enum=ServiceAccount;OIDC;SPIFFE
type PrincipalKind string

const (
	// PrincipalServiceAccount is a Kubernetes ServiceAccount token,
	// validated with TokenReview.
	PrincipalServiceAccount PrincipalKind = "ServiceAccount"
	// PrincipalOIDC is an OIDC token, validated against the issuer's JWKS.
	PrincipalOIDC PrincipalKind = "OIDC"
	// PrincipalSPIFFE is a SPIFFE JWT-SVID, validated against a trust
	// bundle endpoint.
	PrincipalSPIFFE PrincipalKind = "SPIFFE"
)

// EnvelopePrincipal identifies a principal allowed to request a mandate for
// an envelope. Exactly the fields for the given kind must be set.
type EnvelopePrincipal struct {
	// Kind of credential the principal presents.
	// +required
	Kind PrincipalKind `json:"kind"`

	// Namespace of the ServiceAccount. Only for kind ServiceAccount.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Name of the ServiceAccount. Only for kind ServiceAccount.
	// +optional
	Name string `json:"name,omitempty"`

	// Issuer URL of the OIDC provider. Only for kind OIDC.
	// +optional
	Issuer string `json:"issuer,omitempty"`

	// Claims that must match, claim name to required value.
	// Only for kind OIDC.
	// +optional
	Claims map[string]string `json:"claims,omitempty"`

	// SPIFFEID is the SPIFFE ID the workload must present. A trailing
	// "/*" matches any path below the prefix. Only for kind SPIFFE.
	// +optional
	SPIFFEID string `json:"spiffeID,omitempty"`
}

// NamespaceScope selects the namespaces a run may act in.
// At least one of selector and names must be set.
type NamespaceScope struct {
	// Selector matches namespaces by label.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// Names lists namespaces explicitly.
	// +optional
	Names []string `json:"names,omitempty"`
}

// EnvelopeScope is what a run of the envelope may access.
type EnvelopeScope struct {
	// Namespaces a run may act in.
	// +required
	Namespaces NamespaceScope `json:"namespaces"`

	// Rules are the RBAC rules granted to the envelope identity in the
	// scoped namespaces. The validating webhook rejects rules that would
	// let a run escape its envelope (secrets, exec, impersonation, ...).
	// +required
	// +kubebuilder:validation:MinItems=1
	Rules []rbacv1.PolicyRule `json:"rules"`
}

// MandatePolicy bounds the mandates issued for an envelope.
type MandatePolicy struct {
	// TTL is how long a mandate is valid after issuance.
	// +required
	TTL metav1.Duration `json:"ttl"`

	// MaxCalls is the maximum number of API calls one run may make.
	// Zero means unlimited.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxCalls int32 `json:"maxCalls,omitempty"`
}

// ApprovalPolicy controls whether a mandate needs approval before it
// becomes active.
type ApprovalPolicy struct {
	// Required makes every mandate start in phase Pending until approved.
	// +optional
	Required bool `json:"required,omitempty"`
}

// EnvelopeSpec defines a kind of work a run may be authorized for.
type EnvelopeSpec struct {
	// Description of the work, for humans reviewing the envelope.
	// +optional
	Description string `json:"description,omitempty"`

	// Tier is the risk classification of the work.
	// +required
	Tier Tier `json:"tier"`

	// Principals allowed to request a mandate for this envelope.
	// +required
	// +kubebuilder:validation:MinItems=1
	Principals []EnvelopePrincipal `json:"principals"`

	// Scope is what a run may access.
	// +required
	Scope EnvelopeScope `json:"scope"`

	// Mandate bounds the mandates issued for this envelope.
	// +required
	Mandate MandatePolicy `json:"mandate"`

	// Approval controls whether mandates need approval.
	// +optional
	Approval ApprovalPolicy `json:"approval,omitempty"`
}

// EnvelopeStatus is the observed state of an Envelope.
type EnvelopeStatus struct {
	// Identity is the impersonated Kubernetes user name for runs of this
	// envelope: "prokura:envelope:<name>".
	// +optional
	Identity string `json:"identity,omitempty"`

	// ObservedGeneration is the spec generation the controller last
	// reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions describe the state of the envelope's RBAC. The "Ready"
	// condition is true when the ClusterRole and bindings for the
	// identity exist and match the spec.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Tier",type=string,JSONPath=`.spec.tier`
// +kubebuilder:printcolumn:name="Identity",type=string,JSONPath=`.status.identity`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Envelope is a cluster-scoped definition of a kind of work. It declares
// who may request it, what it may touch, and how its mandates are bounded.
type Envelope struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EnvelopeSpec   `json:"spec,omitempty"`
	Status EnvelopeStatus `json:"status,omitempty"`
}

// Identity returns the impersonated Kubernetes user name for runs of this
// envelope.
func (e *Envelope) Identity() string {
	return EnvelopeIdentityPrefix + e.Name
}

// +kubebuilder:object:root=true

// EnvelopeList contains a list of Envelope.
type EnvelopeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Envelope `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Envelope{}, &EnvelopeList{})
}
