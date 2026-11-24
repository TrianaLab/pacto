package dashboard

import (
	"cmp"
	"context"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// RemoteDelivery is the delivery of a cluster other than the dashboard's own,
// as the host reads it for one request. A remote only reads: every write on
// it is refused with RemoteClusterReadOnly and the command that acts there.
type RemoteDelivery struct {
	Cluster fleet.Cluster
	// API reads the cluster; nil when Err or Hidden is set.
	API DeliveryAPI
	// Err says why the cluster could not be read: its listing is partial
	// and shows none of its objects.
	Err error
	// AsOf is when the cluster's objects were recorded, when they are
	// records; nil means read now.
	AsOf *time.Time
	// Hidden is a cluster the reader may not see, or one with no live
	// channel: counted, never named.
	Hidden bool
	// Next is the command that acts on operation namespace/name there; nil
	// names pacto delivery approve with the cluster's kubeconfig context.
	Next func(namespace, name string) string
}

// namespacesHidden is a remote API that left out the namespaces the reader
// may not list there: it says how many, never which.
type namespacesHidden interface{ HiddenNamespaces() int }

// next is the command that acts on operation namespace/name in r's cluster.
func (r RemoteDelivery) next(namespace, name string) string {
	if r.Next != nil {
		return r.Next(namespace, name)
	}
	return "pacto delivery approve " + name + " -n " + namespace + " --context " + cmp.Or(r.Cluster.Name, r.Cluster.ID)
}

// remotes are the other clusters the request reads delivery from, and the
// dashboard's own cluster; none when the host configures no remote.
func (s *Server) remotes(ctx context.Context) (fleet.Cluster, []RemoteDelivery) {
	if s.opts.DeliveryRemotes == nil {
		return fleet.Cluster{}, nil
	}
	return s.opts.DeliveryRemotes(requestFrom(ctx))
}

// deliveryAt is the caller's delivery API in cluster: the dashboard's own for
// "", else a remote the caller may see, which only reads. A cluster the
// caller may not see answers as one that does not exist.
func (s *Server) deliveryAt(ctx context.Context, cluster string) (DeliveryAPI, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil || cluster == "" {
		return api, err
	}
	_, remotes := s.remotes(ctx)
	for _, r := range remotes {
		switch {
		case r.Cluster.ID != cluster || r.Hidden:
		case r.Err != nil:
			return nil, Refuse(http.StatusServiceUnavailable, "ClusterUnreachable", "cluster "+cmp.Or(r.Cluster.Name, r.Cluster.ID)+" could not be read: "+r.Err.Error())
		default:
			return readOnly{r.API}, nil
		}
	}
	return nil, Refuse(http.StatusNotFound, "ClusterUnknown", "no cluster "+cluster+" is read here")
}

// refuseRemote refuses a write in a cluster other than the dashboard's own,
// with the command that acts there; nil for the dashboard's own ("").
func (s *Server) refuseRemote(ctx context.Context, cluster, namespace, name string) error {
	if cluster == "" {
		return nil
	}
	_, remotes := s.remotes(ctx)
	r := RemoteDelivery{Cluster: fleet.Cluster{ID: cluster}}
	for _, rd := range remotes {
		if rd.Cluster.ID == cluster && !rd.Hidden {
			r = rd
		}
	}
	return Refuse(http.StatusConflict, "RemoteClusterReadOnly", "this dashboard only reads cluster "+cmp.Or(r.Cluster.Name, r.Cluster.ID)+
		": act there with "+r.next(namespace, name))
}

// readOnly is a remote cluster's API: it offers no action, since this
// dashboard acts only in its own cluster.
type readOnly struct{ DeliveryAPI }

func (readOnly) Can(context.Context, string, string, string) (bool, error) { return false, nil }

// fanned is the dashboard's own API plus the remotes a listing read: what
// needs a Stage reads it in the Stage's own cluster.
type fanned struct {
	DeliveryAPI
	by   map[string]DeliveryAPI
	next map[string]func(namespace, name string) string
}

// at is the API of cluster; the dashboard's own for "".
func (f fanned) at(cluster string) DeliveryAPI {
	if a := f.by[cluster]; a != nil {
		return a
	}
	return f.DeliveryAPI
}

func (f fanned) Describe(ctx context.Context, st *delivery.StageObject, digest string) delivery.Revision {
	return f.at(st.Metadata.Cluster).Describe(ctx, st, digest)
}

func (f fanned) History(ctx context.Context, namespace, stage string, listed *delivery.StageObject) (*delivery.History, error) {
	api := f.DeliveryAPI
	if listed != nil {
		api = f.at(listed.Metadata.Cluster)
	}
	return api.History(ctx, namespace, stage, listed)
}

// access answers, per cluster, the caller's access to the workload of each
// of stages there, one review per workload.
func (f fanned) access(stages []delivery.StageObject) func(cluster string) delivery.StageAccess {
	byCluster := map[string][]delivery.StageObject{}
	for _, st := range stages {
		byCluster[st.Metadata.Cluster] = append(byCluster[st.Metadata.Cluster], st)
	}
	memo := map[string]delivery.StageAccess{}
	for c, sts := range byCluster {
		memo[c] = delivery.AccessMemo(sts, f.at(c).WorkloadAccess)
	}
	return func(cluster string) delivery.StageAccess {
		if a := memo[cluster]; a != nil {
			return a
		}
		return delivery.AccessMemo(nil, f.at(cluster).WorkloadAccess)
	}
}

// readOnlyItems marks each item of a remote cluster read-only, with the
// command that acts there.
func (f fanned) readOnlyItems(items []InboxItem) {
	for i, it := range items {
		if it.Cluster != "" {
			items[i].ReadOnly, items[i].Next = true, f.next[it.Cluster](it.Namespace, it.Name)
		}
	}
}

// listsAll is lists plus the Stages and, with ops, Operations of every remote
// the caller may see, each stamped with its cluster. With remotes the
// listing says how completely each cluster was read: a remote that could not
// be read is partial and shows nothing, a hidden one is only counted, and
// the whole listing is partial when any cluster is.
func (s *Server) listsAll(ctx context.Context, api DeliveryAPI, withOps bool) ([]delivery.StageObject, []delivery.OperationObject, Listing, fanned, error) {
	f := fanned{DeliveryAPI: api, by: map[string]DeliveryAPI{}, next: map[string]func(string, string) string{}}
	stages, ops, l, err := s.lists(ctx, api, withOps)
	home, remotes := s.remotes(ctx)
	if err != nil || len(remotes) == 0 {
		return stages, ops, l, f, err
	}
	l.Clusters = []delivery.ClusterListing{{ID: home.ID, Name: home.Name, Completeness: l.Completeness, AsOf: l.AsOf, Unreadable: l.Unreadable}}
	l.whole = map[string]bool{}
	read := make([]remoteRead, len(remotes))
	var g errgroup.Group
	g.SetLimit(delivery.DescribeParallel)
	for i, r := range remotes {
		if !r.Hidden && r.Err == nil {
			g.Go(func() error { read[i] = readRemote(ctx, r, s.opts.DeliveryNamespace, withOps); return nil })
		}
	}
	_ = g.Wait()
	for i, r := range remotes {
		if r.Hidden {
			l.HiddenClusters++
			continue
		}
		cl := delivery.ClusterListing{ID: r.Cluster.ID, Name: r.Cluster.Name, Completeness: "complete", AsOf: l.AsOf}
		if r.AsOf != nil {
			cl.AsOf = r.AsOf.UTC().Format(time.RFC3339)
		}
		switch got := read[i]; {
		case r.Err != nil:
			cl.Completeness, cl.Why = "partial", r.Err.Error()
		case got.err != nil:
			cl.Completeness, cl.Why = "partial", got.err.Error()
		default:
			stages, ops = append(stages, got.stages...), append(ops, got.ops...)
			f.by[r.Cluster.ID], f.next[r.Cluster.ID] = readOnly{r.API}, r.next
			if cl.Hidden = got.hidden; cl.Hidden > 0 {
				cl.Completeness = "partial"
			}
			l.whole[r.Cluster.ID] = cl.Hidden == 0
		}
		l.Clusters = append(l.Clusters, cl)
	}
	for _, cl := range l.Clusters {
		if cl.Completeness != "complete" || l.HiddenClusters > 0 {
			l.Completeness = "partial"
		}
	}
	return stages, ops, l, f, nil
}

// remoteRead is what one remote listed, stamped with its cluster.
type remoteRead struct {
	stages []delivery.StageObject
	ops    []delivery.OperationObject
	hidden int
	err    error
}

// readRemote lists r's Stages and, with ops, Operations in namespace ns (""
// every namespace the caller may list there). What the host returns may be
// shared, so the stamped objects are copies.
func readRemote(ctx context.Context, r RemoteDelivery, ns string, withOps bool) remoteRead {
	sts, err := r.API.Stages(ctx, ns)
	if err != nil {
		return remoteRead{err: err}
	}
	oss, err := operations(ctx, r.API, ns, withOps)
	if err != nil {
		return remoteRead{err: err}
	}
	out := remoteRead{stages: make([]delivery.StageObject, len(sts)), ops: make([]delivery.OperationObject, len(oss))}
	for i, st := range sts {
		st.Metadata.Cluster = r.Cluster.ID
		out.stages[i] = st
	}
	for i, o := range oss {
		o.Metadata.Cluster = r.Cluster.ID
		out.ops[i] = o
	}
	if h, ok := r.API.(namespacesHidden); ok {
		out.hidden = h.HiddenNamespaces()
	}
	return out
}
