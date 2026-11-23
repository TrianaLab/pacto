/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// pagingReader serves ReplicaSets page by page the way the API reader does:
// each page is decoded into the list it is handed.
type pagingReader struct {
	client.Reader
	items []appsv1.ReplicaSet
}

func (r pagingReader) List(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
	o := (&client.ListOptions{}).ApplyOptions(opts)
	from, _ := strconv.Atoi(o.Continue)
	to := min(from+int(o.Limit), len(r.items))
	page := appsv1.ReplicaSetList{Items: r.items[from:to]}
	if to < len(r.items) {
		page.Continue = strconv.Itoa(to)
	}
	b, _ := json.Marshal(page)
	return json.Unmarshal(b, list)
}

// TestListAllKeepsEveryPage: a second page never overwrites the first.
func TestListAllKeepsEveryPage(t *testing.T) {
	items := make([]appsv1.ReplicaSet, 0, pageSize+2)
	for i := range pageSize + 2 {
		items = append(items, appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "rs-" + strconv.Itoa(i)}})
	}
	l := &Linker{Reader: pagingReader{items: items}}
	var got appsv1.ReplicaSetList
	if err := l.listAll(context.Background(), &got, "shop"); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, rs := range got.Items {
		names[rs.Name] = true
	}
	if len(got.Items) != len(items) || len(names) != len(items) {
		t.Fatalf("listed %d objects, %d distinct; want %d", len(got.Items), len(names), len(items))
	}
}

// TestLinkerSampledPodsArePartial: pods past the cap leave the cycle partial.
func TestLinkerSampledPodsArePartial(t *testing.T) {
	c := newClient(interceptor.Funcs{}, cluster(append(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA),
		deployment("billing", annRef, "ghcr.io/acme/billing:1", dB)...)...)...)
	l := newLinker(c, &evaluator{service: "payments"})
	l.maxPods = 1
	sync(t, l)
	got := l.Current()
	if got.Completeness != fleet.CompletenessPartial || !slices.ContainsFunc(got.Limitations, func(lim fleet.Limitation) bool {
		return lim.Code == fleet.LimitationSourcePartial && strings.Contains(lim.Message, "sampled the first 1 running pods")
	}) {
		t.Fatalf("a sampled pod read is complete: %+v", got.Limitations)
	}
}

// TestLinkerKeepsSinceAcrossFailedCycle: a cycle whose index listing fails
// links nothing by digest, and the next good one keeps the first Since.
func TestLinkerKeepsSinceAcrossFailedCycle(t *testing.T) {
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", "", "reg.io/payments:2.3.0", dA)...)...)
	l := digestLinker(t, c, &evaluator{service: "payments"}, newFake())
	tags := l.Tags
	sync(t, l)
	l.Now = func() time.Time { return t0.Add(time.Hour) }
	l.Tags = func(context.Context) ([]string, error) { return nil, errors.New("503") }
	sync(t, l)
	if len(l.Current().Targets) != 0 {
		t.Fatalf("linked with no index: %+v", l.Current().Targets)
	}
	l.Now, l.Tags = func() time.Time { return t0.Add(2 * time.Hour) }, tags
	sync(t, l)
	if got := l.Current().Targets; len(got) != 1 || got[0].Metadata.Labels[core.LinkedSinceLabel] != "2026-11-22T10:00:00Z" {
		t.Fatalf("since after a failed cycle: %+v", got)
	}
}

// TestLinkerWaitsToSeed: a restarted linker that cannot read its ConfigMap
// writes nothing, so the Since a previous linker wrote survives.
func TestLinkerWaitsToSeed(t *testing.T) {
	failGet := false
	c := newClient(interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*corev1.ConfigMap); ok && failGet {
			failGet = false
			return errors.New("down")
		}
		return c.Get(ctx, key, obj, opts...)
	}}, cluster(deployment("payments", annRef, "ghcr.io/acme/payments:2.3.0", dA)...)...)
	first := newLinker(c, &evaluator{service: "payments"})
	first.Now = func() time.Time { return t0.Add(-2 * time.Hour) }
	sync(t, first)
	again := newLinker(c, &evaluator{service: "payments"})
	failGet = true
	sync(t, again)
	sync(t, again)
	if got := again.Current().Targets; len(got) != 1 || got[0].Metadata.Labels[core.LinkedSinceLabel] != "2026-11-22T08:00:00Z" {
		t.Fatalf("since after a failed first read: %+v", got)
	}
}

// TestLinkerAmbiguousSiblingHasNoVersion: a sibling linked by a digest
// several versions record carries no version, so no dependency range is
// judged on a guess.
func TestLinkerAmbiguousSiblingHasNoVersion(t *testing.T) {
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", "", "reg.io/payments:2.3.0", dA)...)...)
	f := newFake()
	l := digestLinker(t, c, &evaluator{service: "payments"}, f)
	older := f.publishVersion(t, "payments", "reg.io/c/payments", "2.2.0", "reg.io/payments", dA)
	tags := l.Tags
	l.Tags = func(ctx context.Context) ([]string, error) { ts, err := tags(ctx); return append(ts, older), err }
	sync(t, l)
	if s := l.Siblings(); len(s) != 1 || s[0].Version != "" {
		t.Fatalf("siblings %+v, want one with no version", s)
	}
}

// TestLinkerPartialPoolMakesNoNewDigestLink: an unread index entry may hold
// another version or service recording the digest, so a partial pool links
// no new workload by digest; one linked before keeps its link.
func TestLinkerPartialPoolMakesNoNewDigestLink(t *testing.T) {
	c := newClient(interceptor.Funcs{}, cluster(deployment("payments", "", "reg.io/payments:2.3.0", dA)...)...)
	f := newFake()
	l := digestLinker(t, c, &evaluator{service: "payments"}, f)
	unread := f.publishVersion(t, "payments", "reg.io/c/payments", "2.4.0", "reg.io/payments", dA)
	f.entryErr[unread] = true
	tags := l.Tags
	l.Tags = func(ctx context.Context) ([]string, error) { ts, err := tags(ctx); return append(ts, unread), err }
	sync(t, l)
	if got := l.Current(); len(got.Targets) != 0 || got.Completeness != fleet.CompletenessPartial {
		t.Fatalf("a partial pool linked: %+v", got)
	}
	f.entryErr[unread] = false
	sync(t, l)
	// unread is cached now; another entry fails instead.
	other := f.publishVersion(t, "billing", "reg.io/c/billing", "1.0.0", "reg.io/billing", dB)
	f.entryErr[other] = true
	l.Tags = func(ctx context.Context) ([]string, error) {
		ts, err := tags(ctx)
		return append(ts, unread, other), err
	}
	sync(t, l)
	if got := l.Current(); len(got.Targets) != 1 || got.Completeness != fleet.CompletenessPartial {
		t.Fatalf("a workload linked before lost its link: %+v", got)
	}
}
