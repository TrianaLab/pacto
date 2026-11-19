package pactify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

const invoicesDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// invoicesShop is shop/invoices, two pods on node-1 (observed now) and
// node-2 (never observed), calling shop/ledger, which two reconciled Pactos
// describe at 2.3.1 and 2.4.0.
func invoicesShop(now time.Time) []runtime.Object {
	pod := func(name, node string) runtime.Object {
		return controlled(obj("v1", "Pod", "shop", name, map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "invoices"}},
			"spec": map[string]any{"nodeName": node},
			"status": map[string]any{"startTime": now.Add(-time.Hour).UTC().Format(time.RFC3339), "containerStatuses": []any{
				map[string]any{"name": "invoices", "imageID": "reg.io/acme/invoices@" + invoicesDigest}}}}), "ReplicaSet", "invoices-7f", "u-"+name)
	}
	return []runtime.Object{
		deployment("shop", "invoices", "reg.io/acme/invoices:1.0.0"), service("shop", "invoices", "10.0.0.30"),
		controlled(obj("apps/v1", "ReplicaSet", "shop", "invoices-7f", nil), "Deployment", "invoices", ""),
		pod("invoices-7f-a", "node-1"), pod("invoices-7f-b", "node-2"),
		service("shop", "ledger", "10.0.0.60"),
		pactoCR("shop", "ledger-a", "ledger", "ledger", "oci://reg.io/acme/contracts/ledger", "2.4.0", 1, 1),
		pactoCR("shop", "ledger-b", "ledger", "ledger", "oci://reg.io/acme/contracts/ledger", "2.3.1", 1, 1),
		obj("pacto.trianalab.io/v1alpha1", "NodeRuntimeObservation", "pacto-system", "node-1", map[string]any{"status": map[string]any{
			"nodeName": "node-1", "publishIntervalSeconds": int64(30), "lastPublished": now.Add(-10 * time.Second).UTC().Format(time.RFC3339),
			"collection": map[string]any{"state": "Running"},
			"pods": []any{map[string]any{"uid": "u-invoices-7f-a", "connections": []any{
				map[string]any{"address": "10.0.0.60", "port": int64(80), "successes": int64(2), "lastSuccessAt": now.UTC().Format(time.RFC3339)}}}}}}),
	}
}

// draftInvoices drafts the shop namespace of objs into out, applied unless
// plan, and returns the invoices draft.
func draftInvoices(t *testing.T, now time.Time, out string, plan bool, objs ...runtime.Object) *Result {
	t.Helper()
	c := ClusterOptions{Namespaces: []string{"shop"}, Out: out, Now: func() time.Time { return now }}
	b, err := Cluster(context.Background(), fakeCluster(objs...), Options{Plan: plan}, c, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	return result(t, b, "invoices")
}

func recordField(t *testing.T, rec *contract.InferredRecord, name string) contract.Inferred {
	t.Helper()
	i := slices.IndexFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == name })
	if i < 0 {
		t.Fatalf("no field %s in\n%s", name, rec.Marshal())
	}
	return rec.Fields[i]
}

func readRecord(t *testing.T, dir string) *contract.InferredRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, contract.InferredRecordFile))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := contract.ParseInferredRecord(data)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	return rec
}

// folderOnly is rec without its cluster facts: what a folder read records.
func folderOnly(rec *contract.InferredRecord) *contract.InferredRecord {
	out := *rec
	out.Fields = slices.DeleteFunc(slices.Clone(rec.Fields), func(f contract.Inferred) bool { return strings.HasPrefix(f.Source, "cluster:") })
	out.Inputs.Cluster, out.Coverage, out.Hidden = nil, nil, nil
	return &out
}

// TestClusterDraftWritesInferredRecord: applying writes pacto.inferred.yaml,
// which parses; bundle verification refuses it for its cluster facts and
// accepts it once they are taken out.
func TestClusterDraftWritesInferredRecord(t *testing.T) {
	now := time.Now()
	out := t.TempDir()
	r := draftInvoices(t, now, out, false, invoicesShop(now)...)
	if file(t, r, contract.InferredRecordFile).Action != Add || r.Inferred == nil || !hasLine(r.Found, "values from the cluster in pacto.inferred.yaml (read ") {
		t.Fatalf("%+v", r)
	}
	rec := readRecord(t, r.Dir)
	if err := contract.VerifyInferredRecord(os.DirFS(r.Dir), rec); err == nil || !strings.Contains(err.Error(), "cluster facts reach a bundle only through a signed declaration") {
		t.Fatalf("a cluster record verified: %v", err)
	}
	if err := contract.VerifyInferredRecord(os.DirFS(r.Dir), folderOnly(rec)); err != nil {
		t.Fatalf("the folder part: %v\n%s", err, folderOnly(rec).Marshal())
	}
}

func TestClusterDraftPlanWritesNothing(t *testing.T) {
	now := time.Now()
	out := filepath.Join(t.TempDir(), "drafts")
	r := draftInvoices(t, now, out, true, invoicesShop(now)...)
	f := file(t, r, contract.InferredRecordFile)
	if f.Action != Add || !strings.Contains(f.Content, "object: shop/Deployment/invoices") {
		t.Fatalf("%+v", f)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a plan wrote under --out: %v", err)
	}
}

// TestClusterDraftRecordMatchesFolderCompletion: once the snapshot stamp is
// undone, the record's folder facts are what folder completion of the
// written draft gives.
func TestClusterDraftRecordMatchesFolderCompletion(t *testing.T) {
	now := time.Now()
	r := draftInvoices(t, now, t.TempDir(), false, invoicesShop(now)...)
	data, _ := os.ReadFile(filepath.Join(r.Dir, "pacto.yaml"))
	declared, err := contract.Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	folder, err := discover.CompleteIn(r.Dir, r.Dir, declared, contract.InferredInputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var undone []contract.Inferred
	for _, f := range readRecord(t, r.Dir).Fields {
		rest, ok := strings.CutPrefix(f.Source, "cluster:manifest shop/Deployment/invoices ")
		if !ok {
			continue
		}
		f.Source, f.AsOf = rest, ""
		undone = append(undone, f)
	}
	got, _ := json.Marshal(undone)
	want, _ := json.Marshal(folder.Fields)
	if len(undone) == 0 || string(got) != string(want) {
		t.Fatalf("cluster record's folder facts:\n%s\nfolder completion:\n%s", got, want)
	}
}

// keptInvoices applies a draft over a folder that already holds a pacto.yaml
// declaring only the service name, and returns it and its text.
func keptInvoices(t *testing.T, now time.Time) (*Result, string) {
	t.Helper()
	out := t.TempDir()
	text := "pactoVersion: \"2.5\"\nservice:\n  name: invoices\n"
	writeTree(t, out, map[string]string{"shop/invoices/pacto.yaml": text})
	return draftInvoices(t, now, out, false, invoicesShop(now)...), text
}

// TestClusterDraftRecordSources: each cluster fact names its kind of source
// and the drafted object, with when it was read; runtime and fleet facts say
// whether every node was observed.
func TestClusterDraftRecordSources(t *testing.T) {
	now := time.Now()
	r, _ := keptInvoices(t, now)
	rec := readRecord(t, r.Dir)
	read := now.UTC().Truncate(time.Second).Format(time.RFC3339)
	observed := now.Add(-10 * time.Second).UTC().Truncate(time.Second).Format(time.RFC3339)
	for name, want := range map[string]contract.Inferred{
		"ports[8080]":                        {Source: "cluster:manifest shop/Deployment/invoices manifests.yaml:", AsOf: read},
		"service.owner":                      {Source: "cluster:label shop/Deployment/invoices team", AsOf: read},
		"image.digest[invoices]":             {Source: "cluster:runtime pods of shop/Deployment/invoices", AsOf: read, Completeness: "complete", Value: invoicesDigest},
		"dependencies[ledger]":               {Source: "cluster:runtime calls from shop/Deployment/invoices", AsOf: observed, Completeness: "partial"},
		"dependencies[ledger].compatibility": {Source: "cluster:fleet reported versions 2.3.1, 2.4.0", AsOf: observed, Completeness: "partial", Value: "^2.3.0"},
	} {
		f := recordField(t, rec, name)
		value := f.Value
		if strings.HasSuffix(name, ".compatibility") {
			value = f.Proposed
		}
		if !strings.HasPrefix(f.Source, want.Source) || f.AsOf != want.AsOf || f.Completeness != want.Completeness || want.Value != "" && value != want.Value {
			t.Errorf("%s: %+v, want %+v", name, f, want)
		}
	}
	if rec.Coverage[len(rec.Coverage)-1] != (contract.InferredCoverage{Source: "runtime-calls", State: "partial", Reason: "1 of 2 nodes observed"}) {
		t.Errorf("coverage %+v", rec.Coverage)
	}
}

// TestClusterCompletesKeptContract: a kept pacto.yaml is never changed; the
// record holds what it does not declare.
func TestClusterCompletesKeptContract(t *testing.T) {
	now := time.Now()
	r, text := keptInvoices(t, now)
	if got, _ := os.ReadFile(filepath.Join(r.Dir, "pacto.yaml")); string(got) != text || r.Mode != modeComplete {
		t.Fatalf("pacto.yaml changed (%s):\n%s", r.Mode, got)
	}
	recordField(t, readRecord(t, r.Dir), "dependencies[ledger]")
}

// TestClusterKeepsExistingRecord: a second run keeps the record of the
// first and says how to record what the cluster shows now.
func TestClusterKeepsExistingRecord(t *testing.T) {
	now := time.Now()
	out := t.TempDir()
	first := draftInvoices(t, now, out, false, invoicesShop(now)...)
	before, _ := os.ReadFile(filepath.Join(first.Dir, contract.InferredRecordFile))
	r := draftInvoices(t, now.Add(time.Hour), out, false, invoicesShop(now)...)
	after, _ := os.ReadFile(filepath.Join(r.Dir, contract.InferredRecordFile))
	if file(t, r, contract.InferredRecordFile).Action != Keep || string(before) != string(after) ||
		!hasLine(r.Next, "pacto.inferred.yaml is from an earlier read: delete it and run again to record what the cluster shows now") {
		t.Fatalf("%+v", r)
	}
}

// TestClusterKeptRecordNeverClaimedWritten: a run that keeps an earlier
// record says its own read was not written, and its result is the record on
// disk.
func TestClusterKeptRecordNeverClaimedWritten(t *testing.T) {
	now := time.Now()
	out := t.TempDir()
	draftInvoices(t, now, out, false, invoicesShop(now)...)
	r := draftInvoices(t, now.Add(time.Hour), out, false, invoicesShop(now)...)
	later := now.Add(time.Hour).UTC().Format(time.RFC3339)
	if hasLine(r.Found, "values from the cluster in") || !hasLine(r.Found, "the cluster read at "+later+" was not written: pacto.inferred.yaml from an earlier read was kept") {
		t.Fatalf("%v", r.Found)
	}
	if got, want := string(r.Inferred.Marshal()), string(readRecord(t, r.Dir).Marshal()); got != want {
		t.Fatalf("result:\n%s\non disk:\n%s", got, want)
	}
}

func TestClusterDraftIsPacto25(t *testing.T) {
	now := time.Now()
	r := draftInvoices(t, now, t.TempDir(), true, invoicesShop(now)...)
	if !strings.Contains(file(t, r, "pacto.yaml").Content, "pactoVersion: \"2.5\"\n") {
		t.Fatalf("%s", file(t, r, "pacto.yaml").Content)
	}
}

// withoutObservations is objs less the node runtime observations.
func withoutObservations(objs []runtime.Object) []runtime.Object {
	return slices.DeleteFunc(objs, func(o runtime.Object) bool {
		return o.(*unstructured.Unstructured).GetKind() == "NodeRuntimeObservation"
	})
}

// TestNoObservationsNoRuntimeField: with no node observed, no call is a
// fact, and the runtime-calls source failed.
func TestNoObservationsNoRuntimeField(t *testing.T) {
	now := time.Now()
	r := draftInvoices(t, now, t.TempDir(), true, withoutObservations(invoicesShop(now))...)
	for _, f := range r.Inferred.Fields {
		if strings.HasPrefix(f.Source, "cluster:runtime calls") || strings.HasPrefix(f.Source, "cluster:fleet") {
			t.Errorf("a runtime field with no observation: %+v", f)
		}
	}
	if got := r.Inferred.Coverage[len(r.Inferred.Coverage)-1]; got != (contract.InferredCoverage{Source: "runtime-calls", State: "failed", Reason: "no node runtime observations"}) {
		t.Errorf("runtime-calls %+v", got)
	}
}

// invoicesFacts reads invoicesShop with only the observed pod, cuts source
// as cut, and returns the facts of invoices.
func invoicesFacts(t *testing.T, cut map[string]string) discover.ClusterFacts {
	t.Helper()
	now := time.Now()
	c := ClusterOptions{MaxItems: DefaultMaxItems, CallWindow: DefaultCallWindow, Now: func() time.Time { return now }}
	nss := []string{"shop"}
	cur, _ := decodeContinue("", nss)
	cl, err := readCluster(context.Background(), fakeCluster(invoicesShop(now)...), c, nss, cur)
	if err != nil {
		t.Fatal(err)
	}
	cl.pods = slices.DeleteFunc(cl.pods, func(p unstructured.Unstructured) bool { return p.GetName() == "invoices-7f-b" })
	for source, state := range cut {
		cl.coverage[source] = contract.InferredCoverage{Source: source, State: state, Reason: "forbidden"}
	}
	w := cl.workloads[slices.IndexFunc(cl.workloads, func(w unstructured.Unstructured) bool { return w.GetName() == "invoices" })]
	return cl.facts(w, cl.inferred(now.Add(-c.CallWindow))[key(w)], true, c)
}

// TestClusterFieldPartialWhenSupportingSourceCut: a call read through a
// source that was not read in full is partial; so is a proposal when the
// Pactos were not.
func TestClusterFieldPartialWhenSupportingSourceCut(t *testing.T) {
	if f := invoicesFacts(t, nil); len(f.Calls) != 1 || f.Calls[0].Partial || f.Calls[0].Proposed != "^2.3.0" || f.Images[0].Partial {
		t.Fatalf("every node observed and every source read: %+v", f)
	}
	for source, state := range map[string]string{"services": "partial", "endpointslices": "failed", "replicasets": "failed", "pods": "partial", "pactos": "failed"} {
		if f := invoicesFacts(t, map[string]string{source: state}); !f.Calls[0].Partial {
			t.Errorf("%s %s: %+v", source, state, f.Calls)
		}
	}
}

// TestClusterImagePartialWhenPodsBounded: a digest read from a bounded pod
// list is partial.
func TestClusterImagePartialWhenPodsBounded(t *testing.T) {
	now := time.Now()
	c := ClusterOptions{Namespaces: []string{"shop"}, Out: t.TempDir(), MaxItems: 1, Now: func() time.Time { return now }}
	cl := fakeCluster(invoicesShop(now)...)
	paged(cl, "pods")
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f := recordField(t, result(t, b, "invoices").Inferred, "image.digest[invoices]"); f.Completeness != "partial" {
		t.Fatalf("%+v", f)
	}
}

// TestClusterImagePartialWhenOwnersCut: a digest is partial when the
// ReplicaSets or Jobs that tie pods to the workload were not all read, since
// a pod of an unread one is not counted.
func TestClusterImagePartialWhenOwnersCut(t *testing.T) {
	for _, source := range []string{"replicasets", "jobs"} {
		f := invoicesFacts(t, map[string]string{source: "failed"})
		if !f.Images[0].Partial || !f.Calls[0].Partial {
			t.Errorf("%s failed: images %+v, calls %+v", source, f.Images, f.Calls)
		}
	}
}

// viewerLedger is viewerShop with invoices also calling shop/ledger, which a
// Pacto describes at 2.3.1.
func viewerLedger(now time.Time) []runtime.Object {
	objs := viewerShop(now)
	nro := objs[len(objs)-1].(*unstructured.Unstructured)
	pods, _, _ := unstructured.NestedSlice(nro.Object, "status", "pods")
	conns := pods[0].(map[string]any)["connections"].([]any)
	pods[0].(map[string]any)["connections"] = append(conns, map[string]any{"address": "10.0.0.60", "port": int64(80), "successes": int64(1), "lastSuccessAt": now.UTC().Format(time.RFC3339)})
	_ = unstructured.SetNestedSlice(nro.Object, pods, "status", "pods")
	return append(objs, service("shop", "ledger", "10.0.0.60"), pactoCR("shop", "ledger", "ledger", "ledger", "oci://reg.io/acme/contracts/ledger", "2.3.1", 1, 1))
}

// TestViewerHiddenDependencyMakesRuntimePartial: a workload that called a
// Service the viewer may not read has every visible call partial, and its
// runtime-calls row says how many it called.
func TestViewerHiddenDependencyMakesRuntimePartial(t *testing.T) {
	now := time.Now()
	c := ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }, Visible: onlyShop}
	b, err := Cluster(context.Background(), fakeCluster(viewerLedger(now)...), Options{Plan: true}, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := result(t, b, "invoices").Inferred
	if f := recordField(t, rec, "dependencies[ledger].compatibility"); f.Completeness != "partial" {
		t.Errorf("%+v", f)
	}
	if got := rec.Coverage[len(rec.Coverage)-1]; got != (contract.InferredCoverage{Source: "runtime-calls", State: "partial", Reason: "1 called services you may not read"}) {
		t.Errorf("runtime-calls %+v", got)
	}
}

// TestClusterDraftCoverageCarried: a source the read could not list is in
// every draft's record.
func TestClusterDraftCoverageCarried(t *testing.T) {
	now := time.Now()
	cl := failing(fakeCluster(shopCluster(now)...), "endpointslices", apierrors.NewForbidden(schema.GroupResource{Resource: "endpointslices"}, "", nil))
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }}, nil)
	if err != nil || len(b.Results) != 4 {
		t.Fatalf("%+v %v", b, err)
	}
	for _, r := range b.Results {
		if !slices.Contains(r.Inferred.Coverage, contract.InferredCoverage{Source: "endpointslices", State: "failed", Reason: "forbidden"}) {
			t.Errorf("%s: %+v", r.Service, r.Inferred.Coverage)
		}
	}
}

func TestClusterDraftHiddenCarried(t *testing.T) {
	now := time.Now()
	c := ClusterOptions{Out: t.TempDir(), Now: func() time.Time { return now }, Visible: onlyShop}
	b, err := Cluster(context.Background(), fakeCluster(viewerShop(now)...), Options{Plan: true}, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h := result(t, b, "invoices").Inferred.Hidden; h == nil || *h != (contract.InferredHidden{Services: 1, Callers: 2}) {
		t.Fatalf("%+v", h)
	}
}

func TestClusterTemplateRefused(t *testing.T) {
	opts := Options{Dir: t.TempDir(), Owner: "shop", Plan: true, Template: &Template{FS: goldenPath("")}, Cluster: &discover.ClusterFacts{}}
	if _, err := Init(context.Background(), opts, nil); err == nil || !strings.Contains(err.Error(), "drop --cluster") {
		t.Fatalf("%v", err)
	}
}

// literalShop is checkout setting a password in its env, reading another
// from a ConfigMap, and passing secrets as args in the =, split and sh -c
// forms, one of them in an init container.
func literalShop() runtime.Object {
	app := ctr("checkout", []any{"/bin/sh", "-c"}, []any{"serve --client-secret=v4lues-s3cret --port=8080"},
		append(env("DB_PASSWORD", "hunter2"), map[string]any{"name": "LOG_LEVEL", "valueFrom": map[string]any{"configMapKeyRef": map[string]any{"name": "checkout", "key": "level"}}}))
	sidecar := ctr("audit", []any{"audit"}, []any{"--api-key", "l0g-l1teral"}, nil)
	migrate := ctr("migrate", nil, []any{"--token=t0ken-arg"}, nil)
	return obj("apps/v1", "Deployment", "shop", "checkout", map[string]any{"metadata": map[string]any{"labels": map[string]any{"team": "shop"}},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{app, sidecar}, "initContainers": []any{migrate}}}}})
}

var fixtureLiterals = []string{"hunter2", "v4lues-s3cret", "l0g-l1teral", "t0ken-arg"}

// TestClusterNoLiteralReachesContractOrRecord: no literal of the workload's
// env or args reaches a file the draft writes or what it reports.
func TestClusterNoLiteralReachesContractOrRecord(t *testing.T) {
	out := t.TempDir()
	b, err := Cluster(context.Background(), fakeCluster(literalShop()), Options{}, ClusterOptions{Out: out}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	r := result(t, b, "checkout")
	raw, _ := json.Marshal(b)
	texts := []string{string(raw)}
	for _, name := range []string{"pacto.yaml", contract.InferredRecordFile, "manifests.yaml", configSchema} {
		data, err := os.ReadFile(filepath.Join(r.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, string(data))
	}
	for _, text := range texts {
		for _, l := range fixtureLiterals {
			if strings.Contains(text, l) {
				t.Errorf("%q in:\n%s", l, text)
			}
		}
	}
	if !strings.Contains(texts[4], `"DB_PASSWORD"`) || !strings.Contains(texts[3], "$(CLIENT_SECRET)") {
		t.Errorf("the keys are gone:\n%s\n%s", texts[4], texts[3])
	}
}

// TestCompleteClusterNothingOrUnreadable: a folder and a read showing
// nothing plan no record; an unreadable folder fails.
func TestCompleteClusterNothingOrUnreadable(t *testing.T) {
	facts := &discover.ClusterFacts{Object: "shop/Deployment/x", AsOf: time.Now(), CallWindow: time.Hour}
	p := &planner{r: &Result{Dir: t.TempDir()}, opts: Options{Cluster: facts}}
	if err := p.completeCluster(nil, nil, contract.InferredInputs{}); err != nil || p.r.Inferred != nil || len(p.r.Files) != 0 {
		t.Fatalf("%v %+v", err, p.r)
	}
	gone := writeTree(t, t.TempDir(), map[string]string{"file": "x"}) + "/file"
	p = &planner{r: &Result{Dir: gone}, opts: Options{Cluster: facts}}
	if err := p.completeCluster(nil, nil, contract.InferredInputs{}); err == nil || !strings.Contains(err.Error(), "reading "+gone) {
		t.Fatalf("%v", err)
	}
}

// TestReportedVersionsBounded: the versions a fleet reported keep a source
// within the record's bound, dropping the highest.
func TestReportedVersionsBounded(t *testing.T) {
	var vs []string
	for i := range 40 {
		vs = append(vs, fmt.Sprintf("1.%d.0", i))
	}
	got := reportedVersions(vs)
	if len("cluster:fleet reported versions "+got) > 256 || !strings.HasPrefix(got, "1.0.0, 1.1.0") || strings.HasSuffix(got, "1.39.0") {
		t.Fatalf("%q", got)
	}
	if reportedVersions([]string{"2.3.1", "2.4.0"}) != "2.3.1, 2.4.0" {
		t.Fatal("short list changed")
	}
}

// runtimeRow is the runtime-calls row of r's record.
func runtimeRow(r *Result) contract.InferredCoverage {
	return r.Inferred.Coverage[len(r.Inferred.Coverage)-1]
}

// TestClusterNoRunningPodNeverCallsNothing: a workload none of whose pods
// ran on a node, or whose pods could not be tied to it, reads its runtime
// calls as partial and says why, never as a complete "calls nothing".
func TestClusterNoRunningPodNeverCallsNothing(t *testing.T) {
	now := time.Now()
	scaled := slices.DeleteFunc(invoicesShop(now), func(o runtime.Object) bool { return o.(*unstructured.Unstructured).GetKind() == "Pod" })
	r := draftInvoices(t, now, t.TempDir(), true, scaled...)
	if runtimeRow(r) != (contract.InferredCoverage{Source: "runtime-calls", State: "partial", Reason: "no running pods observed"}) ||
		!hasLine(r.Unknown, "no pod of the workload was running on a node when the cluster was read, so no calls were seen") {
		t.Fatalf("scaled to 0: %+v %v", runtimeRow(r), r.Unknown)
	}
	c := ClusterOptions{Namespaces: []string{"shop"}, Out: t.TempDir(), Now: func() time.Time { return now }}
	cl := failing(fakeCluster(invoicesShop(now)...), "replicasets", apierrors.NewForbidden(schema.GroupResource{Resource: "replicasets"}, "", errors.New("no")))
	b, err := Cluster(context.Background(), cl, Options{Plan: true}, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtimeRow(result(t, b, "invoices")); got != (contract.InferredCoverage{Source: "runtime-calls", State: "partial", Reason: "the pods were not all read"}) {
		t.Fatalf("replicasets forbidden: %+v", got)
	}
}

// TestClusterPactosCutNeverSilentDraft: when the Pactos could not all be
// read, a workload a Pacto describes may be drafted, and its draft says that
// whether a Pacto describes it is unknown; read in full, it is skipped.
func TestClusterPactosCutNeverSilentDraft(t *testing.T) {
	now := time.Now()
	objs := append(invoicesShop(now), obj("pacto.trianalab.io/v1alpha1", "Pacto", "shop", "invoices", map[string]any{
		"spec": map[string]any{"target": map[string]any{"serviceName": "invoices", "workloadRef": map[string]any{"name": "invoices", "kind": "Deployment"}}}}))
	c := ClusterOptions{Namespaces: []string{"shop"}, Out: t.TempDir(), Now: func() time.Time { return now }}
	b, err := Cluster(context.Background(), fakeCluster(objs...), Options{Plan: true}, c, nil)
	if err != nil || !slices.Equal(b.Skipped, []string{"shop/Deployment/invoices"}) {
		t.Fatalf("read in full: %+v: %v", b, err)
	}
	cl := failing(fakeCluster(objs...), "pactos", apierrors.NewForbidden(schema.GroupResource{Resource: "pactos"}, "", errors.New("no")))
	if b, err = Cluster(context.Background(), cl, Options{Plan: true}, c, nil); err != nil {
		t.Fatal(err)
	}
	if r := result(t, b, "invoices"); !hasLine(r.Unknown, "whether a Pacto already describes this workload: the Pacto list was failed (forbidden), so this draft may duplicate a contract") {
		t.Fatalf("%v", r.Unknown)
	}
}
