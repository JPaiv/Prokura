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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	prokurav1alpha1 "github.com/JPaiv/prokura/api/v1alpha1"
)

// testScopeLabel is how a test envelope selects its namespaces. Keying it on
// the envelope name keeps one test's namespaces out of another's scope, since
// namespaces outlive a spec in envtest.
const testScopeLabel = "prokura.dev/test-scope"

// testEnvelope returns a minimal valid Envelope for tests. It selects the
// namespaces labelled for it by name.
func testEnvelope(name string) *prokurav1alpha1.Envelope {
	return &prokurav1alpha1.Envelope{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: prokurav1alpha1.EnvelopeSpec{
			Description: "Roll the pods of a Deployment.",
			Tier:        prokurav1alpha1.TierReversible,
			Principals: []prokurav1alpha1.EnvelopePrincipal{
				{
					Kind:      prokurav1alpha1.PrincipalServiceAccount,
					Namespace: "demo",
					Name:      "demo-agent",
				},
			},
			Scope: prokurav1alpha1.EnvelopeScope{
				Namespaces: prokurav1alpha1.NamespaceScope{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{testScopeLabel: name},
					},
				},
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups: []string{"apps"},
						Resources: []string{"deployments"},
						Verbs:     []string{"get", "list", "watch", "patch"},
					},
				},
			},
			Mandate: prokurav1alpha1.MandatePolicy{
				TTL:      metav1.Duration{Duration: 10 * time.Minute},
				MaxCalls: 50,
			},
		},
	}
}

// createEnvelope creates the envelope and removes it, and the RBAC it
// generated, when the test ends.
func createEnvelope(ctx context.Context, env *prokurav1alpha1.Envelope) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, env)).To(Succeed())
	DeferCleanup(func() {
		deleteEnvelope(ctx, env.Name)
	})
}

// deleteEnvelope deletes the envelope and reconciles until the finalizer has
// released it. Deleting an already-deleted envelope is not an error.
func deleteEnvelope(ctx context.Context, name string) {
	GinkgoHelper()
	env := &prokurav1alpha1.Envelope{ObjectMeta: metav1.ObjectMeta{Name: name}}
	err := k8sClient.Delete(ctx, env)
	if errors.IsNotFound(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred())
	reconcileEnvelope(ctx, name)
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, env)).
		To(MatchError(ContainSubstring("not found")))
}

// createNamespace creates a namespace with the given labels. Namespaces are
// never deleted: envtest has no controller to finish the deletion, so a
// deleted namespace would stay Terminating for the rest of the suite.
func createNamespace(ctx context.Context, name string, labels map[string]string) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
	})).To(Succeed())
}

// relabelNamespace replaces the labels of an existing namespace.
func relabelNamespace(ctx context.Context, name string, labels map[string]string) {
	GinkgoHelper()
	ns := &corev1.Namespace{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, ns)).To(Succeed())
	ns.Labels = labels
	Expect(k8sClient.Update(ctx, ns)).To(Succeed())
}

// reconcileEnvelope runs one reconcile pass and requires it to succeed.
func reconcileEnvelope(ctx context.Context, name string) {
	GinkgoHelper()
	reconciler := &EnvelopeReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	_, err := reconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: name},
	})
	Expect(err).NotTo(HaveOccurred())
}

// getEnvelope reads the envelope back.
func getEnvelope(ctx context.Context, name string) *prokurav1alpha1.Envelope {
	GinkgoHelper()
	env := &prokurav1alpha1.Envelope{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, env)).To(Succeed())
	return env
}

// roleBindingIn reads the envelope's RoleBinding in a namespace, or nil when
// there is none.
func roleBindingIn(ctx context.Context, namespace, identity string) *rbacv1.RoleBinding {
	GinkgoHelper()
	binding := &rbacv1.RoleBinding{}
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: identity}, binding)
	if errors.IsNotFound(err) {
		return nil
	}
	Expect(err).NotTo(HaveOccurred())
	return binding
}

var _ = Describe("Envelope Controller", func() {
	ctx := context.Background()

	It("creates a ClusterRole holding the envelope's rules", func() {
		env := testEnvelope("clusterrole-envelope")
		createEnvelope(ctx, env)

		reconcileEnvelope(ctx, env.Name)

		role := &rbacv1.ClusterRole{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: env.Identity()}, role)).To(Succeed())
		Expect(role.Rules).To(Equal(env.Spec.Scope.Rules))
		Expect(role.Labels).To(HaveKeyWithValue(envelopeLabel, env.Name))

		By("owning it, so that it cannot outlive the envelope")
		Expect(role.OwnerReferences).To(HaveLen(1))
		Expect(role.OwnerReferences[0].Name).To(Equal(env.Name))
		Expect(role.OwnerReferences[0].Kind).To(Equal("Envelope"))

		By("reporting the identity and readiness")
		reconciled := getEnvelope(ctx, env.Name)
		Expect(reconciled.Status.Identity).To(Equal("prokura:envelope:clusterrole-envelope"))
		Expect(reconciled.Status.ObservedGeneration).To(Equal(reconciled.Generation))
		Expect(meta.IsStatusConditionTrue(reconciled.Status.Conditions, prokurav1alpha1.EnvelopeReady)).
			To(BeTrue())
	})

	It("binds the identity only in the namespaces the scope selects", func() {
		env := testEnvelope("selected-envelope")
		createNamespace(ctx, "selected-match", map[string]string{testScopeLabel: env.Name})
		createNamespace(ctx, "selected-other", map[string]string{testScopeLabel: "someone-else"})
		createEnvelope(ctx, env)

		reconcileEnvelope(ctx, env.Name)

		binding := roleBindingIn(ctx, "selected-match", env.Identity())
		Expect(binding).NotTo(BeNil())
		Expect(binding.RoleRef).To(Equal(rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     clusterRoleKind,
			Name:     env.Identity(),
		}))
		Expect(binding.Subjects).To(Equal([]rbacv1.Subject{{
			APIGroup: rbacv1.GroupName,
			Kind:     rbacv1.UserKind,
			Name:     env.Identity(),
		}}))

		By("leaving a namespace the selector does not match alone")
		Expect(roleBindingIn(ctx, "selected-other", env.Identity())).To(BeNil())

		By("not owning the binding, which the garbage collector would delete")
		Expect(binding.OwnerReferences).To(BeEmpty())
	})

	It("binds in a namespace that starts matching later", func() {
		env := testEnvelope("late-envelope")
		createNamespace(ctx, "late-namespace", nil)
		createEnvelope(ctx, env)

		reconcileEnvelope(ctx, env.Name)
		Expect(roleBindingIn(ctx, "late-namespace", env.Identity())).To(BeNil())

		relabelNamespace(ctx, "late-namespace", map[string]string{testScopeLabel: env.Name})
		reconcileEnvelope(ctx, env.Name)

		Expect(roleBindingIn(ctx, "late-namespace", env.Identity())).NotTo(BeNil())
	})

	It("removes the binding from a namespace that stops matching", func() {
		env := testEnvelope("pruned-envelope")
		createNamespace(ctx, "pruned-namespace", map[string]string{testScopeLabel: env.Name})
		createEnvelope(ctx, env)

		reconcileEnvelope(ctx, env.Name)
		Expect(roleBindingIn(ctx, "pruned-namespace", env.Identity())).NotTo(BeNil())

		relabelNamespace(ctx, "pruned-namespace", nil)
		reconcileEnvelope(ctx, env.Name)

		Expect(roleBindingIn(ctx, "pruned-namespace", env.Identity())).To(BeNil())
	})

	It("binds in the namespaces the scope names", func() {
		createNamespace(ctx, "named-namespace", nil)

		env := testEnvelope("named-envelope")
		env.Spec.Scope.Namespaces = prokurav1alpha1.NamespaceScope{
			Names: []string{"named-namespace", "namespace-that-does-not-exist"},
		}
		createEnvelope(ctx, env)

		reconcileEnvelope(ctx, env.Name)

		Expect(roleBindingIn(ctx, "named-namespace", env.Identity())).NotTo(BeNil())

		By("counting only the namespaces that exist as bound")
		ready := meta.FindStatusCondition(
			getEnvelope(ctx, env.Name).Status.Conditions, prokurav1alpha1.EnvelopeReady)
		Expect(ready.Message).To(ContainSubstring("bound in 1 namespaces"))
	})

	It("removes the RBAC when the envelope is deleted", func() {
		env := testEnvelope("deleted-envelope")
		createNamespace(ctx, "deleted-namespace", map[string]string{testScopeLabel: env.Name})
		Expect(k8sClient.Create(ctx, env)).To(Succeed())

		reconcileEnvelope(ctx, env.Name)
		Expect(roleBindingIn(ctx, "deleted-namespace", env.Identity())).NotTo(BeNil())
		Expect(getEnvelope(ctx, env.Name).Finalizers).To(ContainElement(envelopeFinalizer))

		deleteEnvelope(ctx, env.Name)

		Expect(roleBindingIn(ctx, "deleted-namespace", env.Identity())).To(BeNil())
		err := k8sClient.Get(ctx, types.NamespacedName{Name: env.Identity()}, &rbacv1.ClusterRole{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "the ClusterRole should be gone")
	})

	It("reports a scope that selects nothing instead of retrying it", func() {
		env := testEnvelope("empty-scope-envelope")
		env.Spec.Scope.Namespaces = prokurav1alpha1.NamespaceScope{}
		createEnvelope(ctx, env)

		By("not returning an error, which would only be retried to the same end")
		reconcileEnvelope(ctx, env.Name)

		ready := meta.FindStatusCondition(
			getEnvelope(ctx, env.Name).Status.Conditions, prokurav1alpha1.EnvelopeReady)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal("InvalidEnvelope"))
		Expect(ready.Message).To(ContainSubstring("neither selector nor names"))

		By("creating no RBAC for it")
		err := k8sClient.Get(ctx, types.NamespacedName{Name: env.Identity()}, &rbacv1.ClusterRole{})
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("ignores an envelope that no longer exists", func() {
		reconciler := &EnvelopeReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "never-created"},
		})
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("Envelope watches", func() {
	ctx := context.Background()

	It("maps a generated RoleBinding back to its envelope", func() {
		binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{envelopeLabel: "some-envelope"},
		}}
		Expect(envelopeOfRoleBinding(ctx, binding)).To(Equal([]reconcile.Request{
			{NamespacedName: client.ObjectKey{Name: "some-envelope"}},
		}))
	})

	It("ignores a RoleBinding it did not generate", func() {
		Expect(envelopeOfRoleBinding(ctx, &rbacv1.RoleBinding{})).To(BeEmpty())
	})
})
