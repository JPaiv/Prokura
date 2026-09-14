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
	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	prokurav1alpha1 "github.com/JPaiv/prokura/api/v1alpha1"
)

// The tests bind the impersonation role to this ServiceAccount, which stands in
// for the proxy, and ask the API server what it may impersonate. That checks
// the rules the way the RBAC authorizer reads them, not the way they look.
const (
	testProxyNamespace = "prokura-system"
	testProxyName      = "test-proxy"
)

// rbacPropagation is how long a test waits for the authorizer to see an RBAC
// change. The authorizer reads RBAC from an informer, so a write is not
// visible to it the moment it returns.
const rbacPropagation = 10 * time.Second

// reconcileImpersonation runs one reconcile pass and requires it to succeed.
func reconcileImpersonation(ctx context.Context) {
	GinkgoHelper()
	reconciler := &ImpersonationReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	_, err := reconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: proxyImpersonationRole},
	})
	Expect(err).NotTo(HaveOccurred())
}

// getImpersonationRole reads the impersonation role back.
func getImpersonationRole(ctx context.Context) *rbacv1.ClusterRole {
	GinkgoHelper()
	role := &rbacv1.ClusterRole{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: proxyImpersonationRole}, role)).To(Succeed())
	return role
}

// proxyMay asks the API server whether the test proxy may impersonate with the
// given attributes.
func proxyMay(ctx context.Context, attributes authorizationv1.ResourceAttributes) (bool, error) {
	attributes.Verb = impersonateVerb
	review := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:               "system:serviceaccount:" + testProxyNamespace + ":" + testProxyName,
			ResourceAttributes: &attributes,
		},
	}
	if err := k8sClient.Create(ctx, review); err != nil {
		return false, err
	}
	return review.Status.Allowed, nil
}

// impersonatingUser, impersonatingGroup and impersonatingExtra are the
// attributes the API server authorizes an Impersonate-User,
// Impersonate-Group and Impersonate-Extra header with.
func impersonatingUser(name string) authorizationv1.ResourceAttributes {
	return authorizationv1.ResourceAttributes{Resource: usersResource, Name: name}
}

func impersonatingGroup(name string) authorizationv1.ResourceAttributes {
	return authorizationv1.ResourceAttributes{Resource: groupsResource, Name: name}
}

func impersonatingExtra(key, value string) authorizationv1.ResourceAttributes {
	return authorizationv1.ResourceAttributes{
		Group:       "authentication.k8s.io",
		Resource:    "userextras",
		Subresource: key,
		Name:        value,
	}
}

var _ = Describe("Impersonation Controller", func() {
	ctx := context.Background()

	BeforeEach(func() {
		binding := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "test-proxy-impersonation"},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     clusterRoleKind,
				Name:     proxyImpersonationRole,
			},
			Subjects: []rbacv1.Subject{{
				Kind:      rbacv1.ServiceAccountKind,
				Namespace: testProxyNamespace,
				Name:      testProxyName,
			}},
		}
		Expect(k8sClient.Create(ctx, binding)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, binding)).To(Succeed())
		})
	})

	It("lets the proxy impersonate the identity of every envelope", func() {
		first := testEnvelope("first-impersonated-envelope")
		second := testEnvelope("second-impersonated-envelope")
		createEnvelope(ctx, first)
		createEnvelope(ctx, second)

		reconcileImpersonation(ctx)

		for _, env := range []*prokurav1alpha1.Envelope{first, second} {
			Eventually(proxyMay).WithArguments(ctx, impersonatingUser(env.Identity())).
				WithTimeout(rbacPropagation).Should(BeTrue(), "impersonating %s", env.Identity())
		}

		By("letting it send the envelopes group and a run id with them")
		Expect(proxyMay(ctx, impersonatingGroup(prokurav1alpha1.EnvelopesGroup))).To(BeTrue())
		Expect(proxyMay(ctx, impersonatingExtra(prokurav1alpha1.RunExtraKey, "any-run-id"))).To(BeTrue())
	})

	It("does not let the proxy impersonate anything else", func() {
		env := testEnvelope("bounded-envelope")
		createEnvelope(ctx, env)

		reconcileImpersonation(ctx)
		Eventually(proxyMay).WithArguments(ctx, impersonatingUser(env.Identity())).
			WithTimeout(rbacPropagation).Should(BeTrue())

		Expect(proxyMay(ctx, impersonatingUser("system:admin"))).To(BeFalse())
		Expect(proxyMay(ctx, impersonatingUser("prokura:envelope:envelope-that-does-not-exist"))).To(BeFalse())
		Expect(proxyMay(ctx, impersonatingGroup("system:masters"))).To(BeFalse())
		Expect(proxyMay(ctx, impersonatingExtra("scopes", "view"))).To(BeFalse())
		Expect(proxyMay(ctx, authorizationv1.ResourceAttributes{
			Resource: "serviceaccounts", Namespace: "kube-system", Name: "default",
		})).To(BeFalse())
	})

	It("takes the identity away as soon as the envelope is being deleted", func() {
		env := testEnvelope("departing-envelope")
		createEnvelope(ctx, env)

		By("holding the envelope in deletion with its finalizer")
		reconcileEnvelope(ctx, env.Name)
		reconcileImpersonation(ctx)
		Eventually(proxyMay).WithArguments(ctx, impersonatingUser(env.Identity())).
			WithTimeout(rbacPropagation).Should(BeTrue())

		Expect(k8sClient.Delete(ctx, env)).To(Succeed())
		Expect(getEnvelope(ctx, env.Name).DeletionTimestamp).NotTo(BeNil())

		reconcileImpersonation(ctx)
		Eventually(proxyMay).WithArguments(ctx, impersonatingUser(env.Identity())).
			WithTimeout(rbacPropagation).Should(BeFalse())
	})

	It("grants nothing once the last envelope is gone", func() {
		var envelopes prokurav1alpha1.EnvelopeList
		Expect(k8sClient.List(ctx, &envelopes)).To(Succeed())
		Expect(envelopes.Items).To(BeEmpty(), "another test's envelope would still be in the role")

		env := testEnvelope("last-envelope")
		createEnvelope(ctx, env)
		reconcileImpersonation(ctx)
		Eventually(proxyMay).WithArguments(ctx, impersonatingUser(env.Identity())).
			WithTimeout(rbacPropagation).Should(BeTrue())

		deleteEnvelope(ctx, env.Name)
		reconcileImpersonation(ctx)

		By("leaving no rule at all, because a rule with no resourceNames allows every name")
		Expect(getImpersonationRole(ctx).Rules).To(BeEmpty())
		Eventually(proxyMay).WithArguments(ctx, impersonatingUser(env.Identity())).
			WithTimeout(rbacPropagation).Should(BeFalse())
		Expect(proxyMay(ctx, impersonatingUser("system:admin"))).To(BeFalse())
		Expect(proxyMay(ctx, impersonatingGroup(prokurav1alpha1.EnvelopesGroup))).To(BeFalse())
	})

	It("reverts a rule someone else added to the role", func() {
		env := testEnvelope("tampered-envelope")
		createEnvelope(ctx, env)
		reconcileImpersonation(ctx)

		role := getImpersonationRole(ctx)
		role.Rules = append(role.Rules, rbacv1.PolicyRule{
			APIGroups: []string{""},
			Resources: []string{usersResource},
			Verbs:     []string{impersonateVerb},
		})
		Expect(k8sClient.Update(ctx, role)).To(Succeed())

		reconcileImpersonation(ctx)

		Expect(getImpersonationRole(ctx).Rules).To(Equal(impersonationRules([]string{env.Identity()})))
	})
})

var _ = Describe("Impersonation watches", func() {
	ctx := context.Background()

	It("maps every event to the one reconcile request", func() {
		Expect(impersonationRequest(ctx, testEnvelope("any-envelope"))).To(Equal([]reconcile.Request{
			{NamespacedName: client.ObjectKey{Name: proxyImpersonationRole}},
		}))
	})

	It("selects only the impersonation role from the ClusterRoles", func() {
		Expect(isImpersonationRole(&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: proxyImpersonationRole},
		})).To(BeTrue())
		Expect(isImpersonationRole(&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: "prokura:envelope:proxy-impersonation"},
		})).To(BeFalse())
	})
})
