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
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	prokurav1alpha1 "github.com/JPaiv/prokura/api/v1alpha1"
)

// testEnvelope returns a minimal valid Envelope for tests.
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
						MatchLabels: map[string]string{"prokura.dev/agents": "allowed"},
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

var _ = Describe("Envelope Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-envelope"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}
		envelope := &prokurav1alpha1.Envelope{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Envelope")
			err := k8sClient.Get(ctx, typeNamespacedName, envelope)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, testEnvelope(resourceName))).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &prokurav1alpha1.Envelope{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Envelope")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &EnvelopeReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
