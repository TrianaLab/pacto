/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

var gvkHelmRelease = schema.GroupVersionKind{Group: "helm.toolkit.fluxcd.io", Version: "v2", Kind: "HelmRelease"}

// helmReleaseOverrides are the HelmRelease fields that change what runs
// beyond the chart bytes the pointer names. Mediated names their paths, never
// their values: inline values can hold secrets.
var helmReleaseOverrides = [][]string{{"values"}, {"valuesFrom"}, {"postRenderers"}}

// helmReleaseApplier is a Flux HelmRelease whose spec.chartRef names an
// OCIRepository in its own namespace, the Stage namespace. The promoter
// writes the chart digest on that OCIRepository.
type helmReleaseApplier struct {
	hr    *unstructured.Unstructured
	chart *source
}

func (a helmReleaseApplier) object() *unstructured.Unstructured { return a.hr }
func (a helmReleaseApplier) src() *source                       { return a.chart }

func (a helmReleaseApplier) target() string {
	return "spec.ref.digest of OCIRepository " + a.chart.obj.GetName() + " (chart of HelmRelease " + a.hr.GetName() + ")"
}

// setDigest is the Flux OCIRepository write, on the chart source.
func (a helmReleaseApplier) setDigest(ctx context.Context, c client.Client, digest string, now time.Time) error {
	return fluxApplier{a.chart}.setDigest(ctx, c, digest, now)
}

// refusedApplier is an applier object the promoter reads no further and
// never writes to, with the Ready condition that says why.
type refusedApplier struct{ ready delivery.Condition }

func (e *refusedApplier) Error() string { return e.ready.Message }

// helmRelease reads the HelmRelease ns/name and the OCIRepository its
// spec.chartRef names. A chart from spec.chart, a chartRef to anything but an
// OCIRepository, and a chartRef outside ns are refused before the chart
// source is read: the apply-ref label repeats across namespaces, so a
// foreign chartRef would let one tenant's HelmRelease steer the promoter's
// write into another tenant's chart source.
func helmRelease(ctx context.Context, r client.Reader, ns, name string) (applier, error) {
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(gvkHelmRelease)
	if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, hr); err != nil {
		return nil, err
	}
	what := "the HelmRelease " + name
	kind, _, _ := unstructured.NestedString(hr.Object, "spec", "chartRef", "kind")
	chart, _, _ := unstructured.NestedString(hr.Object, "spec", "chartRef", "name")
	chartNS, _, _ := unstructured.NestedString(hr.Object, "spec", "chartRef", "namespace")
	var refused delivery.Condition
	switch _, fromChart, _ := unstructured.NestedFieldNoCopy(hr.Object, "spec", "chart"); {
	case fromChart:
		refused = notReady(delivery.ReadyReasonApplierNotChartRef, what+" takes its chart from spec.chart, a HelmChart that pins a version and never a digest")
	case kind != kindOCIRepository:
		refused = notReady(delivery.ReadyReasonApplierUnreadable, what+" names no OCIRepository in spec.chartRef (kind "+fmt.Sprintf("%q", kind)+
			"), so the promoter has no chart digest to write")
	case chartNS != "" && chartNS != ns:
		refused = notReady(delivery.ReadyReasonApplierUnreadable, what+" chartRef names OCIRepository "+chartNS+"/"+chart+
			" outside the Stage namespace "+ns+", so the promoter never writes to it")
	default:
		s, err := ociRepository(ctx, r, ns, chart)
		if err != nil {
			return nil, err
		}
		return helmReleaseApplier{hr: hr, chart: s}, nil
	}
	return nil, &refusedApplier{refused}
}

// applied reads whether helm-controller released digest: Ready True with the
// newest release at that chart digest is a success; Ready False after an
// attempt at digest, for a reason other than progress, is a confirmed
// failure; anything else is "not yet". The released digest is
// status.history[0].ociDigest, or status.lastAttemptedRevisionDigest when the
// history carries none (helm-controller v2, Flux v2.9).
func (a helmReleaseApplier) applied(_ context.Context, _ Cluster, _, digest string) (ok, failed bool, msg string, err error) {
	name := a.hr.GetName()
	ready, reason, message := readyCondition(a.hr.Object)
	attempted, _, _ := unstructured.NestedString(a.hr.Object, "status", "lastAttemptedRevisionDigest")
	released := attempted
	if history, _, _ := unstructured.NestedSlice(a.hr.Object, "status", "history"); len(history) > 0 {
		if m, _ := history[0].(map[string]any); m["ociDigest"] != nil {
			released, _ = m["ociDigest"].(string)
		}
	}
	switch {
	case ready == statusTrue && delivery.RevisionDigest(released) == digest:
		return true, false, "", nil
	case ready == statusFalse && delivery.RevisionDigest(attempted) == digest && !helmProgress[reason]:
		return false, true, fmt.Sprintf("HelmRelease %s failed to release %s: %s: %s", name, digest, reason, message), nil
	}
	return false, false, "HelmRelease " + name + " has not released " + digest + " yet", nil
}

// helmProgress are the Ready reasons of a HelmRelease still working on a
// release.
var helmProgress = map[string]bool{"Progressing": true, "ProgressingWithRetry": true, "DependencyNotReady": true}

// mediated is the Mediated condition of a labelled HelmRelease: its chart
// source carries the apply-ref label, it runs as its own service account and
// sets nothing that changes the chart's output.
func (a helmReleaseApplier) mediated(context.Context, Cluster, string) (delivery.Condition, error) {
	name, chart := a.hr.GetName(), a.chart.obj.GetName()
	if !labelled(a.chart.obj) {
		return notMediated("ApplyRefUnguarded", "the chart OCIRepository "+chart+" of HelmRelease "+name+" has no "+ApplyRefLabel+
			" label, so admission does not guard its digest"), nil
	}
	if sa, _, _ := unstructured.NestedString(a.hr.Object, "spec", "serviceAccountName"); sa == "" {
		return notMediated("ApplierUnscoped", "HelmRelease "+name+" sets no serviceAccountName, so Flux installs each version with "+
			"helm-controller's own rights (cluster-admin in a default install) and a version can grant itself anything"), nil
	}
	if set := setPaths(a.hr.Object, []string{"spec"}, helmReleaseOverrides); len(set) > 0 {
		return notMediated("ApplierOverrides", "HelmRelease "+name+" sets "+strings.Join(set, ", ")), nil
	}
	return pointerOnly, nil
}

// notReady says why a labelled HelmRelease cannot take a version: its chart
// source lacks the apply-ref label of the same Stage, so admission refuses
// the promoter's digest write or another Stage could drive it.
func (a helmReleaseApplier) notReady(what string) *delivery.Condition {
	chart, want := a.chart.obj, a.hr.GetLabels()[ApplyRefLabel]
	var c delivery.Condition
	switch got := chart.GetLabels()[ApplyRefLabel]; {
	case !labelled(chart):
		c = notReady(reasonApplierUnlabelled, what+" takes its chart from OCIRepository "+chart.GetName()+", which has no "+ApplyRefLabel+
			" label, so admission refuses every version the promoter writes to it")
	case got != want:
		c = notReady(reasonApplierUnlabelled, what+" takes its chart from OCIRepository "+chart.GetName()+", labelled "+ApplyRefLabel+"="+got+
			": the label names the one Stage that applies through it, "+want)
	default:
		return nil
	}
	return &c
}
