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
	"errors"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	prokurav1alpha1 "github.com/JPaiv/prokura/api/v1alpha1"
)

const (
	// envelopeFinalizer keeps an Envelope around until its RoleBindings are
	// gone. RoleBindings are namespaced and an Envelope is cluster-scoped, so
	// an owner reference cannot connect the two and the API server's garbage
	// collector will not clean them up for us.
	envelopeFinalizer = "prokura.dev/envelope-rbac"

	// envelopeLabel is set on every RBAC object the controller generates. It
	// is what makes a RoleBinding findable again, both to prune it and to map
	// it back to its Envelope.
	envelopeLabel = "prokura.dev/envelope"

	// envelopeFieldManager owns the fields the controller applies. A field it
	// stops setting, such as a rule removed from the spec, is removed from the
	// object by server-side apply.
	envelopeFieldManager = "prokura-envelope-controller"

	clusterRoleKind = "ClusterRole"
)

// errInvalidEnvelope marks an Envelope the controller cannot act on. Retrying
// does not help: the spec has to change. The validating webhook will reject
// these before they are ever stored, once it exists.
var errInvalidEnvelope = errors.New("invalid envelope")

// EnvelopeReconciler reconciles a Envelope object
type EnvelopeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=prokura.dev,resources=envelopes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=prokura.dev,resources=envelopes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=prokura.dev,resources=envelopes/finalizers,verbs=update
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=bind;escalate
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile brings the RBAC for an envelope identity in line with the
// Envelope: one ClusterRole holding the envelope's rules, and one RoleBinding
// granting it to the identity in every namespace the scope selects.
func (r *EnvelopeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var envelope prokurav1alpha1.Envelope
	if err := r.Get(ctx, req.NamespacedName, &envelope); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !envelope.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finalize(ctx, &envelope)
	}

	if err := r.ensureFinalizer(ctx, &envelope); err != nil {
		return ctrl.Result{}, err
	}

	namespaces, reconcileErr := r.reconcileRBAC(ctx, &envelope)
	if err := r.updateStatus(ctx, &envelope, namespaces, reconcileErr); err != nil {
		return ctrl.Result{}, errors.Join(reconcileErr, err)
	}

	if errors.Is(reconcileErr, errInvalidEnvelope) {
		// Requeueing would only fail the same way. The next change to the
		// spec reconciles again, and the status says what is wrong.
		log.FromContext(ctx).Info("envelope cannot be reconciled", "reason", reconcileErr.Error())
		return ctrl.Result{}, nil
	}
	if reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	log.FromContext(ctx).Info("reconciled envelope",
		"identity", envelope.Identity(), "namespaces", len(namespaces))
	return ctrl.Result{}, nil
}

// reconcileRBAC applies the ClusterRole and the RoleBindings, and removes the
// bindings in namespaces the scope no longer selects. It returns the
// namespaces the identity is bound in.
func (r *EnvelopeReconciler) reconcileRBAC(
	ctx context.Context, env *prokurav1alpha1.Envelope,
) ([]string, error) {
	if len(env.Name) > validation.LabelValueMaxLength {
		return nil, fmt.Errorf("%w: the name is %d characters, but it has to fit the %s label, which allows %d",
			errInvalidEnvelope, len(env.Name), envelopeLabel, validation.LabelValueMaxLength)
	}

	namespaces, err := r.selectedNamespaces(ctx, env)
	if err != nil {
		return nil, err
	}
	if err := r.applyClusterRole(ctx, env); err != nil {
		return nil, err
	}
	if err := r.applyRoleBindings(ctx, env, namespaces); err != nil {
		return namespaces, err
	}
	return namespaces, r.pruneRoleBindings(ctx, env, namespaces)
}

// selectedNamespaces returns the existing namespaces the scope selects, by
// label selector or by name. Listing once and filtering keeps a namespace that
// is named but does not exist from being an error: if it is created later, the
// namespace watch reconciles the envelope again.
func (r *EnvelopeReconciler) selectedNamespaces(
	ctx context.Context, env *prokurav1alpha1.Envelope,
) ([]string, error) {
	scope := env.Spec.Scope.Namespaces
	if scope.Selector == nil && len(scope.Names) == 0 {
		return nil, fmt.Errorf("%w: spec.scope.namespaces sets neither selector nor names, "+
			"so the envelope selects no namespace at all", errInvalidEnvelope)
	}

	// A nil selector must not become labels.Everything(). An empty but
	// non-nil selector still does, which is what a label selector means in
	// Kubernetes; the validating webhook is where that should be refused.
	selector := labels.Nothing()
	if scope.Selector != nil {
		var err error
		selector, err = metav1.LabelSelectorAsSelector(scope.Selector)
		if err != nil {
			return nil, fmt.Errorf("%w: spec.scope.namespaces.selector: %s", errInvalidEnvelope, err)
		}
	}
	named := sets.New(scope.Names...)

	var list corev1.NamespaceList
	if err := r.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("listing namespaces: %w", err)
	}

	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		ns := &list.Items[i]
		// A namespace that is going away will not accept a RoleBinding.
		if !ns.DeletionTimestamp.IsZero() {
			continue
		}
		if named.Has(ns.Name) || selector.Matches(labels.Set(ns.Labels)) {
			names = append(names, ns.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// applyClusterRole writes the envelope's rules to the ClusterRole for its
// identity.
func (r *EnvelopeReconciler) applyClusterRole(ctx context.Context, env *prokurav1alpha1.Envelope) error {
	role := &rbacv1.ClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: rbacv1.SchemeGroupVersion.String(),
			Kind:       clusterRoleKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   env.Identity(),
			Labels: map[string]string{envelopeLabel: env.Name},
			// Both objects are cluster-scoped, so this owner reference is
			// valid and the API server collects the ClusterRole on its own.
			// The finalizer deletes it too, so that the identity loses its
			// rules at the same moment it loses its bindings.
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(env, prokurav1alpha1.GroupVersion.WithKind("Envelope")),
			},
		},
		Rules: env.Spec.Scope.Rules,
	}
	if err := r.apply(ctx, role); err != nil {
		return fmt.Errorf("applying ClusterRole %s: %w", env.Identity(), err)
	}
	return nil
}

// applyRoleBindings grants the ClusterRole to the envelope identity in each of
// the given namespaces.
func (r *EnvelopeReconciler) applyRoleBindings(
	ctx context.Context, env *prokurav1alpha1.Envelope, namespaces []string,
) error {
	for _, ns := range namespaces {
		binding := &rbacv1.RoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: rbacv1.SchemeGroupVersion.String(),
				Kind:       "RoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      env.Identity(),
				Namespace: ns,
				Labels:    map[string]string{envelopeLabel: env.Name},
				// Deliberately no owner reference: a namespaced object cannot
				// be owned by a cluster-scoped one, and the garbage collector
				// treats such a reference as dangling and deletes the object.
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     clusterRoleKind,
				Name:     env.Identity(),
			},
			// The proxy impersonates this user. The prokura:envelopes group it
			// also sends carries no permissions; it is there for admission
			// policies to match on.
			Subjects: []rbacv1.Subject{{
				APIGroup: rbacv1.GroupName,
				Kind:     rbacv1.UserKind,
				Name:     env.Identity(),
			}},
		}
		if err := r.apply(ctx, binding); err != nil {
			return fmt.Errorf("applying RoleBinding in namespace %s: %w", ns, err)
		}
	}
	return nil
}

// pruneRoleBindings deletes the envelope's RoleBindings outside keep. Passing
// no namespaces deletes all of them.
func (r *EnvelopeReconciler) pruneRoleBindings(
	ctx context.Context, env *prokurav1alpha1.Envelope, keep []string,
) error {
	var bindings rbacv1.RoleBindingList
	if err := r.List(ctx, &bindings, client.MatchingLabels{envelopeLabel: env.Name}); err != nil {
		return fmt.Errorf("listing RoleBindings for %s: %w", env.Name, err)
	}

	wanted := sets.New(keep...)
	for i := range bindings.Items {
		binding := &bindings.Items[i]
		if wanted.Has(binding.Namespace) {
			continue
		}
		if err := r.Delete(ctx, binding); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting RoleBinding in namespace %s: %w", binding.Namespace, err)
		}
	}
	return nil
}

// apply writes obj with server-side apply, taking ownership of the fields it
// sets.
func (r *EnvelopeReconciler) apply(ctx context.Context, obj client.Object) error {
	return r.Patch(ctx, obj, client.Apply,
		client.FieldOwner(envelopeFieldManager), client.ForceOwnership)
}

// ensureFinalizer adds the finalizer to an envelope that does not have it yet.
func (r *EnvelopeReconciler) ensureFinalizer(ctx context.Context, env *prokurav1alpha1.Envelope) error {
	if controllerutil.ContainsFinalizer(env, envelopeFinalizer) {
		return nil
	}
	patch := client.MergeFrom(env.DeepCopy())
	controllerutil.AddFinalizer(env, envelopeFinalizer)
	if err := r.Patch(ctx, env, patch); err != nil {
		return fmt.Errorf("adding the finalizer to %s: %w", env.Name, err)
	}
	return nil
}

// finalize removes the RBAC for a deleted envelope and then releases it.
func (r *EnvelopeReconciler) finalize(ctx context.Context, env *prokurav1alpha1.Envelope) error {
	if !controllerutil.ContainsFinalizer(env, envelopeFinalizer) {
		return nil
	}

	if err := r.pruneRoleBindings(ctx, env, nil); err != nil {
		return err
	}
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: env.Identity()}}
	if err := r.Delete(ctx, role); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting ClusterRole %s: %w", env.Identity(), err)
	}

	patch := client.MergeFrom(env.DeepCopy())
	controllerutil.RemoveFinalizer(env, envelopeFinalizer)
	if err := r.Patch(ctx, env, patch); err != nil {
		return fmt.Errorf("removing the finalizer from %s: %w", env.Name, err)
	}

	log.FromContext(ctx).Info("removed the RBAC for a deleted envelope", "identity", env.Identity())
	return nil
}

// updateStatus records the identity and what the last reconcile did.
func (r *EnvelopeReconciler) updateStatus(
	ctx context.Context, env *prokurav1alpha1.Envelope, namespaces []string, reconcileErr error,
) error {
	patch := client.MergeFrom(env.DeepCopy())

	env.Status.Identity = env.Identity()
	env.Status.ObservedGeneration = env.Generation

	ready := metav1.Condition{
		Type:               prokurav1alpha1.EnvelopeReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: env.Generation,
		Reason:             "RBACReconciled",
		Message: fmt.Sprintf("%s is bound in %d namespaces.",
			env.Identity(), len(namespaces)),
	}
	switch {
	case errors.Is(reconcileErr, errInvalidEnvelope):
		ready.Status = metav1.ConditionFalse
		ready.Reason = "InvalidEnvelope"
		ready.Message = reconcileErr.Error()
	case reconcileErr != nil:
		ready.Status = metav1.ConditionFalse
		ready.Reason = "ReconcileFailed"
		ready.Message = reconcileErr.Error()
	}
	meta.SetStatusCondition(&env.Status.Conditions, ready)

	if err := r.Status().Patch(ctx, env, patch); err != nil {
		return fmt.Errorf("updating the status of %s: %w", env.Name, err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EnvelopeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&prokurav1alpha1.Envelope{}).
		Owns(&rbacv1.ClusterRole{}).
		Watches(&rbacv1.RoleBinding{}, handler.EnqueueRequestsFromMapFunc(envelopeOfRoleBinding)).
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(r.allEnvelopes)).
		Named("envelope").
		Complete(r)
}

// envelopeOfRoleBinding maps a generated RoleBinding back to its envelope. The
// binding carries no owner reference, so the label is the only link.
func envelopeOfRoleBinding(_ context.Context, obj client.Object) []ctrl.Request {
	name, ok := obj.GetLabels()[envelopeLabel]
	if !ok {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: name}}}
}

// allEnvelopes enqueues every envelope when a namespace changes. Evaluating
// the selectors here would enqueue fewer, but a namespace that has just lost
// the label an envelope selects no longer matches that envelope, and it is
// exactly the envelope whose RoleBinding has to be removed.
func (r *EnvelopeReconciler) allEnvelopes(ctx context.Context, _ client.Object) []ctrl.Request {
	var envelopes prokurav1alpha1.EnvelopeList
	if err := r.List(ctx, &envelopes); err != nil {
		log.FromContext(ctx).Error(err, "listing envelopes after a namespace changed")
		return nil
	}
	requests := make([]ctrl.Request, 0, len(envelopes.Items))
	for i := range envelopes.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: client.ObjectKey{Name: envelopes.Items[i].Name},
		})
	}
	return requests
}
