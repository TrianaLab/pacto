package detect

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ClustersConfigMapKey is the key of the operator's detected ConfigMap that
// holds the candidate clusters, beside [ConfigMapKey].
const ClustersConfigMapKey = "clusters.json"

// Bounds of a Clusters document: candidates kept, labels per candidate and
// the encoded size.
const (
	MaxCandidates      = 200
	MaxCandidateLabels = 16
	MaxClustersBytes   = 192 << 10
)

// The inventory sources a candidate can come from.
const (
	SourceArgoCD  = "argocd"
	SourceFlux    = "flux"
	SourceCAPI    = "capi"
	SourceOCM     = "ocm"
	SourceRancher = "rancher"
)

// inventoryKind is the detection entry kind that reports the inventory read.
const inventoryKind = "cluster-inventory"

// Clusters is one inventory read: the clusters the hub's GitOps and cluster
// managers know. Every candidate is inferred; nothing joins on its own.
type Clusters struct {
	SchemaVersion string    `json:"schemaVersion"`
	AsOf          time.Time `json:"asOf"`
	// Cluster is the id of the cluster the operator read.
	Cluster    string            `json:"cluster,omitempty"`
	Sources    []InventorySource `json:"sources"`
	Candidates []Candidate       `json:"candidates"`
	// Total is how many candidates were found before the list was cut.
	Total int `json:"total"`
}

// From are the candidates of c from source.
func (c Clusters) From(source string) []Candidate {
	var out []Candidate
	for _, cand := range c.Candidates {
		if cand.Source == source {
			out = append(out, cand)
		}
	}
	return out
}

// InventorySource is how one source was read: completeness, why it is short
// of complete and the namespaces it was read in.
type InventorySource struct {
	Source       string   `json:"source"`
	Completeness string   `json:"completeness"`
	Reason       string   `json:"reason,omitempty"`
	Namespaces   []string `json:"namespaces,omitempty"`
}

// Candidate is one cluster an inventory source knows. UID is set only when
// the source object states one, and UIDSource says where.
type Candidate struct {
	Source      string            `json:"source"`
	Kind        string            `json:"kind"`
	Namespace   string            `json:"namespace,omitempty"`
	Name        string            `json:"name"`
	DisplayName string            `json:"displayName,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	UID         string            `json:"uid,omitempty"`
	UIDSource   string            `json:"uidSource,omitempty"`
	// KubeConfig is the kubeconfig Secret or ConfigMap a Flux object names.
	KubeConfig *Object `json:"kubeConfig,omitempty"`
}

// Where a candidate's UID can be stated: the annotation a person or their
// own tooling writes on the source object, or an OCM cluster claim.
const (
	ClusterUIDAnnotation = "pacto.trianalab.io/cluster-uid"
	ocmIDClaim           = "id.k8s.io"
)

// argoSecretType is the label every Argo CD cluster Secret carries, dropped
// from a candidate's labels since it tells nothing apart.
const argoSecretType = "argocd.argoproj.io/secret-type"

// ErrForbidden marks a source the operator may not list; the operator wraps
// a Forbidden answer with it, so this package stays free of Kubernetes.
var ErrForbidden = errors.New("forbidden")

// inventoryResources name what each source lists, in the unknown reason.
var inventoryResources = map[string]string{
	SourceArgoCD:  "secrets",
	SourceFlux:    "Flux Kustomizations and HelmReleases",
	SourceCAPI:    "clusters.cluster.x-k8s.io",
	SourceOCM:     "managedclusters.cluster.open-cluster-management.io",
	SourceRancher: "clusters.management.cattle.io",
}

// Meta is the metadata of one listed object; Claims holds only OCM cluster
// claims. Nothing else of an object is kept.
type Meta struct {
	Kind, Namespace, Name, DisplayName string
	Labels, Annotations, Claims        map[string]string
}

// InventoryInput is what one inventory read found. Sources are the sources
// read (their API is served); Errs holds why a source failed, keyed by
// source. Argo holds the labelled cluster Secrets of the Granted namespaces
// and Secrets every Secret of them by namespace/name; FluxRefs are the
// kubeconfig refs of Flux objects. Proposed are the namespaces the operator
// would read Secrets in.
type InventoryInput struct {
	AsOf               time.Time
	Cluster            string
	Sources            []string
	Granted            []string
	Argo               []Meta
	FluxRefs           []Object
	Secrets            map[string]Meta
	CAPI, OCM, Rancher []Meta
	Errs               map[string]error
	Proposed           []string
	Hardened           bool
}

// Inventory builds the candidates of in and the cluster-inventory entry that
// reports the read. A source that failed is unknown, one with labels left
// unread or a cut list is partial, and the entry is as short of complete as
// its worst source, or partial when a proposed namespace is not granted.
func Inventory(in InventoryInput) (Clusters, Entry) {
	c := Clusters{SchemaVersion: SchemaVersion, AsOf: in.AsOf, Cluster: in.Cluster}
	for _, src := range in.Sources {
		s := InventorySource{Source: src, Completeness: Complete}
		if err := in.Errs[src]; err != nil {
			s.Completeness, s.Reason = Unknown, unreadReason(src, err, in.Hardened)
		} else {
			cs, why := sourceCandidates(src, in)
			c.Candidates = append(c.Candidates, cs...)
			if why != "" {
				s.Completeness, s.Reason = Partial, why
			}
		}
		if src == SourceArgoCD {
			s.Namespaces = slices.Clone(in.Granted)
			if len(in.Granted) == 0 && s.Completeness != Unknown {
				s.Completeness, s.Reason = Unknown, "no namespace is granted for Secret reads"
			}
		}
		c.Sources = append(c.Sources, s)
	}
	sortCandidates(c.Candidates)
	c.Total = len(c.Candidates)
	cutCandidates(&c, MaxCandidates)
	return c, inventoryEntry(c, in)
}

// sourceCandidates are the candidates src gave and why its read is partial.
func sourceCandidates(src string, in InventoryInput) ([]Candidate, string) {
	var metas []Meta
	switch src {
	case SourceArgoCD:
		metas = in.Argo
	case SourceFlux:
		return fluxCandidates(in)
	case SourceCAPI:
		metas = in.CAPI
	case SourceOCM:
		metas = in.OCM
	case SourceRancher:
		metas = in.Rancher
	}
	out := make([]Candidate, 0, len(metas))
	for _, m := range metas {
		out = append(out, clusterCandidate(src, m))
	}
	return out, ""
}

// fluxCandidates are the distinct kubeconfig refs, labelled from the Secret
// they name when its namespace is granted.
func fluxCandidates(in InventoryInput) ([]Candidate, string) {
	var out []Candidate
	seen := map[Object]bool{}
	var unread []string
	for _, ref := range in.FluxRefs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		m, ok := in.Secrets[ref.Namespace+"/"+ref.Name]
		if ref.Kind != "Secret" || !ok {
			m = Meta{}
			if !slices.Contains(in.Granted, ref.Namespace) {
				unread = append(unread, ref.Namespace)
			}
		}
		m.Kind, m.Namespace, m.Name = ref.Kind, ref.Namespace, ref.Name
		cand := clusterCandidate(SourceFlux, m)
		cand.KubeConfig = &Object{Kind: ref.Kind, Namespace: ref.Namespace, Name: ref.Name}
		out = append(out, cand)
	}
	if slices.Sort(unread); len(unread) > 0 {
		return out, "labels not read in " + strings.Join(slices.Compact(unread), ", ")
	}
	return out, ""
}

// clusterCandidate is m as a candidate of src: its display name is its name, its
// labels are capped and its UID is set only when the object states one.
func clusterCandidate(src string, m Meta) Candidate {
	c := Candidate{Source: src, Kind: m.Kind, Namespace: m.Namespace, Name: m.Name, DisplayName: cmp.Or(m.DisplayName, m.Name)}
	for k, v := range m.Labels {
		if k == argoSecretType {
			continue
		}
		if c.Labels == nil {
			c.Labels = map[string]string{}
		}
		c.Labels[k] = v
	}
	switch {
	case uuid.MatchString(m.Annotations[ClusterUIDAnnotation]):
		c.UID, c.UIDSource = m.Annotations[ClusterUIDAnnotation], "annotation "+ClusterUIDAnnotation
	case uuid.MatchString(m.Claims[ocmIDClaim]):
		c.UID, c.UIDSource = m.Claims[ocmIDClaim], "OCM cluster claim "+ocmIDClaim
	}
	return c
}

// uuid is a cluster UID as Kubernetes writes it: a lower-case UUID.
var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// unreadReason says why src was not read: a Forbidden list names what the
// operator may not list, and the hardened profile only when it holds.
func unreadReason(src string, err error, hardened bool) string {
	if !errors.Is(err, ErrForbidden) {
		return "reading " + inventoryResources[src] + " failed: " + err.Error()
	}
	why := "the operator may not list " + inventoryResources[src]
	if hardened {
		why += "; profile hardened renders no ClusterRole"
	}
	return why
}

// inventoryEntry is the one detection row of an inventory read.
func inventoryEntry(c Clusters, in InventoryInput) Entry {
	e := Entry{Kind: inventoryKind, Completeness: Complete, Value: fmt.Sprintf("%d candidate clusters", c.Total), Namespaces: slices.Clone(in.Granted)}
	var reasons []string
	for _, s := range c.Sources {
		e.Details = append(e.Details, sourceDetail(s, c.Candidates))
		if s.Completeness == Unknown || s.Completeness == Partial && e.Completeness == Complete {
			e.Completeness = s.Completeness
		}
		if s.Reason != "" {
			reasons = append(reasons, s.Source+": "+s.Reason)
		}
	}
	if ungranted := slices.DeleteFunc(slices.Clone(in.Proposed), func(ns string) bool { return slices.Contains(in.Granted, ns) }); len(ungranted) > 0 {
		e.Namespaces = append(e.Namespaces, ungranted...)
		slices.Sort(e.Namespaces)
		e.Namespaces = slices.Compact(e.Namespaces)
		e.Hint = InventoryHint(e.Namespaces)
		if e.Completeness == Complete {
			e.Completeness = Partial
		}
		reasons = append(reasons, "Secret reads are not granted in "+strings.Join(ungranted, ", "))
	}
	e.Details = capped(e.Details, MaxDetails)
	e.Reason = strings.Join(reasons, "; ")
	return e
}

// sourceDetail is one entry detail: a source and how many candidates it gave.
func sourceDetail(s InventorySource, cs []Candidate) string {
	if s.Completeness == Unknown {
		return s.Source + ": not read"
	}
	n := 0
	for _, c := range cs {
		if c.Source == s.Source {
			n++
		}
	}
	return fmt.Sprintf("%s: %d candidates", s.Source, n)
}

// InventoryProposal is the entry of an operator whose inventory is off: the
// namespaces it would read Secrets in, for a person to grant. It has no kind
// when there is nothing to propose.
func InventoryProposal(proposed []string) Entry {
	if len(proposed) == 0 {
		return Entry{}
	}
	ns := slices.Clone(proposed)
	slices.Sort(ns)
	ns = slices.Compact(ns)
	return Entry{Kind: inventoryKind, Completeness: Partial, Value: "cluster inventory off",
		Reason: "Argo CD or Flux knows other clusters; a person grants the reads", Namespaces: ns, Hint: InventoryHint(ns)}
}

// sortCandidates sorts cs by source, namespace and name.
func sortCandidates(cs []Candidate) {
	slices.SortStableFunc(cs, func(a, b Candidate) int {
		return cmp.Or(strings.Compare(a.Source, b.Source), strings.Compare(a.Namespace, b.Namespace), strings.Compare(a.Name, b.Name))
	})
}

// cutCandidates keeps the first n candidates of c and marks every source that
// lost some partial with how many it listed.
func cutCandidates(c *Clusters, n int) {
	if len(c.Candidates) <= n {
		return
	}
	kept, found := map[string]int{}, map[string]int{}
	for i, cand := range c.Candidates {
		found[cand.Source]++
		if i < n {
			kept[cand.Source]++
		}
	}
	c.Candidates = c.Candidates[:n]
	for i := range c.Sources {
		s := &c.Sources[i]
		if kept[s.Source] == found[s.Source] {
			continue
		}
		total := found[s.Source]
		// A source cut before keeps the total it was found with.
		if m := listedNote.FindStringSubmatch(s.Reason); m != nil {
			total, _ = strconv.Atoi(m[2])
			s.Reason = strings.TrimSuffix(s.Reason, m[0])
		}
		why := fmt.Sprintf("listed the first %d of %d", kept[s.Source], total)
		if s.Completeness == Complete {
			s.Completeness = Partial
		}
		s.Reason = strings.TrimPrefix(s.Reason+"; "+why, "; ")
	}
}

// listedNote is the note cutCandidates leaves on a cut source.
var listedNote = regexp.MustCompile(`(^|; )listed the first [0-9]+ of ([0-9]+)$`)

// maxSources caps the sources of a hand-edited document.
const maxSources = 8

// EncodeClusters writes c as the operator stores it: every string cleaned,
// candidates sorted and capped at MaxCandidates, labels at
// MaxCandidateLabels, and trailing candidates dropped until the encoding
// fits MaxClustersBytes, each cut source marked partial.
func EncodeClusters(c Clusters) ([]byte, error) {
	c.SchemaVersion = SchemaVersion
	c.Sources = slices.Clone(c.Sources)
	c.Candidates = slices.Clone(c.Candidates)
	sortCandidates(c.Candidates)
	c.Total = max(c.Total, len(c.Candidates))
	sanitizeClusters(&c)
	cutCandidates(&c, MaxCandidates)
	// ponytail: one encode per dropped candidate; a binary search if 200 encodes ever show up in a profile.
	for {
		b, err := json.Marshal(c)
		if err != nil || len(b) <= MaxClustersBytes || len(c.Candidates) == 0 {
			return b, err
		}
		cutCandidates(&c, len(c.Candidates)-1)
	}
}

// sanitizeClusters cleans every string of c and caps every list.
func sanitizeClusters(c *Clusters) {
	c.Cluster = clean(c.Cluster)
	c.Sources = capped(c.Sources, maxSources)
	for i := range c.Sources {
		s := &c.Sources[i]
		s.Source, s.Completeness, s.Reason = clean(s.Source), clean(s.Completeness), clean(s.Reason)
		s.Namespaces = cleanAll(slices.Clone(s.Namespaces), MaxNamed)
	}
	for i := range c.Candidates {
		cand := &c.Candidates[i]
		for _, s := range []*string{&cand.Source, &cand.Kind, &cand.Namespace, &cand.Name, &cand.DisplayName, &cand.UID, &cand.UIDSource} {
			*s = clean(*s)
		}
		cand.Labels = cleanLabels(cand.Labels)
		if o := cand.KubeConfig; o != nil {
			cand.KubeConfig = &Object{Kind: clean(o.Kind), Namespace: clean(o.Namespace), Name: clean(o.Name)}
		}
	}
}

// cleanLabels is the first MaxCandidateLabels labels of l by key, cleaned.
func cleanLabels(l map[string]string) map[string]string {
	if len(l) == 0 {
		return nil
	}
	keys := slices.Sorted(maps.Keys(l))
	out := make(map[string]string, min(len(keys), MaxCandidateLabels))
	for _, k := range capped(keys, MaxCandidateLabels) {
		out[clean(k)] = clean(l[k])
	}
	return out
}

// DecodeClusters reads a Clusters document. One larger than
// MaxClustersBytes, of another major or with no asOf is refused; every
// string is cleaned and every list capped again, a candidate from an unknown
// source is dropped and a UID that is not a cluster UID is ignored, so a
// hand-edited ConfigMap cannot flood an answer or claim an identity.
func DecodeClusters(data []byte) (Clusters, error) {
	if len(data) > MaxClustersBytes {
		return Clusters{}, fmt.Errorf("clusters document is %d bytes, over the %d bound", len(data), MaxClustersBytes)
	}
	var c Clusters
	if err := json.Unmarshal(data, &c); err != nil {
		return Clusters{}, fmt.Errorf("decoding clusters: %w", err)
	}
	if major, _, _ := strings.Cut(c.SchemaVersion, "."); major != SchemaVersion {
		return Clusters{}, fmt.Errorf("clusters schema version %q is not %s", clean(c.SchemaVersion), SchemaVersion)
	}
	if c.AsOf.IsZero() {
		return Clusters{}, errors.New("clusters document has no asOf")
	}
	c.Candidates = slices.DeleteFunc(c.Candidates, func(cand Candidate) bool { return inventoryResources[cand.Source] == "" })
	for i := range c.Candidates {
		if cand := &c.Candidates[i]; !uuid.MatchString(cand.UID) {
			cand.UID, cand.UIDSource = "", ""
		}
	}
	c.Total = max(c.Total, len(c.Candidates))
	sanitizeClusters(&c)
	cutCandidates(&c, MaxCandidates)
	return c, nil
}
