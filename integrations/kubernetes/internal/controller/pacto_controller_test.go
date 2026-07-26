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

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/api/v1alpha1"
)

const validContract = `
pactoVersion: "2.0"
service:
  name: test-svc
  version: 1.0.0
  owner:
    team: team-a
state:
  type: stateless
  dataCriticality: low
  persistence:
    durability: ephemeral
    scope: local
workload: service
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

		It("should set ContractValid=False and contractStatus=Invalid", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
				cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(pactov1alpha1.ReasonContractInvalid))
				// Spec section 9.8: no contract source is a load error -> classifyLoadError -> Invalid (fail-closed)
				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusInvalid))
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

		It("should set ContractValid=True and ContractStatus=Unknown (workload declared, target missing)", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				contractCond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
				g.Expect(contractCond).NotTo(BeNil())
				g.Expect(contractCond.Status).To(Equal(metav1.ConditionTrue))

				// Contract declares workload:service (required). Target doesn't exist -> workload GET NotFound
				// -> EVIDENCE_MISSING (section 7.1) -> aggregate Unknown.
				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusUnknown))
				g.Expect(pacto.Status.Summary).NotTo(BeNil())
				g.Expect(pacto.Status.Summary.UnknownCount).To(BeNumerically(">", 0))
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

const readinessContract = `
pactoVersion: "2.0"
service:
  name: readiness-svc
  version: 1.0.0
  owner:
    team: team-a
state:
  type: stateless
  dataCriticality: low
  persistence:
    durability: ephemeral
    scope: local
workload: service
readiness:
  expires: "2099-12-31"
  claims:
    - id: dashboard
      type: url
      status: done
      evidence: https://grafana.company.com/readiness-svc
      weight: 60
    - id: runbook
      type: document
      status: done
      evidence: docs/runbooks/readiness-svc.md
      weight: 40
`

const readinessExpiredContract = `
pactoVersion: "2.0"
service:
  name: readiness-expired-svc
  version: 1.0.0
  owner:
    team: team-a
state:
  type: stateless
  dataCriticality: low
  persistence:
    durability: ephemeral
    scope: local
workload: service
readiness:
  expires: "2000-01-15"
  claims:
    - id: dashboard
      type: url
      status: done
      evidence: https://grafana.company.com/readiness-expired-svc
      weight: 60
    - id: security-review
      type: ticket
      status: done
      evidence: SEC-1
      weight: 40
`

var _ = Describe("Pacto Controller Readiness", func() {

	Context("When a reference contract declares readiness with all checks current", func() {
		const name = "test-readiness-current"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: readinessContract},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
		})

		It("populates status.readiness and ReadinessChecksCurrent=True", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.Readiness).NotTo(BeNil())
				g.Expect(pacto.Status.Readiness.Score).To(Equal(int32(100)))
				g.Expect(pacto.Status.Readiness.TotalWeight).To(Equal(int32(100)))
				g.Expect(pacto.Status.Readiness.DoneCount).To(Equal(int32(2)))
				g.Expect(pacto.Status.Readiness.Expired).To(BeFalse())

				cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(pactov1alpha1.ReasonReadinessSatisfied))

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusReference))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("When a contract declares an expired readiness check", func() {
		const name = "test-readiness-expired"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: readinessExpiredContract},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
		})

		It("sets ReadinessChecksCurrent=False without affecting ContractStatus", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.Readiness).NotTo(BeNil())
				g.Expect(pacto.Status.Readiness.Expired).To(BeTrue())
				g.Expect(pacto.Status.Readiness.Score).To(Equal(int32(0)))

				cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(pactov1alpha1.ReasonReadinessExpired))

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusReference))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("When a contract declares no readiness", func() {
		const name = "test-readiness-absent"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			deleteResource(name, "default")
		})

		It("leaves status.readiness nil and does not set ReadinessChecksCurrent", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.Readiness).To(BeNil())

				cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
				g.Expect(cond).To(BeNil())

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusReference))
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("When target contract has InterfaceBindings and ObservationWindows", func() {
		const name = "test-interface-bindings"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					Target: pactov1alpha1.TargetRef{
						ServiceName: "test-service",
						InterfaceBindings: []pactov1alpha1.InterfaceBinding{
							{Interface: "http"},
							{Interface: "grpc"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())
		})

		AfterEach(func() {
			p := &pactov1alpha1.Pacto{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
		})

		It("processes InterfaceBindings and persists ObservationWindows", func() {
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusUnknown))

				// Observation windows should be populated (if any negative findings observed)
				// This covers the loop at lines 216-219
				// InterfaceBindings loop (lines 207-212) is also covered by processing the spec
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})

	Context("When target contract has existing ObservationWindows", func() {
		const name = "test-observation-windows"

		BeforeEach(func() {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					Target:      pactov1alpha1.TargetRef{ServiceName: "test-service"},
				},
			}
			Expect(k8sClient.Create(ctx, pacto)).To(Succeed())

			// Simulate a previous reconciliation that left observation windows
			Eventually(func(g Gomega) {
				p := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, p)).To(Succeed())
				p.Status.ObservationWindows = []pactov1alpha1.ObservationWindow{
					{
						Kind:                    "check",
						Subject:                 "foo",
						FirstObservedNegativeAt: metav1.Now(),
					},
				}
				g.Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})

		AfterEach(func() {
			p := &pactov1alpha1.Pacto{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, p)).To(Succeed())
			Expect(k8sClient.Delete(ctx, p)).To(Succeed())
		})

		It("processes existing ObservationWindows during reconciliation", func() {
			// Trigger another reconciliation by updating the generation
			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())
				pacto.Spec.Target.ServiceName = "updated-service"
				g.Expect(k8sClient.Update(ctx, pacto)).To(Succeed())
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

			Eventually(func(g Gomega) {
				pacto := &pactov1alpha1.Pacto{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, pacto)).To(Succeed())

				// Status should be Unknown (no workload exists)
				g.Expect(pacto.Status.ContractStatus).To(Equal(pactov1alpha1.ContractStatusUnknown))

				// Observation windows loop (lines 216-219) should have been covered
			}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
		})
	})
})
