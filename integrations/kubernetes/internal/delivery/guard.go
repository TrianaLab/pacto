/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"slices"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// unguardedBy says why the admission policies do not guard what the promoter
// trusts, or "" when every one is installed and enforcing: the policy fails
// closed, and a binding of the same name binds that policy with Deny, to
// every object (no matchResources) and with no params (no paramRef). A
// binding set to Warn or Audit, or narrowed to some objects, guards nothing
// the promoter can count on. An error is a read that failed.
func unguardedBy(ctx context.Context, c client.Reader, policies []string) (string, error) {
	for _, name := range policies {
		p, b := &admissionv1.ValidatingAdmissionPolicy{}, &admissionv1.ValidatingAdmissionPolicyBinding{}
		for _, obj := range []client.Object{p, b} {
			if err := c.Get(ctx, client.ObjectKey{Name: name}, obj); apierrors.IsNotFound(err) {
				return "the admission policy " + name + " or its binding is not installed", nil
			} else if err != nil {
				return "", err
			}
		}
		switch s := b.Spec; {
		case p.Spec.FailurePolicy != nil && *p.Spec.FailurePolicy != admissionv1.Fail:
			return "the admission policy " + name + " does not fail closed", nil
		case s.PolicyName != name:
			return "the binding " + name + " binds " + s.PolicyName + ", not the policy " + name, nil
		case !slices.Contains(s.ValidationActions, admissionv1.Deny):
			return "the binding " + name + " does not deny", nil
		case narrowed(s.MatchResources) || s.ParamRef != nil:
			return "the binding " + name + " is narrowed by matchResources or paramRef", nil
		}
	}
	return "", nil
}

// narrowed reports whether m binds a policy to fewer objects than every one
// it matches: a selector or a resource rule. An empty one, as some tools
// write it out, narrows nothing.
func narrowed(m *admissionv1.MatchResources) bool {
	empty := func(s *metav1.LabelSelector) bool {
		return s == nil || len(s.MatchLabels) == 0 && len(s.MatchExpressions) == 0
	}
	return m != nil && (!empty(m.NamespaceSelector) || !empty(m.ObjectSelector) || len(m.ResourceRules) > 0 || len(m.ExcludeResourceRules) > 0)
}
