package fleetsrc

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/internal/workloadshape"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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
}

// NewWorkloadSource returns a live workload source over namespaces (empty
// for all namespaces). id is its provenance id; cluster is the cluster every
// workload is in, nil when unknown.
func NewWorkloadSource(id string, cluster *fleet.Cluster, client k8sclient.K8sClient, namespaces []string) *WorkloadSource {
	return &WorkloadSource{id: cmp.Or(id, "k8s-workloads"), cluster: cluster, client: client, namespaces: normalizeNamespaces(namespaces)}
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
	namespaces := s.namespaces
	if len(namespaces) == 0 {
		namespaces = []string{""}
	}
	now := time.Now()
	var read []string
	var listErr error
	listed := map[string]int{} // complete lists per kind
	for _, ns := range namespaces {
		complete := true
		for _, kind := range workloadshape.WorkloadKinds {
			switch err := s.listKind(ctx, col, ns, kind, now); {
			case errors.Is(err, errCapped):
				col.Limitations = append(col.Limitations, fleet.Limitation{
					Code: fleet.LimitationInferredServicesCapped, Source: s.id,
					Message: "workloads past the bound of " + strconv.Itoa(fleet.MaxInferredServices) + " were not read; raise MaxInferredServices if a real cluster hits it",
				})
				col.Providers = []fleet.Provider{workloadsRow(col, listed, len(namespaces))}
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
	col.Providers = []fleet.Provider{workloadsRow(col, listed, len(namespaces))}
	return scoped(s.id, s.namespaces, col, read), nil
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
	return fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: id, Message: "source " + id + " could not list " + kind + "s in any namespace: " + reasonClass(err)}
}

// listKind pages through the workloads of kind in ns into col.
func (s *WorkloadSource) listKind(ctx context.Context, col *fleet.Collection, ns, kind string, now time.Time) error {
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
			col.InferredServices = append(col.InferredServices, inferredService(kind, item, now))
		}
		if next == "" {
			return nil
		}
		token = next
	}
}

// inferredService is the inferred service of one workload object: its
// identity and shape, each value's path prefixed with the kind.
func inferredService(kind string, item map[string]any, now time.Time) fleet.RawInferredService {
	meta, _ := item["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	ns, _ := meta["namespace"].(string)
	shape := workloadshape.ReadWorkloadShape(kind, name, item)
	created, _ := meta["creationTimestamp"].(string)
	in := fleet.RawInferredService{Namespace: ns, Kind: kind, Name: name, ObservedAt: &now, CreatedAt: parseK8sTime(created)}
	if shape.Image != "" {
		in.Image = &fleet.InferredValue{Value: shape.Image, From: kind + " " + shape.ImageFrom}
	}
	for _, p := range shape.Ports {
		in.Ports = append(in.Ports, fleet.InferredPort{Port: p.Number, Name: p.Name, Protocol: p.Protocol, From: kind + " " + p.From})
	}
	for _, p := range shape.Probes {
		in.Probes = append(in.Probes, fleet.InferredProbe{Kind: p.Kind, Path: p.Path, Port: p.Port, From: kind + " " + p.From})
	}
	return in
}

// normalizeNamespaces sorts and deduplicates a namespace list, dropping "".
func normalizeNamespaces(namespaces []string) []string {
	return slices.Compact(slices.Sorted(slices.Values(slices.DeleteFunc(slices.Clone(namespaces), func(n string) bool { return n == "" }))))
}
