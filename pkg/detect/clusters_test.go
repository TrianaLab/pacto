package detect

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestClustersWireKeys pins the JSON keys of clusters.json, which the
// operator writes and older readers ignore.
func TestClustersWireKeys(t *testing.T) {
	c := Clusters{SchemaVersion: SchemaVersion, AsOf: time.Unix(0, 0).UTC(), Cluster: "uid", Total: 1,
		Sources: []InventorySource{{Source: "flux", Completeness: Partial, Reason: "labels not read", Namespaces: []string{"apps"}}},
		Candidates: []Candidate{{Source: "flux", Kind: "Secret", Namespace: "apps", Name: "eu", DisplayName: "eu",
			Labels: map[string]string{"env": "prod"}, UID: "u", UIDSource: "s", KubeConfig: &KubeConfigRef{Kind: "Secret", Namespace: "apps", Name: "eu", Key: "value"}}}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schemaVersion":"1","asOf":"1970-01-01T00:00:00Z","cluster":"uid",` +
		`"sources":[{"source":"flux","completeness":"partial","reason":"labels not read","namespaces":["apps"]}],` +
		`"candidates":[{"source":"flux","kind":"Secret","namespace":"apps","name":"eu","displayName":"eu","labels":{"env":"prod"},` +
		`"uid":"u","uidSource":"s","kubeConfig":{"kind":"Secret","namespace":"apps","name":"eu","key":"value"}}],"total":1}`
	if string(data) != want {
		t.Errorf("clusters.json = %s", data)
	}
}

const uid1 = "9f1c4e2a-0000-4000-8000-000000000001"

// decodeClusters reads what EncodeClusters wrote.
func decodeClusters(t *testing.T, b []byte) Clusters {
	t.Helper()
	var c Clusters
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClustersEncodeRoundTripsThroughJSON(t *testing.T) {
	in := Clusters{AsOf: asOf, Cluster: "hub", Total: 1, Sources: []InventorySource{{Source: SourceCAPI, Completeness: Complete}},
		Candidates: []Candidate{{Source: SourceCAPI, Kind: "Cluster", Namespace: "fleet", Name: "eu", DisplayName: "eu",
			Labels: map[string]string{"env": "prod"}, UID: uid1, UIDSource: "annotation " + ClusterUIDAnnotation}}}
	b, err := EncodeClusters(in)
	if err != nil {
		t.Fatal(err)
	}
	in.SchemaVersion = SchemaVersion
	if got := decodeClusters(t, b); !reflect.DeepEqual(got, in) {
		t.Errorf("round trip = %+v, want %+v", got, in)
	}
}

func TestClustersCleansAndCaps(t *testing.T) {
	labels := map[string]string{}
	for i := range 40 {
		labels[fmt.Sprintf("k%02d", i)] = "v\u202e"
	}
	long := strings.Repeat("n", 300)
	b, err := EncodeClusters(Clusters{Cluster: "hub\x07", Sources: make([]InventorySource, 12),
		Candidates: []Candidate{{Source: "argocd\u2066", Name: long, Labels: labels, KubeConfig: &KubeConfigRef{Kind: "Secret", Name: "x\x1b", Key: "k\x1b"}}}})
	if err != nil {
		t.Fatal(err)
	}
	c := decodeClusters(t, b)
	cand := c.Candidates[0]
	if c.Cluster != "hub" || cand.Source != "argocd" || len([]rune(cand.Name)) != MaxRunes || len(cand.Labels) != MaxCandidateLabels ||
		cand.Labels["k00"] != "v" || cand.Labels["k16"] != "" || cand.KubeConfig.Name != "x" || cand.KubeConfig.Key != "k" || len(c.Sources) != maxSources {
		t.Errorf("cleaned = %+v", c)
	}
}

// many is n Argo CD cluster Secrets.
func many(n int) []Meta {
	out := make([]Meta, n)
	for i := range out {
		out[i] = Meta{Kind: "Secret", Namespace: "argocd", Name: fmt.Sprintf("c%03d", i), Labels: map[string]string{"pad": strings.Repeat("x", 200)}}
	}
	return out
}

func TestInventoryCutIsPartial(t *testing.T) {
	c, e := Inventory(InventoryInput{Sources: []string{SourceArgoCD, SourceCAPI, SourceFlux}, Granted: []string{"argocd"}, Argo: many(250),
		CAPI: []Meta{{Kind: "Cluster", Name: "z"}}})
	if len(c.Candidates) != MaxCandidates || c.Total != 251 {
		t.Fatalf("kept %d of %d", len(c.Candidates), c.Total)
	}
	if s := c.Sources[0]; s.Completeness != Partial || s.Reason != "listed the first 200 of 250" {
		t.Errorf("argocd = %+v", s)
	}
	if s := c.Sources[1]; s.Completeness != Partial || s.Reason != "listed the first 0 of 1" {
		t.Errorf("capi = %+v", s)
	}
	if e.Completeness != Partial || e.Value != "251 candidate clusters" {
		t.Errorf("entry = %+v", e)
	}
}

// TestInventoryByteCutReachesEntry: a source the byte bound cuts reads
// partial in the cluster-inventory entry, never complete.
func TestInventoryByteCutReachesEntry(t *testing.T) {
	argo := many(MaxCandidates)
	for i := range argo {
		for j := range MaxCandidateLabels {
			argo[i].Labels[fmt.Sprint("l", j)] = strings.Repeat("y", 200)
		}
	}
	c, e := Inventory(InventoryInput{Sources: []string{SourceArgoCD}, Granted: []string{"argocd"}, Argo: argo})
	if len(c.Candidates) == MaxCandidates || c.Sources[0].Completeness != Partial || e.Completeness != Partial ||
		!strings.Contains(e.Reason, "listed the first ") {
		t.Errorf("%d candidates, sources %+v, entry %+v", len(c.Candidates), c.Sources, e)
	}
	if b, _ := EncodeClusters(c); len(decodeClusters(t, b).Candidates) != len(c.Candidates) {
		t.Error("encoding cuts again what Inventory already bounded")
	}
}

func TestClustersFitByteBound(t *testing.T) {
	c, _ := Inventory(InventoryInput{Sources: []string{SourceArgoCD}, Granted: []string{"argocd"}, Argo: many(200)})
	for i := range c.Candidates {
		c.Candidates[i].Labels = map[string]string{}
		for j := range MaxCandidateLabels {
			c.Candidates[i].Labels[fmt.Sprint("l", j)] = strings.Repeat("y", 200)
		}
	}
	b, err := EncodeClusters(c)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeClusters(t, b)
	if len(b) > MaxClustersBytes || len(got.Candidates) == MaxCandidates || got.Total != 200 ||
		got.Sources[0].Completeness != Partial || !strings.HasPrefix(got.Sources[0].Reason, "listed the first ") {
		t.Errorf("%d bytes, %d candidates, %+v", len(b), len(got.Candidates), got.Sources)
	}
	// Cut twice (by count in Inventory, by bytes here), one note keeps the total found.
	c, _ = Inventory(InventoryInput{Sources: []string{SourceArgoCD}, Granted: []string{"argocd"}, Argo: many(250)})
	c.Sources[0].Reason = "labels not read; " + c.Sources[0].Reason
	for i := range c.Candidates {
		c.Candidates[i].Labels = map[string]string{}
		for j := range MaxCandidateLabels {
			c.Candidates[i].Labels[fmt.Sprint("l", j)] = strings.Repeat("y", 200)
		}
	}
	b, _ = EncodeClusters(c)
	got = decodeClusters(t, b)
	if want := fmt.Sprintf("labels not read; listed the first %d of 250", len(got.Candidates)); got.Sources[0].Reason != want || got.Total != 250 {
		t.Errorf("reason = %q, want %q", got.Sources[0].Reason, want)
	}
	if b, _ := EncodeClusters(Clusters{Candidates: []Candidate{{Name: strings.Repeat("x", 10)}}}); len(b) == 0 {
		t.Error("a small document encodes")
	}
}

func TestInventoryArgoFromLabelledSecrets(t *testing.T) {
	c, e := Inventory(InventoryInput{Sources: []string{SourceArgoCD}, Granted: []string{"argocd"},
		Argo: []Meta{{Kind: "Secret", Namespace: "argocd", Name: "eu-prod", Labels: map[string]string{argoSecretType: "cluster", "env": "prod"}},
			{Kind: "Secret", Namespace: "argocd", Name: "bare", Labels: map[string]string{argoSecretType: "cluster"}}}})
	want := []Candidate{{Source: SourceArgoCD, Kind: "Secret", Namespace: "argocd", Name: "bare", DisplayName: "bare"},
		{Source: SourceArgoCD, Kind: "Secret", Namespace: "argocd", Name: "eu-prod", DisplayName: "eu-prod", Labels: map[string]string{"env": "prod"}}}
	if !reflect.DeepEqual(c.Candidates, want) {
		t.Errorf("candidates = %+v", c.Candidates)
	}
	if e.Completeness != Complete || !reflect.DeepEqual(e.Details, []string{"argocd: 2 candidates"}) || !reflect.DeepEqual(c.Sources[0].Namespaces, []string{"argocd"}) {
		t.Errorf("entry = %+v, sources = %+v", e, c.Sources)
	}
	if c, _ := Inventory(InventoryInput{Sources: []string{SourceArgoCD}}); c.Sources[0].Completeness != Unknown {
		t.Errorf("no granted namespace: %+v", c.Sources)
	}
}

func TestInventoryFluxFromKubeConfigRefs(t *testing.T) {
	refs := []KubeConfigRef{{Kind: "Secret", Namespace: "apps", Name: "eu", Key: "kubeconfig"}, {Kind: "Secret", Namespace: "apps", Name: "eu", Key: "kubeconfig"},
		{Kind: "ConfigMap", Namespace: "apps", Name: "us"}, {Kind: "Secret", Namespace: "other", Name: "ap"}}
	c, _ := Inventory(InventoryInput{Sources: []string{SourceFlux}, Granted: []string{"apps"}, FluxRefs: refs,
		Secrets: map[string]Meta{"apps/eu": {Labels: map[string]string{"env": "prod"}, Annotations: map[string]string{ClusterUIDAnnotation: uid1}}}})
	if len(c.Candidates) != 3 {
		t.Fatalf("candidates = %+v", c.Candidates)
	}
	eu := c.Candidates[0]
	if eu.Name != "eu" || eu.Labels["env"] != "prod" || eu.UID != uid1 || *eu.KubeConfig != refs[0] {
		t.Errorf("eu = %+v", eu)
	}
	if c.Candidates[1].Kind != "ConfigMap" || c.Candidates[2].Labels != nil {
		t.Errorf("candidates = %+v", c.Candidates)
	}
	if s := c.Sources[0]; s.Completeness != Partial || s.Reason != "labels not read in other; labels of ConfigMap kubeconfigs not read" {
		t.Errorf("flux = %+v", s)
	}
	// A failed Secret list leaves granted refs unlabelled, and says so.
	c, _ = Inventory(InventoryInput{Sources: []string{SourceFlux}, Granted: []string{"apps"}, FluxRefs: refs[:1],
		Errs: map[string]error{SourceArgoCD: errors.New("timeout")}})
	if s := c.Sources[0]; s.Completeness != Partial || s.Reason != "labels not read in apps" || c.Candidates[0].KubeConfig.Key != "kubeconfig" {
		t.Errorf("failed Secret list: %+v", c.Sources)
	}
	if c, _ := Inventory(InventoryInput{Sources: []string{SourceFlux}, Granted: []string{"apps"}, FluxRefs: refs[:1]}); c.Sources[0].Completeness != Complete {
		t.Errorf("granted, Secret absent: %+v", c.Sources)
	}
}

func TestInventoryCAPIOCMRancher(t *testing.T) {
	c, e := Inventory(InventoryInput{Sources: []string{SourceCAPI, SourceOCM, SourceRancher},
		CAPI: []Meta{{Kind: "Cluster", Namespace: "fleet", Name: "eu"}}, OCM: []Meta{{Kind: "ManagedCluster", Name: "us"}},
		Rancher: []Meta{{Kind: "Cluster", Name: "c-m-abc"}}})
	var names []string
	for _, cand := range c.Candidates {
		names = append(names, cand.Source+"/"+cand.DisplayName)
	}
	if !reflect.DeepEqual(names, []string{"capi/eu", "ocm/us", "rancher/c-m-abc"}) || e.Completeness != Complete {
		t.Errorf("candidates = %v, entry = %+v", names, e)
	}
}

func TestCandidateUIDFromAnnotation(t *testing.T) {
	got := clusterCandidate(SourceCAPI, Meta{Name: "eu", Annotations: map[string]string{ClusterUIDAnnotation: uid1}, Claims: map[string]string{"id.k8s.io": "aaaaaaaa-0000-4000-8000-000000000001"}})
	if got.UID != uid1 || got.UIDSource != "annotation pacto.trianalab.io/cluster-uid" {
		t.Errorf("candidate = %+v", got)
	}
}

func TestNonUUIDClaimIgnored(t *testing.T) {
	for _, v := range []string{"eu-prod", "9F1C4E2A-0000-4000-8000-000000000001", uid1 + " "} {
		if got := clusterCandidate(SourceOCM, Meta{Name: "eu", Annotations: map[string]string{ClusterUIDAnnotation: v}, Claims: map[string]string{"id.k8s.io": v}}); got.UID != "" {
			t.Errorf("%q gave UID %q", v, got.UID)
		}
	}
}

func TestCandidateUIDFromOCMClaim(t *testing.T) {
	if got := clusterCandidate(SourceOCM, Meta{Name: "eu", Claims: map[string]string{"id.k8s.io": uid1}}); got.UID != uid1 || got.UIDSource != "OCM cluster claim id.k8s.io" {
		t.Errorf("candidate = %+v", got)
	}
}

func TestInventoryForbiddenIsUnknown(t *testing.T) {
	forbidden := fmt.Errorf("list: %w", ErrForbidden)
	errs := map[string]error{}
	for _, s := range []string{SourceArgoCD, SourceFlux, SourceCAPI, SourceOCM, SourceRancher} {
		errs[s] = forbidden
	}
	c, e := Inventory(InventoryInput{Sources: []string{SourceArgoCD, SourceFlux, SourceCAPI, SourceOCM, SourceRancher}, Granted: []string{"argocd"}, Errs: errs})
	for _, s := range c.Sources {
		if s.Completeness != Unknown || s.Reason != "the operator may not list "+inventoryResources[s.Source] {
			t.Errorf("%s = %+v", s.Source, s)
		}
	}
	if e.Completeness != Unknown || e.Details[0] != "argocd: not read" || strings.Contains(e.Reason, "hardened") {
		t.Errorf("entry = %+v", e)
	}
	c, _ = Inventory(InventoryInput{Sources: []string{SourceCAPI}, Errs: map[string]error{SourceCAPI: forbidden}, Hardened: true})
	if c.Sources[0].Reason != "the operator may not list clusters.cluster.x-k8s.io; profile hardened renders no ClusterRole" {
		t.Errorf("hardened = %+v", c.Sources[0])
	}
	c, _ = Inventory(InventoryInput{Sources: []string{SourceCAPI}, Errs: map[string]error{SourceCAPI: errors.New("Timeout")}, Hardened: true})
	if c.Sources[0].Reason != "reading clusters.cluster.x-k8s.io failed: Timeout" {
		t.Errorf("timeout = %+v", c.Sources[0])
	}
}

func TestInventoryEntryProposesNamespaces(t *testing.T) {
	_, e := Inventory(InventoryInput{Sources: []string{SourceFlux}, Proposed: []string{"flux-system", "argocd"}})
	if e.Completeness != Partial || !reflect.DeepEqual(e.Namespaces, []string{"argocd", "flux-system"}) ||
		!strings.HasSuffix(e.Hint, "{argocd,flux-system}'") || !strings.Contains(e.Reason, "not granted in flux-system, argocd") {
		t.Errorf("entry = %+v", e)
	}
	if _, e := Inventory(InventoryInput{Granted: []string{"argocd"}, Proposed: []string{"argocd"}}); e.Hint != "" || e.Completeness != Complete {
		t.Errorf("granted entry = %+v", e)
	}
	if e := InventoryProposal(nil); e.Kind != "" {
		t.Errorf("nothing to propose: %+v", e)
	}
	if e := InventoryProposal([]string{"flux-system", "argocd", "argocd"}); e.Completeness != Partial || !reflect.DeepEqual(e.Namespaces, []string{"argocd", "flux-system"}) || e.Hint == "" {
		t.Errorf("proposal = %+v", e)
	}
}

func TestClustersRoundTrip(t *testing.T) {
	in := Clusters{AsOf: asOf, Cluster: "hub", Total: 2, Sources: []InventorySource{{Source: SourceArgoCD, Completeness: Complete, Namespaces: []string{"argocd"}}},
		Candidates: []Candidate{{Source: SourceArgoCD, Kind: "Secret", Namespace: "argocd", Name: "eu", DisplayName: "eu",
			Labels: map[string]string{"env": "prod"}, UID: uid1, UIDSource: "annotation " + ClusterUIDAnnotation},
			{Source: SourceArgoCD, Kind: "Secret", Namespace: "argocd", Name: "us", DisplayName: "us"}}}
	b, err := EncodeClusters(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeClusters(b)
	in.SchemaVersion = SchemaVersion
	if err != nil || !reflect.DeepEqual(got, in) {
		t.Errorf("round trip = %+v %v, want %+v", got, err, in)
	}
}

func TestClustersDecodeRefusesOtherMajor(t *testing.T) {
	for name, doc := range map[string]string{
		"major":    `{"schemaVersion":"2","asOf":"2026-11-23T10:00:00Z"}`,
		"json":     `{`,
		"asOf":     `{"schemaVersion":"1"}`,
		"oversize": `{"schemaVersion":"1","asOf":"2026-11-23T10:00:00Z","cluster":"` + strings.Repeat("x", MaxClustersBytes) + `"}`,
	} {
		if _, err := DecodeClusters([]byte(doc)); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
	if c, err := DecodeClusters([]byte(`{"schemaVersion":"1.3","asOf":"2026-11-23T10:00:00Z"}`)); err != nil || c.Total != 0 {
		t.Errorf("minor = %+v %v", c, err)
	}
}

// TestClustersDecodeCleans: a hand-edited document loses unknown sources and
// UIDs that are not cluster UIDs, and is capped again.
func TestClustersDecodeCleans(t *testing.T) {
	var cands []string
	for i := range MaxCandidates + 5 {
		cands = append(cands, fmt.Sprintf(`{"source":"capi","kind":"Cluster","name":"c%03d","uid":"eu-prod","uidSource":"forged"}`, i))
	}
	doc := `{"schemaVersion":"1","asOf":"2026-11-23T10:00:00Z","sources":[{"source":"capi","completeness":"complete"}],"candidates":[` +
		`{"source":"git","name":"x"},` + strings.Join(cands, ",") + `]}`
	c, err := DecodeClusters([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Candidates) != MaxCandidates || c.Total != MaxCandidates+5 || c.Candidates[0].Source != SourceCAPI || c.Candidates[0].UID != "" ||
		c.Candidates[0].UIDSource != "" || c.Sources[0].Completeness != Partial {
		t.Errorf("decoded = %d candidates, total %d, %+v, %+v", len(c.Candidates), c.Total, c.Candidates[0], c.Sources)
	}
}

// TestClustersFromKeepsOneSource: From keeps a source's candidates in order.
func TestClustersFromKeepsOneSource(t *testing.T) {
	c := Clusters{Candidates: []Candidate{{Source: SourceArgoCD, Name: "a"}, {Source: SourceCAPI, Name: "b"}, {Source: SourceArgoCD, Name: "c"}}}
	if got := c.From(SourceArgoCD); len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Errorf("From(argocd) = %+v", got)
	}
	if got := c.From(SourceFlux); got != nil {
		t.Errorf("From(flux) = %+v", got)
	}
}

// TestArgoSourcePartialWhenAControlPlaneNamespaceIsNotGranted: a granted
// namespace other than Argo CD's own leaves its cluster Secrets unread, so
// the argocd source itself is partial, as is a failed control-plane read.
func TestArgoSourcePartialWhenAControlPlaneNamespaceIsNotGranted(t *testing.T) {
	c, _ := Inventory(InventoryInput{Sources: []string{SourceArgoCD}, Granted: []string{"flux-system"}, ArgoControl: []string{"argocd", "argocd"}})
	if s := c.Sources[0]; s.Completeness != Partial || s.Reason != "Secret reads are not granted in argocd" || !reflect.DeepEqual(s.ControlPlane, []string{"argocd"}) {
		t.Errorf("ungranted = %+v", s)
	}
	c, _ = Inventory(InventoryInput{Sources: []string{SourceArgoCD}, Granted: []string{"argocd"}, ArgoControlErr: errors.New("Forbidden")})
	if s := c.Sources[0]; s.Completeness != Partial || !strings.Contains(s.Reason, "control-plane namespaces could not be read: Forbidden") {
		t.Errorf("control plane unread = %+v", s)
	}
	if c, _ := Inventory(InventoryInput{Sources: []string{SourceArgoCD}, Granted: []string{"argocd"}, ArgoControl: []string{"argocd"}}); c.Sources[0].Completeness != Complete {
		t.Errorf("granted = %+v", c.Sources[0])
	}
}

// TestFailedSecretNamespaceKeepsTheOthers: one granted namespace whose list
// failed leaves the Secrets read elsewhere, marks argocd partial naming it and
// leaves the Flux refs there unlabelled.
func TestFailedSecretNamespaceKeepsTheOthers(t *testing.T) {
	c, _ := Inventory(InventoryInput{Sources: []string{SourceArgoCD, SourceFlux}, Granted: []string{"argocd", "flux-system"},
		Argo:       []Meta{{Kind: "Secret", Namespace: "argocd", Name: "eu"}},
		SecretErrs: map[string]error{"flux-system": errors.New("Forbidden")},
		FluxRefs:   []KubeConfigRef{{Kind: "Secret", Namespace: "flux-system", Name: "us"}}})
	argo, _ := c.Source(SourceArgoCD)
	flux, _ := c.Source(SourceFlux)
	if len(c.From(SourceArgoCD)) != 1 || argo.Completeness != Partial || argo.Reason != "Secrets not read in flux-system: Forbidden" ||
		flux.Completeness != Partial || flux.Reason != "labels not read in flux-system" {
		t.Errorf("sources = %+v, candidates = %+v", c.Sources, c.Candidates)
	}
	if _, ok := c.Source(SourceCAPI); ok {
		t.Error("an unread source is found")
	}
}

// TestScopedInstallLeavesArgoAndFluxPartial: a namespace-list install reads
// Argo CD and Flux only in the watched namespaces.
func TestScopedInstallLeavesArgoAndFluxPartial(t *testing.T) {
	c, e := Inventory(InventoryInput{Sources: []string{SourceFlux, SourceArgoCD, SourceCAPI}, Granted: []string{"argocd"}, Scoped: "read only the watched namespaces"})
	for _, s := range c.Sources {
		if want := s.Source != SourceCAPI; (s.Completeness == Partial) != want || want && s.Reason != "read only the watched namespaces" {
			t.Errorf("%s = %+v", s.Source, s)
		}
	}
	if e.Completeness != Partial {
		t.Errorf("entry = %+v", e)
	}
}

// TestInventoryEntryOfClusterWideReadsIsClusterScoped: Cluster API, OCM and
// Rancher counts come from cluster-wide reads, so the row is cluster-scoped
// and restriction hides its details; Secret and Flux reads alone are not.
func TestInventoryEntryOfClusterWideReadsIsClusterScoped(t *testing.T) {
	for _, src := range []string{SourceCAPI, SourceOCM, SourceRancher} {
		if _, e := Inventory(InventoryInput{Sources: []string{SourceArgoCD, src}, Granted: []string{"argocd"}}); e.Scope != fleet.ScopeCluster {
			t.Errorf("%s: scope %q", src, e.Scope)
		}
	}
	if _, e := Inventory(InventoryInput{Sources: []string{SourceArgoCD, SourceFlux}, Granted: []string{"argocd"}}); e.Scope != "" {
		t.Errorf("namespaced reads: scope %q", e.Scope)
	}
}

// TestLabelCapKeepsEnvironmentLabelsAndSaysSo: an environment label survives
// the cap even when 16 others sort first, and the source says labels were cut.
func TestLabelCapKeepsEnvironmentLabelsAndSaysSo(t *testing.T) {
	labels := map[string]string{"env": "prod"}
	for i := range MaxCandidateLabels + 2 {
		labels[fmt.Sprintf("app.kubernetes.io/l%02d", i)] = "x"
	}
	c, _ := Inventory(InventoryInput{Sources: []string{SourceArgoCD}, Granted: []string{"argocd"},
		Argo: []Meta{{Kind: "Secret", Namespace: "argocd", Name: "eu", Labels: labels}, {Kind: "Secret", Namespace: "argocd", Name: "us"}}})
	got := c.From(SourceArgoCD)[0].Labels
	if len(got) != MaxCandidateLabels || got["env"] != "prod" {
		t.Errorf("labels = %v", got)
	}
	if s := c.Sources[0]; s.Completeness != Partial || s.Reason != "labels cut to 16 on 1 candidates" {
		t.Errorf("source = %+v", s)
	}
	b, _ := EncodeClusters(c)
	if again := decodeClusters(t, b); again.Sources[0].Reason != "labels cut to 16 on 1 candidates" {
		t.Errorf("re-encoded = %+v", again.Sources[0])
	}
}
