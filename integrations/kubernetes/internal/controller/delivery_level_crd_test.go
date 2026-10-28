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
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// A Stage starts on any level (creating one is an admin action), and its
// startLevel never changes: only an autonomy operation moves the level.
var _ = Describe("Stage startLevel", func() {
	stage := func(name string, start any) *unstructured.Unstructured {
		spec := map[string]any{"service": map[string]any{"name": "payments"}, "environment": "prod",
			"applyRef": map[string]any{"name": "payments"}}
		if start != nil {
			spec["startLevel"] = start
		}
		u := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": "default", "name": name}, "spec": spec}}
		u.SetGroupVersionKind(schema.GroupVersionKind{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Kind: "Stage"})
		return u
	}

	It("accepts every level and refuses a name that is not one", func() {
		for _, r := range []string{"advise", "auto-nonbreaking", "autonomous"} {
			Expect(k8sClient.Create(ctx, stage("start-"+r, r))).To(Succeed(), r)
		}
		err := k8sClient.Create(ctx, stage("start-yolo", "yolo"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("startLevel"))
	})

	It("defaults to observe and keeps startLevel fixed", func() {
		s := stage("start-default", nil)
		Expect(k8sClient.Create(ctx, s)).To(Succeed())
		got, _, _ := unstructured.NestedString(s.Object, "spec", "startLevel")
		Expect(got).To(Equal("observe"))
		Expect(unstructured.SetNestedField(s.Object, "advise", "spec", "startLevel")).To(Succeed())
		err := k8sClient.Update(ctx, s)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("startLevel is fixed at creation"))
	})
})
