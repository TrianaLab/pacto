package fleetsrc

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
	"github.com/trianalab/pacto/v4/pkg/workloadshape"
)

// WorkloadSource is a live [fleet.Source] over the Deployments, StatefulSets,
// DaemonSets and CronJobs of a cluster. Each becomes an inferred service
// carrying only its image, ports and probes; [fleet.Build] leaves out every
// workload a Pacto targets. It only lists, and reads no environment value,
// Secret or ConfigMap.
type WorkloadSource struct {
	id         string
	cluster    *fleet.Cluster // nil when the cluster identity could not be read
	client     k8sclient.K8sClient
	namespaces []string // sorted, deduplicated; empty means all namespaces
	promoters  []workloadmanager.Fingerprint
}

// NewWorkloadSource returns a live workload source over namespaces (empty
// for all namespaces). id is its provenance id; cluster is the cluster every
// workload is in, nil when unknown.
func NewWorkloadSource(id string, cluster *fleet.Cluster, client k8sclient.K8sClient, namespaces []string) *WorkloadSource {
	return &WorkloadSource{id: cmp.Or(id, "k8s-workloads"), cluster: cluster, client: client, namespaces: normalizeNamespaces(namespaces)}
}

// WithPromoters sets the installation's promoter fingerprints: each one found
// (a served CRD, a label or annotation on a workload read) is a promotion row.
func (s *WorkloadSource) WithPromoters(promoters []workloadmanager.Fingerprint) *WorkloadSource {
	s.promoters = promoters
	return s
}

// ID implements [fleet.Source].
func (s *WorkloadSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *WorkloadSource) Kind() string { return fleet.SourceKindKubernetesWorkloads }

// errCapped stops reading once the source holds fleet.MaxInferredServices.
var errCapped = errors.New("inferred services capped")

// Collect lists every workload kind in each configured namespace (or once
// across all of them). A list that fails names its namespace and kind in a
// limitation and the rest is kept; when every list fails the source is
// unavailable. Past [fleet.MaxInferredServices] it stops reading and says so.
func (s *WorkloadSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	col := &fleet.Collection{Cluster: s.cluster}
	namespaces := readNamespaces(s.namespaces)
	now := time.Now()
	var read []string
	var listErr error
	listed := map[string]int{}                   // complete lists per kind
	promoted := map[int][]fleet.ProviderObject{} // workloads per promoter fingerprint
	for _, ns := range namespaces {
		complete := true
		for _, kind := range workloadshape.WorkloadKinds {
			switch err := s.listKind(ctx, col, ns, kind, now, promoted); {
			case errors.Is(err, errCapped):
				col.Limitations = append(col.Limitations, fleet.Limitation{
					Code: fleet.LimitationInferredServicesCapped, Source: s.id,
					Message: "workloads past the bound of " + strconv.Itoa(fleet.MaxInferredServices) + " were not read; raise MaxInferredServices if a real cluster hits it",
				})
				col.Providers = s.providerRows(ctx, col, listed, len(namespaces), promoted)
				return scoped(s.id, s.namespaces, col, read), nil
			case err != nil:
				listErr, complete = cmp.Or(listErr, err), false
				col.Limitations = append(col.Limitations, kindUnreadable(s.id, ns, kind, err))
			default:
				listed[kind]++
			}
		}
		if complete {
			read = append(read, ns)
		}
	}
	if len(col.Limitations) == len(namespaces)*len(workloadshape.WorkloadKinds) {
		return nil, listErr
	}
	col.Providers = s.providerRows(ctx, col, listed, len(namespaces), promoted)
	return scoped(s.id, s.namespaces, col, read), nil
}

// readNamespaces are the namespaces to list: the configured ones, or "" for
// all of them at once.
func readNamespaces(namespaces []string) []string {
	if len(namespaces) == 0 {
		return []string{""}
	}
	return namespaces
}

// providerRows are the rows of col: the Kubernetes workloads row, the Helm
// row when a workload is grouped by a Helm release, then the Argo CD and
// Flux rows. On the way it names the repository behind each Argo CD or Flux
// group, or why it could not. A failed discovery or GitOps list stays on the
// rows and records and never on col, so the workloads row and the source's
// health do not change.
func (s *WorkloadSource) providerRows(ctx context.Context, col *fleet.Collection, listed map[string]int, lists int, promoted map[int][]fleet.ProviderObject) []fleet.Provider {
	rows := []fleet.Provider{workloadsRow(col, listed, lists)}
	if row, ok := helmRow(col); ok {
		rows = append(rows, row)
	}
	idx, gitops := discoveryFailedIndex(s.namespaces), []fleet.Provider(nil)
	versions, err := s.client.PreferredVersions(ctx, append(gitopsGroups(), s.promoterGroups()...)...)
	if err == nil {
		idx, gitops = s.readGitOps(ctx, versions)
	} else {
		versions = nil
	}
	for i := range col.InferredServices {
		in := &col.InferredServices[i]
		in.Group, in.Repositories, in.RepositoryUnknown = idx.resolveGroup(*in)
		in.RepositoryUnknownFrom = idx.unknownFrom(*in, in.RepositoryUnknown)
	}
	return append(append(rows, gitops...), s.promoterRows(versions, err == nil, promoted)...)
}

// crdGroup is the API group of a <plural>.<group> CRD name.
func crdGroup(crd string) string {
	_, group, _ := strings.Cut(crd, ".")
	return group
}

// promoterGroups are the API groups of the promoter CRD fingerprints.
func (s *WorkloadSource) promoterGroups() []string {
	var out []string
	for _, f := range s.promoters {
		if f.CRD != "" {
			out = append(out, crdGroup(f.CRD))
		}
	}
	return out
}

// notePromoted adds the workload in to promoted under every promoter
// fingerprint whose label or annotation meta carries. Only the keys are
// looked up; no other label or annotation is copied.
func (s *WorkloadSource) notePromoted(promoted map[int][]fleet.ProviderObject, in fleet.RawInferredService, meta map[string]any) {
	for i, f := range s.promoters {
		o := workloadmanager.Object{Labels: allowlisted(meta["labels"], []string{f.Label}), Annotations: allowlisted(meta["annotations"], []string{f.Annotation})}
		if _, ok := f.Match(o); ok {
			promoted[i] = append(promoted[i], fleet.ProviderObject{Kind: in.Kind, Namespace: in.Namespace, Name: in.Name})
		}
	}
}

// promoterRows are the promotion rows: one per product whose CRD's API group
// the cluster serves (versions, from discovery) or whose label or annotation
// is on a workload read. The evidence of a product found starts with
// fleet.PromotionOwnedBy. When discovery failed (discovered false), a
// product known only by its CRD gets a partial row that says Pacto could not
// check, so a failed check never reads as no promoter.
// ponytail: a CRD counts as served when discovery serves its API group, and
// the evidence says only that, so a group shared with another controller's
// kinds may name a promoter that is not installed; that only ever backs
// delivery off. Ask discovery for the group's resources if a team reports one.
func (s *WorkloadSource) promoterRows(versions map[string]string, discovered bool, promoted map[int][]fleet.ProviderObject) []fleet.Provider {
	var rows []fleet.Provider
	at := map[string]int{}
	row := func(product string) *fleet.Provider {
		j, ok := at[product]
		if !ok {
			j, at[product] = len(rows), len(rows)
			rows = append(rows, fleet.Provider{ID: "promotion/" + product, Name: product, UsedFor: []string{fleet.UsePromotion}})
		}
		return &rows[j]
	}
	for i, f := range s.promoters {
		switch group := crdGroup(f.CRD); {
		case f.CRD != "" && versions[group] != "":
			promotionFound(row(f.Product), "API group "+group+" served", nil)
		case len(promoted[i]) > 0:
			found := "annotation " + f.Annotation + " on workloads"
			if f.Label != "" {
				found = "label " + f.Label + " on workloads"
			}
			promotionFound(row(f.Product), found, promoted[i])
		case f.CRD != "" && !discovered:
			p := row(f.Product)
			p.Status = fleet.SourcePartial
			p.Limitations = append(p.Limitations, fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: s.id,
				Message: "Pacto could not discover whether CRD " + f.CRD + " is served"})
		}
	}
	for i := range rows {
		if rows[i].Evidence == "" {
			rows[i].Evidence = "Pacto could not check whether " + rows[i].Name + " promotes here"
		}
	}
	return rows
}

// promotionFound adds found, and the objects it names, to the evidence of a
// promotion row, once.
func promotionFound(p *fleet.Provider, found string, objects []fleet.ProviderObject) {
	switch {
	case p.Evidence == "":
		p.Evidence = fleet.PromotionOwnedBy + p.Name + " (" + found + ")"
	case !strings.Contains(p.Evidence, found):
		p.Evidence = strings.TrimSuffix(p.Evidence, ")") + ", " + found + ")"
	}
	p.Objects = append(p.Objects, objects...)
}

// workloadsRow is the Kubernetes workloads row of col: how many workloads of
// each kind it read per namespace, and the kinds not listed in full in all
// lists namespaces: a list failed, or the cap stopped before it ended.
// pkg/fleet words the evidence, so an unread kind is never read as none. A failed list or the cap
// makes the row partial with the limitations col holds so far, which name no
// scope (scoped adds that after).
func workloadsRow(col *fleet.Collection, listed map[string]int, lists int) fleet.Provider {
	row := fleet.Provider{ID: fleet.ProviderKubernetesWorkloads, Name: "Kubernetes workloads", UsedFor: []string{fleet.UseInferredServices}, Counts: []fleet.ProviderCount{}}
	at := map[[2]string]int{}
	for _, in := range col.InferredServices {
		key := [2]string{in.Kind, in.Namespace}
		if i, ok := at[key]; ok {
			row.Counts[i].N++
			continue
		}
		at[key] = len(row.Counts)
		row.Counts = append(row.Counts, fleet.ProviderCount{Kind: in.Kind, Namespace: in.Namespace, N: 1})
	}
	for _, kind := range workloadshape.WorkloadKinds {
		if listed[kind] < lists {
			row.UnreadableKinds = append(row.UnreadableKinds, kind)
		}
	}
	if len(col.Limitations) > 0 {
		row.Status, row.Limitations = fleet.SourcePartial, slices.Clone(col.Limitations)
	}
	return row
}

// kindUnreadable is the limitation of a failed list of kind in ns. Reading
// every namespace (ns ""), it names the kind, never an empty namespace.
func kindUnreadable(id, ns, kind string, err error) fleet.Limitation {
	if ns != "" {
		return fleet.NamespaceUnreadableLimitation(id, ns, reasonClass(err)+" listing "+kind)
	}
	return fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: id, Message: "source " + id + " could not list " + fleet.PluralKind(kind) + " in any namespace: " + reasonClass(err)}
}

// listKind pages through the workloads of kind in ns into col.
func (s *WorkloadSource) listKind(ctx context.Context, col *fleet.Collection, ns, kind string, now time.Time, promoted map[int][]fleet.ProviderObject) error {
	gvr := k8sclient.WorkloadGVRs[kind]
	token := ""
	for {
		data, next, err := s.client.ListPage(ctx, gvr, ns, token)
		if err != nil {
			return fmt.Errorf("listing %s: %w", gvr.Resource, err)
		}
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return fmt.Errorf("decoding %s list: %w", gvr.Resource, err)
		}
		for _, item := range page.Items {
			if len(col.InferredServices) == fleet.MaxInferredServices {
				return errCapped
			}
			in := inferredService(kind, item, now)
			meta, _ := item["metadata"].(map[string]any)
			s.notePromoted(promoted, in, meta)
			col.InferredServices = append(col.InferredServices, in)
		}
		if next == "" {
			return nil
		}
		token = next
	}
}

// inferredService is [fleet.InferredServiceOf] with the group the workload's
// own metadata names.
func inferredService(kind string, item map[string]any, now time.Time) fleet.RawInferredService {
	in := fleet.InferredServiceOf(kind, item, now)
	meta, _ := item["metadata"].(map[string]any)
	in.Group = toFleetGroup(workloadmanager.Group(groupingObject(k8sclient.WorkloadGVRs[kind], kind, in.Namespace, in.Name, meta)))
	return in
}

// normalizeNamespaces sorts and deduplicates a namespace list, dropping "".
func normalizeNamespaces(namespaces []string) []string {
	return slices.Compact(slices.Sorted(slices.Values(slices.DeleteFunc(slices.Clone(namespaces), func(n string) bool { return n == "" }))))
}
