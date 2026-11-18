package publish

import (
	"context"
	"testing"
)

const testSource = "https://github.com/acme/payments"

// publishWith publishes payments 1.0.0 with source to a fresh registry and
// returns the registry, its host and the result.
func publishWith(t *testing.T, source string) (*Client, string, *Result) {
	t.Helper()
	host, reg := newRegistry(t)
	in := Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "abc123", Source: source, Checks: []Check{{Name: "validation", Passed: true}}}
	res, err := Apply(context.Background(), reg, newTools(), in)
	expect(t, err == nil, "publish: %v", err)
	return reg, host, res
}

func TestApplyStampsSource(t *testing.T) {
	reg, host, _ := publishWith(t, testSource)
	for _, ref := range []string{host + "/payments:1.0.0", host + "/charts/payments:1.0.0", host + "/deploy/payments:1.0.0"} {
		m, err := reg.Head(context.Background(), ref)
		expect(t, err == nil, "head %s: %v", ref, err)
		expect(t, m.Annotations[AnnotationSource] == testSource, "%s annotations: %v", ref, m.Annotations)
	}
}

func TestApplyNoSourceNoAnnotation(t *testing.T) {
	reg, host, _ := publishWith(t, "")
	m, err := reg.Head(context.Background(), host+"/deploy/payments:1.0.0")
	expect(t, err == nil, "head: %v", err)
	_, ok := m.Annotations[AnnotationSource]
	expect(t, !ok, "an empty source stamped %v", m.Annotations)
}

// TestBundleDigestUnchangedBySource: the contract bundle stays annotation-free,
// so the bundle a sourceless publish would write is the one already written.
func TestBundleDigestUnchangedBySource(t *testing.T) {
	reg, host, with := publishWith(t, testSource)
	in := Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "abc123", Checks: []Check{{Name: "validation", Passed: true}}}
	p := MakePlan(context.Background(), reg, in)
	bundle := p.Items[len(p.Items)-1]
	expect(t, bundle.State == Identical && bundle.Digest == with.Digests[BundleItem], "bundle without source: %+v, with: %s", bundle, with.Digests[BundleItem])
}
