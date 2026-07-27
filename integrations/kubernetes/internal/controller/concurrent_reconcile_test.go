/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
)

// Reconciling many Pacto CRs created concurrently must be race-free (run under
// -race) and every CR must independently converge to a terminal ContractValid
// condition — no cross-CR interference in the shared reconciler.
var _ = Describe("Concurrent reconciliation", func() {
	It("converges every CR when many are created at once", func() {
		const n = 12
		names := make([]string, n)

		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				pacto := &pactov1alpha1.Pacto{
					ObjectMeta: metav1.ObjectMeta{GenerateName: "concurrent-", Namespace: "default"},
					Spec: pactov1alpha1.PactoSpec{
						ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
						Target:      pactov1alpha1.TargetRef{ServiceName: "nonexistent-svc"},
					},
				}
				Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
				names[i] = pacto.Name
			}()
		}
		wg.Wait()

		for _, name := range names {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
				g.Expect(meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)).NotTo(BeNil())
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		}

		for _, name := range names {
			pacto := &pactov1alpha1.Pacto{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pacto)).To(Succeed())
		}
	})
})
