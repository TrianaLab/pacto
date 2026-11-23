package app

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// K8sEntry is one kubeconfig context resolved for reading: its config, its
// client and the cluster it reaches. The fleet sources and the per-cluster
// reviewers are both built from one list of entries.
type K8sEntry struct {
	// Name is the context name; "" is a pod's in-cluster config.
	Name   string
	Config *rest.Config
	// UID is the cluster id (the kube-system namespace UID), "" when it was
	// never read.
	UID string
	// Host and CA are the server and its CA, which tell apart two clusters
	// that report one UID (a cluster restored from another's etcd snapshot).
	Host, CA string
	// Alias is the earlier context on the same cluster: this entry is read
	// through that one and is no source of its own.
	Alias string
	// Conflict is the earlier context whose UID this entry reports from
	// another server; such an entry is read as no cluster.
	Conflict string
	// Err is why the entry's client could not be built.
	Err error

	client  k8sclient.K8sClient
	cluster *fleet.Cluster
}

// label names e where a reader sees it: its context, "k8s" without one.
func (e K8sEntry) label() string { return cmp.Or(e.Name, "k8s") }

// k8sMemo keys what Service.k8sLast remembers of one context.
type k8sMemo struct{ what, key string }

// rememberedCollection is the last collection a live source read and when.
type rememberedCollection struct {
	col *fleet.Collection
	at  time.Time
}

// ResolveK8sContexts reads, once and in parallel, the current context (when
// includeCurrent) and then each named context: its config, its client and
// its cluster identity. A context named twice is read once. An identity read
// that fails falls back to the one this Service last read for that context.
// Entries on a cluster an earlier entry already reaches are marked Alias
// (same server) or Conflict (same UID on another server).
func (s *Service) ResolveK8sContexts(ctx context.Context, includeCurrent bool, contexts []string) []K8sEntry {
	type ref struct{ name, read string }
	var refs []ref
	if includeCurrent {
		refs = append(refs, ref{name: s.env().kubeContext()})
	}
	for _, c := range contexts {
		if !slices.ContainsFunc(refs, func(r ref) bool { return r.name == c }) {
			refs = append(refs, ref{name: c, read: c})
		}
	}
	entries := make([]K8sEntry, len(refs))
	var wg sync.WaitGroup
	for i, r := range refs {
		wg.Go(func() { entries[i] = s.contextEntry(ctx, r.name, r.read) })
	}
	wg.Wait()
	markSameCluster(entries)
	return entries
}

// contextEntry reads one context: read is what the client is built from
// ("" for the current context), name what the identity read is told.
func (s *Service) contextEntry(ctx context.Context, name, read string) K8sEntry {
	e := K8sEntry{Name: name}
	env := s.env()
	client, err := env.newK8s(read)
	if err != nil {
		e.Err = err
		return e
	}
	e.client = client
	if cfg, err := env.contextConfig(read); err == nil {
		e.Host, e.CA = k8sclient.ServerOf(cfg)
		e.Config = cfg
	}
	memo := k8sMemo{"identity", e.label()}
	ident, err := client.ClusterIdentity(ctx, name)
	if err == nil {
		s.k8sLast.Store(memo, ident)
	} else if v, ok := s.k8sLast.Load(memo); ok {
		ident = v.(kubecluster.Identity)
	}
	if ident.ID != "" {
		e.UID, e.cluster = ident.ID, &fleet.Cluster{ID: ident.ID, Name: ident.Name, NameSource: ident.NameSource}
	}
	return e
}

// markSameCluster marks each identified entry whose UID an earlier entry
// already reports: Alias on the same server and CA, else Conflict.
func markSameCluster(entries []K8sEntry) {
	for i := range entries {
		e := &entries[i]
		if e.UID == "" {
			continue
		}
		j := slices.IndexFunc(entries[:i], func(p K8sEntry) bool { return p.UID == e.UID && p.Alias == "" && p.Conflict == "" })
		switch {
		case j < 0:
		case entries[j].Host == e.Host && entries[j].CA == e.CA:
			e.Alias = entries[j].label()
		default:
			e.Conflict = entries[j].label()
		}
	}
}

// k8sSources are the live sources of every context opts names, each cluster
// read once.
func (s *Service) k8sSources(ctx context.Context, opts FleetOptions, promoters []workloadmanager.Fingerprint) []fleet.Source {
	entries := s.ResolveK8sContexts(ctx, opts.IncludeK8s, opts.K8sContexts)
	several := countReadable(entries) > 1
	var out []fleet.Source
	for _, e := range entries {
		out = append(out, s.entrySources(e, several, opts, promoters)...)
	}
	return out
}

// countReadable counts the entries that are no alias of another.
func countReadable(entries []K8sEntry) int {
	n := 0
	for _, e := range entries {
		if e.Alias == "" {
			n++
		}
	}
	return n
}

// entrySources are the sources of one entry; several says other clusters
// are read beside it.
func (s *Service) entrySources(e K8sEntry, several bool, opts FleetOptions, promoters []workloadmanager.Fingerprint) []fleet.Source {
	switch {
	case e.Alias != "":
		return nil
	case e.Err != nil:
		// A client that cannot be built (no kubeconfig, an unknown context)
		// reads as unavailable, both the Pacto and the workload source.
		return []fleet.Source{fleet.NewFailingSource(e.label(), fleet.SourceKindKubernetes, e.Err),
			fleet.NewFailingSource(e.label()+workloadsSuffix, fleet.SourceKindKubernetesWorkloads, e.Err)}
	case e.Conflict != "":
		return []fleet.Source{refusedContext(e.label(), fleet.LimitationClusterConflict,
			"contexts "+e.Conflict+" and "+e.label()+" reach two servers that report one cluster id, so Pacto reads only "+e.Conflict)}
	case e.UID == "" && several:
		// Targets are keyed by cluster only when every cluster is known, so a
		// clusterless context beside others would merge same-named targets.
		return []fleet.Source{refusedContext(e.label(), fleet.LimitationClusterUnknown,
			"the cluster of context "+e.label()+" cannot be told apart from the others: Pacto could not read its identity")}
	}
	id := cmp.Or(e.UID, e.label())
	// The mesh source is rebuilt for every build; its memo outlives them so
	// the mesh is read at most once per interval per process.
	memo, _ := s.k8sLast.LoadOrStore(k8sMemo{"mesh", id + "|" + strings.Join(opts.K8sNamespaces, ",")}, &fleetsrc.MeshMemo{})
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	srcs := liveK8sSources(id, e.cluster, e.client, opts.K8sNamespaces, promoters, memo.(*fleetsrc.MeshMemo), now)
	if !opts.LongRunning {
		return srcs
	}
	// srcs is the Pacto source, then the workload and mesh sources.
	keys := []string{e.label(), e.label() + workloadsSuffix, e.label() + meshSuffix}
	for i, src := range srcs {
		srcs[i] = &rememberingSource{Source: src, last: &s.k8sLast, now: s.env().now, key: k8sMemo{"collection", keys[i]}}
	}
	return srcs
}

// refusedContext is a source that reads nothing and says why in one fixed
// message: an unavailable state with no raw error.
func refusedContext(id, code, message string) fleet.Source {
	return fleet.NewMemorySource(id, fleet.SourceKindKubernetes, &fleet.Collection{
		State:       &fleet.SourceState{Status: fleet.SourceUnavailable},
		Limitations: []fleet.Limitation{{Code: code, Source: id, Message: message}},
	})
}

// rememberingSource serves the last collection of a live source, stale, when
// the source fails. ponytail: one collection per source in memory; its
// targets keep their own evidence age, so they read stale past the freshness
// window.
type rememberingSource struct {
	fleet.Source
	last *sync.Map
	now  func() time.Time
	key  k8sMemo
}

// Cluster implements [fleet.ClusterSource] for the source it wraps.
func (r *rememberingSource) Cluster() *fleet.Cluster {
	if cs, ok := r.Source.(fleet.ClusterSource); ok {
		return cs.Cluster()
	}
	return nil
}

// Collect implements [fleet.Source].
func (r *rememberingSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	col, err := r.Source.Collect(ctx)
	if err == nil {
		r.last.Store(r.key, rememberedCollection{col: col, at: r.now()})
		return col, nil
	}
	v, ok := r.last.Load(r.key)
	if !ok {
		return nil, err
	}
	m := v.(rememberedCollection)
	stale := *m.col
	stale.State = fleet.StaleState(m.at, err)
	if m.col.State != nil {
		stale.State.Namespaces, stale.State.NamespacesTotal = m.col.State.Namespaces, m.col.State.NamespacesTotal
	}
	return &stale, nil
}
