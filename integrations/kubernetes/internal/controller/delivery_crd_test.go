/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The Stage CRD admits at most one candidate source, so the version offered
// and the version judged always come from the same place.
var _ = Describe("Stage candidates", func() {
	stage := func(name string, candidates map[string]any) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"namespace": "default", "name": name},
			"spec": map[string]any{
				"service": map[string]any{"name": "payments"}, "environment": "prod",
				"applyRef": map[string]any{"name": "payments"}, "candidates": candidates,
			},
		}}
		u.SetGroupVersionKind(schema.GroupVersionKind{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Kind: "Stage"})
		return u
	}
	oci := map[string]any{"name": "payments-candidates"}
	up := map[string]any{"name": "payments", "namespace": "test"}
	remote := map[string]any{"repository": "reg.example/streams", "stream": "c/test/payments"}

	It("refuses two sources", func() {
		for name, c := range map[string]map[string]any{
			"oci-and-upstream":    {"ociRepositoryRef": oci, "upstreamStage": up},
			"upstream-and-remote": {"upstreamStage": up, "upstreamRecords": remote},
			"all-three":           {"ociRepositoryRef": oci, "upstreamStage": up, "upstreamRecords": remote},
		} {
			err := k8sClient.Create(ctx, stage(name, c))
			Expect(err).To(HaveOccurred(), name)
			Expect(err.Error()).To(ContainSubstring("set at most one of candidates.ociRepositoryRef"), name)
		}
	})

	It("admits one source or none", func() {
		for name, c := range map[string]map[string]any{
			"one-oci":    {"ociRepositoryRef": oci},
			"one-remote": {"upstreamRecords": remote},
			"none":       {},
		} {
			Expect(k8sClient.Create(ctx, stage(name, c))).To(Succeed(), name)
		}
	})
})

// Clients ask the apiserver for only the Operations a read needs: a Stage's,
// or those that did not succeed.
var _ = Describe("Operation selectable fields", func() {
	op := func(name, stage string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"namespace": "default", "name": name},
			"spec": map[string]any{"type": "delivery.pacto.trianalab.io/promote", "stage": stage, "facet": "pointer",
				"actor": map[string]any{"user": "bot"}},
		}}
		u.SetGroupVersionKind(schema.GroupVersionKind{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Kind: "Operation"})
		return u
	}
	It("selects by spec.stage and status.outcome", func() {
		Expect(k8sClient.Create(ctx, op("sel-a", "payments-prod"))).To(Succeed())
		Expect(k8sClient.Create(ctx, op("sel-b", "ledger-prod"))).To(Succeed())
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Kind: "OperationList"})
		Expect(k8sClient.List(ctx, list, client.InNamespace("default"),
			client.MatchingFieldsSelector{Selector: fields.OneTermEqualSelector("spec.stage", "payments-prod")})).To(Succeed())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].GetName()).To(Equal("sel-a"))
		Expect(k8sClient.List(ctx, list, client.InNamespace("default"),
			client.MatchingFieldsSelector{Selector: fields.OneTermNotEqualSelector("status.outcome", "Succeeded")})).To(Succeed())
		Expect(len(list.Items)).To(BeNumerically(">=", 2))
	})
})
