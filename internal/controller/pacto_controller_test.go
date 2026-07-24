/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
)

const validContract = `
pactoVersion: "1.0"
service:
  name: test-svc
  version: 1.0.0
  owner:
    team: team-a
state:
  type: stateless
  persistence:
    durability: ephemeral
workload: service
interfaces:
  - name: http-api
    type: openapi
    ref: spec.yaml
`

const (
	timeout  = 10 * time.Second
	interval = 250 * time.Millisecond
)

var _ = Describe("Pacto Controller", func() {

	Context("When no contract source is specified", func() {
		const name = "test-no-contract"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{},
					Target:      pactov1alpha1.TargetRef{ServiceName: "nonexistent"},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			pacto := &pactov1alpha1.Pacto{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pacto)).To(Succeed())
		})

		It("should set ContractValid=False and contractStatus=NonCompliant", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
				cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(pactov1alpha1.ReasonContractInvalid))
				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusNonCompliant))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("When target service does not exist", func() {
		const name = "test-no-target"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					Target:      pactov1alpha1.TargetRef{ServiceName: "nonexistent-svc"},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			pacto := &pactov1alpha1.Pacto{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pacto)).To(Succeed())
		})

		It("should set ContractValid=True but have findings and contractStatus=Warning or NonCompliant", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				contractCond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
				g.Expect(contractCond).NotTo(BeNil())
				g.Expect(contractCond.Status).To(Equal(metav1.ConditionTrue))

				// v2: findings-based status. Missing service/workload should generate findings.
				g.Expect(pacto.Status.Summary).NotTo(BeNil())
				// We expect at least some warnings or errors.
				g.Expect(pacto.Status.Summary.ErrorCount + pacto.Status.Summary.WarningCount).To(BeNumerically(">", 0))

				// ContractStatus should be Warning or NonCompliant (not Compliant).
				g.Expect(pacto.Status.ContractStatus).NotTo(Equal(pactov1alpha1.ContractStatusCompliant))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("When service exists with matching workload", func() {
		const name = "test-compliant"
		const svcName = "compliant-svc"

		BeforeEach(func() {
			createService(svcName, "default", 8080)
			createDeployment(svcName, "default")

			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					Target:      pactov1alpha1.TargetRef{ServiceName: svcName},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
			deleteService(svcName, "default")
			deleteDeployment(svcName, "default")
		})

		It("should set contractStatus=Compliant with no error findings", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusCompliant))
				g.Expect(pacto.Status.Summary).NotTo(BeNil())
				g.Expect(pacto.Status.Summary.ErrorCount).To(Equal(int32(0)))
				g.Expect(pacto.Status.LastReconciledAt).NotTo(BeNil())

				// Check resources are populated
				g.Expect(pacto.Status.Resources).NotTo(BeNil())
				g.Expect(pacto.Status.Resources.Service).NotTo(BeNil())
				g.Expect(pacto.Status.Resources.Service.Exists).To(BeTrue())
				g.Expect(pacto.Status.Resources.Workload).NotTo(BeNil())
				g.Expect(pacto.Status.Resources.Workload.Exists).To(BeTrue())
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("Reference-only contract (no target)", func() {
		const name = "test-reference"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					// No Target — reference-only
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
		})

		It("should set contractStatus=Reference with no findings", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusReference))
				g.Expect(pacto.Status.Summary).NotTo(BeNil())
				g.Expect(pacto.Status.Summary.ErrorCount).To(Equal(int32(0)))

				// No runtime status should be set for reference-only
				g.Expect(pacto.Status.Resources).To(BeNil())
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

})

// Helper functions to create/delete resources for testing

func createService(name, namespace string, ports ...int32) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
		},
	}
	for _, port := range ports {
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
			Name: fmt.Sprintf("port-%d", port),
			Port: port,
		})
	}
	Expect(k8sClient.Create(ctx, svc)).To(Succeed())
}

func deleteService(name, namespace string) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	_ = k8sClient.Delete(ctx, svc)
}

func createDeployment(name, namespace string) {
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, dep)).To(Succeed())
}

func deleteDeployment(name, namespace string) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	_ = k8sClient.Delete(ctx, dep)
}

func deleteResource(name, namespace string) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	_ = k8sClient.Delete(ctx, pacto)
}
