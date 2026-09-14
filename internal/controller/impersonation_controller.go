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

package controller

import (
	"context"
	"fmt"
	"sort"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	prokurav1alpha1 "github.com/JPaiv/prokura/api/v1alpha1"
)

const (
	// proxyImpersonationRole is the ClusterRole holding everything the proxy
	// may impersonate. config/rbac binds it to the proxy ServiceAccount by this
	// name. The controller writes its rules; nothing else should.
	proxyImpersonationRole = "prokura:proxy-impersonation"

	// impersonationFieldManager owns the rules of the impersonation role. The
	// rules are an atomic list, so applying them replaces the whole list,
	// including a rule someone else added to it.
	impersonationFieldManager = "prokura-impersonation-controller"

	// impersonateVerb, usersResource and groupsResource are what the API
	// server authorizes the Impersonate-User and Impersonate-Group headers
	// against.
	impersonateVerb = "impersonate"
	usersResource   = "users"
	groupsResource  = "groups"
)

// ImpersonationReconciler keeps the proxy's impersonation rights narrowed to
// the envelope identities that exist.
type ImpersonationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=prokura.dev,resources=envelopes,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=escalate

// Reconcile writes the rules of the impersonation role from the envelopes that
// exist. There is one such role for the whole cluster, so the request carries
// nothing the reconcile needs.
func (r *ImpersonationReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	var envelopes prokurav1alpha1.EnvelopeList
	if err := r.List(ctx, &envelopes); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing envelopes: %w", err)
	}

	identities := make([]string, 0, len(envelopes.Items))
	for i := range envelopes.Items {
		env := &envelopes.Items[i]
		// An envelope that is being deleted loses its identity at once, not
		// when its finalizer has finished removing the RoleBindings. A run of
		// an envelope on its way out should not be able to start new work.
		if !env.DeletionTimestamp.IsZero() {
			continue
		}
		identities = append(identities, env.Identity())
	}
	sort.Strings(identities)

	role := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rbacv1.SchemeGroupVersion.String(),
			Kind:       clusterRoleKind,
		},
		ObjectMeta: metav1.ObjectMeta{Name: proxyImpersonationRole},
		Rules:      impersonationRules(identities),
	}
	if err := r.Patch(ctx, role, client.Apply,
		client.FieldOwner(impersonationFieldManager), client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("applying ClusterRole %s: %w", proxyImpersonationRole, err)
	}

	log.FromContext(ctx).Info("reconciled the proxy's impersonation rights", "identities", len(identities))
	return ctrl.Result{}, nil
}

// impersonationRules returns the rules that let the proxy impersonate the given
// envelope identities, the envelopes group, and the run extra, and nothing else.
//
// An empty resourceNames matches every name. No identities therefore has to
// produce no rules at all, never a users rule with an empty list: that rule
// would let the proxy impersonate any user in the cluster.
func impersonationRules(identities []string) []rbacv1.PolicyRule {
	if len(identities) == 0 {
		return []rbacv1.PolicyRule{}
	}
	return []rbacv1.PolicyRule{
		{
			APIGroups:     []string{""},
			Resources:     []string{usersResource},
			Verbs:         []string{impersonateVerb},
			ResourceNames: identities,
		},
		{
			APIGroups:     []string{""},
			Resources:     []string{groupsResource},
			Verbs:         []string{impersonateVerb},
			ResourceNames: []string{prokurav1alpha1.EnvelopesGroup},
		},
		// The run id is new for every mandate, so the values of the extra
		// cannot be listed. The key is still restricted: the proxy cannot
		// impersonate any other extra.
		{
			APIGroups: []string{"authentication.k8s.io"},
			Resources: []string{"userextras/" + prokurav1alpha1.RunExtraKey},
			Verbs:     []string{impersonateVerb},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
//
// With no envelopes and no impersonation role there are no events, and nothing
// to do either: the proxy's binding refers to a role that does not exist and
// grants nothing. A role left behind with stale rules does produce an event, on
// the initial list of the ClusterRole watch.
func (r *ImpersonationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Watches(&prokurav1alpha1.Envelope{}, handler.EnqueueRequestsFromMapFunc(impersonationRequest)).
		Watches(&rbacv1.ClusterRole{}, handler.EnqueueRequestsFromMapFunc(impersonationRequest),
			builder.WithPredicates(predicate.NewPredicateFuncs(isImpersonationRole))).
		Named("impersonation").
		Complete(r)
}

// impersonationRequest maps every watched event to the one reconcile request.
// The rules depend on all envelopes at once, so there is nothing to reconcile
// per object.
func impersonationRequest(_ context.Context, _ client.Object) []ctrl.Request {
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: proxyImpersonationRole}}}
}

// isImpersonationRole selects the impersonation role from all ClusterRoles, so
// that a change to it by anyone else is reverted.
func isImpersonationRole(obj client.Object) bool {
	return obj.GetName() == proxyImpersonationRole
}
