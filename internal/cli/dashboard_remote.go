package cli

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// errRemoteReview hides a cluster whose review of the reader failed, in the
// words the fleet's cluster chips use (514-S2).
var errRemoteReview = errors.New("the review failed or took longer than 5s, so its delivery objects are hidden")

// errClusterUnidentified is a context whose cluster identity could not be
// read: its delivery is not read either.
var errClusterUnidentified = errors.New("its cluster identity could not be read, so its delivery objects are not read")

// remotes is the dashboard's own cluster and the delivery of every other
// cluster the fleet reads, for a request that may read delivery at all: none
// in open shared mode or for a sign-in link session.
func (id *dashboardIdentity) remotes(r *http.Request) (fleet.Cluster, []dashboard.RemoteDelivery) {
	if _, err := id.delivery(r); err != nil {
		return fleet.Cluster{}, nil
	}
	return id.remotesFor(r.Context(), id.state(r.Context()).Person)
}

// remotesFor is the delivery of every resolved context other than the
// dashboard's own (aliases and conflicts are no cluster of their own), each
// read with that context's credentials, and of every approved member no
// context reaches, read from its records (memberRemotes). With a viewer, the person p is
// reviewed in each cluster: a cluster nobody reviews them in is hidden and
// counted, one whose review fails is partial, and every read passes
// Viewer.MayList. Without one (local mode) each context is read as its own
// kubeconfig user, unfiltered.
func (id *dashboardIdentity) remotesFor(ctx context.Context, p authn.Person) (fleet.Cluster, []dashboard.RemoteDelivery) {
	homeUID := ""
	if id.homeUID != nil {
		homeUID, _ = id.homeUID(ctx)
	}
	var home fleet.Cluster
	var out []dashboard.RemoteDelivery
	var entries []app.K8sEntry
	if id.entries != nil {
		entries = id.entries(ctx)
	}
	live := map[string]bool{homeUID: true}
	for _, e := range entries {
		live[e.UID] = true
		c := fleet.Cluster{ID: e.UID, Name: e.ClusterName()}
		switch {
		case e.Alias != "":
		case homeUID != "" && e.UID == homeUID || sameServer(e.Config, id.cluster):
			home = c
		case e.Conflict != "":
			// Its uid is another context's: it is named by its context.
			c.ID = e.Name
			out = append(out, dashboard.RemoteDelivery{Cluster: c, Context: e.Name,
				Err: errors.New("it reports the cluster id of context " + e.Conflict + " from another server, so it is read as no cluster")})
		case e.Err != nil:
			c.ID = cmp.Or(e.UID, e.Name)
			out = append(out, dashboard.RemoteDelivery{Cluster: c, Context: e.Name, Err: e.Err})
		case e.UID == "":
			c.ID = e.Name
			out = append(out, dashboard.RemoteDelivery{Cluster: c, Context: e.Name, Err: errClusterUnidentified})
		default:
			r := id.remote(ctx, e.Config, c, p, e.Name)
			r.Context = e.Name
			out = append(out, r)
		}
	}
	// The hub's members that no context reaches, read from their records.
	if members := id.memberRemotes(ctx, p, live); len(members) > 0 {
		out = append(out, members...)
		home.ID = cmp.Or(home.ID, homeUID)
	}
	if len(out) == 0 {
		return fleet.Cluster{}, nil
	}
	return home, out
}

// remote is cluster c's delivery read through cfg, reviewed for p when the
// dashboard has a viewer.
func (id *dashboardIdentity) remote(ctx context.Context, cfg *rest.Config, c fleet.Cluster, p authn.Person, kubeContext string) dashboard.RemoteDelivery {
	var all bool
	if id.viewer != nil {
		var err error
		all, err = id.viewer.MayList(ctx, p, c.ID, "", authn.StageResource, "list")
		switch {
		case errors.Is(err, authn.ErrClusterNotReviewable):
			return dashboard.RemoteDelivery{Cluster: c, Hidden: true}
		case err != nil:
			return dashboard.RemoteDelivery{Cluster: c, Err: errRemoteReview}
		}
	}
	dyn, err := id.newClient(cfg)
	if err != nil {
		return dashboard.RemoteDelivery{Cluster: c, Err: err}
	}
	act := &app.Delivery{Dynamic: dyn, Registry: id.registry, Records: id.records, Trust: id.trust,
		Policy: delivery.DefaultClassPolicy(), PolicyNamespace: id.policyNS, Now: id.now, Streams: &id.streams}
	api := dashboard.DeliveryAPI(newDashDelivery(id, act, ""))
	if id.viewer != nil {
		api = &reviewedDelivery{inner: api, viewer: id.viewer, person: p, cluster: c, context: kubeContext, stagesAll: all, denied: map[string]bool{}}
	}
	return dashboard.RemoteDelivery{Cluster: c, API: api}
}

// memoEntries keeps what entries resolved for a minute, so a request does
// not read every context's identity again; nil for no entries.
func memoEntries(entries func(context.Context) []app.K8sEntry) func(context.Context) []app.K8sEntry {
	if entries == nil {
		return nil
	}
	var mu sync.Mutex
	var kept []app.K8sEntry
	var at time.Time
	return func(ctx context.Context) []app.K8sEntry {
		mu.Lock()
		defer mu.Unlock()
		if kept == nil || time.Since(at) > namespacesTTL {
			kept, at = entries(ctx), time.Now()
		}
		return kept
	}
}

// reviewedDelivery is another cluster's delivery read with the dashboard's
// own credentials there, every read reviewed for the person in that
// cluster: lists keep only the namespaces they may list (the others are
// counted, never named) and a single object they may not get answers as one
// that does not exist. It only reads: nothing else the dashboard's
// credentials could do there is offered.
type reviewedDelivery struct {
	inner   dashboard.DeliveryAPI
	viewer  *authn.Viewer
	person  authn.Person
	cluster fleet.Cluster
	// context is the kubeconfig context that reaches the cluster.
	context string
	// stagesAll: the person may list Stages in every namespace there.
	stagesAll bool

	mu     sync.Mutex
	denied map[string]bool
}

// HiddenNamespaces counts the namespaces listed objects sat in that the
// person may not list.
func (r *reviewedDelivery) HiddenNamespaces() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.denied)
}

// may asks whether the person may verb res there; a failed review hides
// the cluster.
func (r *reviewedDelivery) may(ctx context.Context, ns string, res authn.ResourceAttributes, verb string) (bool, error) {
	ok, err := r.viewer.MayList(ctx, r.person, r.cluster.ID, ns, res, verb)
	if err != nil {
		return false, errRemoteReview
	}
	return ok, nil
}

// reviewedList keeps the items of the namespaces the person may list res in.
func reviewedList[T any](ctx context.Context, r *reviewedDelivery, items []T, res authn.ResourceAttributes, all bool, nsOf func(T) string) ([]T, error) {
	if all {
		return items, nil
	}
	out, allowed := []T{}, map[string]bool{}
	for _, it := range items {
		ns := nsOf(it)
		ok, seen := allowed[ns]
		if !seen {
			var err error
			if ok, err = r.may(ctx, ns, res, "list"); err != nil {
				return nil, err
			}
			allowed[ns] = ok
			if !ok {
				r.mu.Lock()
				r.denied[ns] = true
				r.mu.Unlock()
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	return out, nil
}

// get refuses, as absent, an object the person may not get.
func (r *reviewedDelivery) get(ctx context.Context, ns, name string, res authn.ResourceAttributes) error {
	res.Name = name
	ok, err := r.may(ctx, ns, res, "get")
	switch {
	case err != nil:
		return dashboard.Refuse(http.StatusServiceUnavailable, "ClusterReviewFailed", "cluster "+r.cluster.Name+": "+err.Error())
	case !ok:
		return refusal(apierrors.NewNotFound(schema.GroupResource{Group: res.Group, Resource: res.Resource}, name))
	}
	return nil
}

// errRemoteOnlyReads refuses what a reviewed remote does not offer.
func (r *reviewedDelivery) errRemoteOnlyReads() error {
	return dashboard.Refuse(http.StatusConflict, "RemoteClusterReadOnly", "this dashboard only reads cluster "+r.cluster.Name)
}

func (r *reviewedDelivery) Stages(ctx context.Context, ns string) ([]delivery.StageObject, error) {
	s, err := r.inner.Stages(ctx, ns)
	if err != nil {
		return nil, err
	}
	return reviewedList(ctx, r, s, authn.StageResource, r.stagesAll, func(s delivery.StageObject) string { return s.Metadata.Namespace })
}

func (r *reviewedDelivery) Operations(ctx context.Context, ns string) ([]delivery.OperationObject, error) {
	o, err := r.inner.Operations(ctx, ns)
	if err != nil {
		return nil, err
	}
	all, err := r.may(ctx, "", authn.OperationResource, "list")
	if err != nil {
		return nil, err
	}
	return reviewedList(ctx, r, o, authn.OperationResource, all, func(o delivery.OperationObject) string { return o.Metadata.Namespace })
}

func (r *reviewedDelivery) Get(ctx context.Context, ns, name string) (*delivery.OperationObject, error) {
	if err := r.get(ctx, ns, name, authn.OperationResource); err != nil {
		return nil, err
	}
	return r.inner.Get(ctx, ns, name)
}

func (r *reviewedDelivery) Stage(ctx context.Context, ns, name string) (*delivery.StageObject, error) {
	if err := r.get(ctx, ns, name, authn.StageResource); err != nil {
		return nil, err
	}
	return r.inner.Stage(ctx, ns, name)
}

func (r *reviewedDelivery) Explain(ctx context.Context, ns, name string) (*delivery.Explanation, error) {
	if err := r.get(ctx, ns, name, authn.OperationResource); err != nil {
		return nil, err
	}
	return r.inner.Explain(ctx, ns, name)
}

func (r *reviewedDelivery) History(ctx context.Context, ns, stage string, listed *delivery.StageObject) (*delivery.History, error) {
	if err := r.get(ctx, ns, stage, authn.StageResource); err != nil {
		return nil, err
	}
	return r.inner.History(ctx, ns, stage, listed)
}

func (r *reviewedDelivery) Blame(ctx context.Context, ns, stage, digest string) (*delivery.Blame, error) {
	if err := r.get(ctx, ns, stage, authn.StageResource); err != nil {
		return nil, err
	}
	return r.inner.Blame(ctx, ns, stage, digest)
}

func (r *reviewedDelivery) Record(ctx context.Context, ns, stage, id string) (*delivery.RecordView, error) {
	if err := r.get(ctx, ns, stage, authn.StageResource); err != nil {
		return nil, err
	}
	return r.inner.Record(ctx, ns, stage, id)
}

// Undo is never planned with the dashboard's credentials there: planning
// reads every Pacto and Stage they may read. It is planned in that cluster.
func (r *reviewedDelivery) Undo(_ context.Context, ns, name, _ string) (*delivery.Undo, error) {
	return nil, dashboard.Refuse(http.StatusConflict, "RemoteClusterReadOnly", "this dashboard only reads cluster "+r.cluster.Name+
		": plan the undo there with kubectl config use-context "+r.context+" && pacto delivery undo "+name+" -n "+ns)
}

func (r *reviewedDelivery) Describe(ctx context.Context, st *delivery.StageObject, digest string) delivery.Revision {
	return r.inner.Describe(ctx, st, digest)
}

// WorkloadAccess is never asked with the dashboard's credentials there: who
// may decide is answered in that cluster.
func (r *reviewedDelivery) WorkloadAccess(_ context.Context, ns string, w delivery.WorkloadRef) delivery.WorkloadAccess {
	return delivery.WorkloadAccess{Name: w.Name, Namespace: ns, Unverified: "this dashboard only reads cluster " + r.cluster.Name}
}

// What follows names the dashboard's own credentials or acts: a reviewed
// remote offers none of it.

func (r *reviewedDelivery) WhoAmI(context.Context) (delivery.Caller, error) {
	return delivery.Caller{}, r.errRemoteOnlyReads()
}

func (r *reviewedDelivery) Plan(context.Context, delivery.Request) (delivery.PlanAnswer, error) {
	return delivery.PlanAnswer{}, r.errRemoteOnlyReads()
}

func (r *reviewedDelivery) Propose(context.Context, delivery.Request) (*delivery.OperationObject, delivery.PlanAnswer, error) {
	return nil, delivery.PlanAnswer{}, r.errRemoteOnlyReads()
}

func (r *reviewedDelivery) Approve(context.Context, string, string, delivery.Decision, string) (string, error) {
	return "", r.errRemoteOnlyReads()
}

func (r *reviewedDelivery) Cancel(context.Context, string, string) error {
	return r.errRemoteOnlyReads()
}

func (r *reviewedDelivery) PublishRefusals(context.Context, string) (*dashboard.PublishRefusals, error) {
	return nil, nil
}

func (r *reviewedDelivery) Trust(context.Context, []delivery.StageObject, []delivery.OperationObject) (*delivery.TrustView, error) {
	return nil, r.errRemoteOnlyReads()
}

func (r *reviewedDelivery) Can(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (r *reviewedDelivery) StageNamespaces(context.Context) ([]string, error) { return nil, nil }

func (r *reviewedDelivery) Environments(context.Context) (*delivery.EnvironmentsView, []delivery.PlacementRow, bool, error) {
	return nil, nil, false, nil
}

// Members are planned on the hub that reads them, never through a remote.
func (r *reviewedDelivery) Members(context.Context, string, []string) []delivery.MemberPlan {
	return nil
}
