/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/delivery/v1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// ladderSource is where a declared installation ladder rung comes from.
const ladderSource = "the chart value delivery.environments.ladder"

// EnvironmentReconciler publishes the installation ladder: the declared chain
// (Ladder) and the rungs Pacto proposes from the cluster's namespaces, the
// services its Pactos describe and the environments the cluster advertises.
// It writes only EnvironmentsConfigMap in Namespace and touches no Stage.
// One pass runs whenever a Namespace or a Pacto changes, and every Resync.
type EnvironmentReconciler struct {
	Client    client.Client
	APIReader client.Reader
	// Namespace is the delivery namespace, where the view is written.
	Namespace string
	ClusterID string
	// ClusterName and NameSource are the cluster's display name and how it
	// was found (kubecluster.Identify).
	ClusterName string
	NameSource  string
	// Ladder is the declared installation ladder, in promotion order.
	Ladder []string
	// Off publishes nothing (--environments=false).
	Off    bool
	Now    func() time.Time
	Resync time.Duration
	// Policy names the approver signers a ladder record must verify under.
	Policy delivery.ClassPolicy
	// LadderRepository is the approvals repository (host/path) the ladder
	// records sit in, read with RecordOptions. Empty reads none.
	LadderRepository string
	RecordOptions    ocireferrer.Options
	// Registry is the installation registry (host/path): the organization
	// index is its pacto-index repository, read with RecordOptions. Empty
	// places no service.
	Registry string
	// Cluster reads the workloads placement checks and pulls bundles.
	Cluster Cluster
	// Accepted is the verdict on the newest version of a service in its
	// bundle repository the publisher check accepts (Publishers.NewestAccepted):
	// nil and why when none is, an error when the check could not run; nil
	// accepts none.
	Accepted func(ctx context.Context, svc delivery.ServiceRef, repo string) (*delivery.PublisherVerdict, string, error)
	// Placement is the verified placement policy and why it could not be
	// read (Publishers.VerifiedPlacement); nil reads none.
	Placement func(ctx context.Context) (*delivery.PlacementPolicy, string)
	// Peers finds the member that runs an upstream rung this cluster does
	// not host; nil follows none.
	Peers *Peers
	// CreateAppliers (profile idp) creates, for a service no applier runs
	// yet, the suspended Flux objects of each placed environment and its
	// deployer identity, bound to the ClusterRole DeployerRole.
	CreateAppliers bool
	DeployerRole   string
	// CreateNamespaces creates the namespace a service is placed in when it
	// does not exist: at once for a declared or observed placement, once a
	// person's ladder record allows it for any other.
	CreateNamespaces bool
	// RetireAfter is how long a managed Stage whose environment stopped being
	// desired, or expired, is kept before it is retired.
	RetireAfter time.Duration
	// Events and CDEvents report each managed Stage created and deleted;
	// the StageReconciler and the recorder report only hand-written ones.
	Events   record.EventRecorder
	CDEvents *Events

	// mu serializes a pass and a placement confirmation, which both write
	// the private state.
	// ponytail: one lock for the installation; a confirmation waits for a
	// running pass. Split the state per service if passes get long.
	mu sync.Mutex

	// records remembers what each ladder record manifest names, and the
	// records that never count; last is the ladder the last full read gave.
	records deliveryrecord.Reader
	last    ladderRead
	// maxServices lowers orgindex.MaxServices in tests.
	maxServices int
}

// SetupWithManager registers the pass on Namespace and, when the cluster
// serves it, Pacto changes. Every event requeues the one installation-wide
// pass. Off registers nothing.
func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Off {
		return nil
	}
	b := ctrl.NewControllerManagedBy(mgr).Named("delivery-environments").
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(onePass))
	pacto := schema.GroupVersionKind{Group: gvkPactoList.Group, Version: gvkPactoList.Version, Kind: "Pacto"}
	if _, err := mgr.GetRESTMapper().RESTMapping(pacto.GroupKind(), pacto.Version); err == nil {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(pacto)
		b.Watches(u, handler.EnqueueRequestsFromMapFunc(onePass))
	}
	return b.Complete(r)
}

// onePass maps every event to the single installation-wide request.
func onePass(context.Context, client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: delivery.EnvironmentsConfigMap}}}
}

// Reconcile runs one pass: it reads the namespaces and the Pactos, proposes
// rungs and writes the view. A read that fails leaves the view partial with
// the reason, and the proposal is built from what was read.
func (r *EnvironmentReconciler) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	if r.Off {
		return reconcile.Result{}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	view := delivery.EnvironmentsView{AsOf: r.Now().UTC().Format(time.RFC3339), Completeness: fleet.CompletenessComplete}
	var in delivery.LadderInput
	var unread []string
	namespaces := &corev1.NamespaceList{}
	if err := r.Client.List(ctx, namespaces); err != nil {
		unread = append(unread, "the namespaces: "+err.Error())
	}
	for _, n := range namespaces.Items {
		in.Namespaces = append(in.Namespaces, n.Name)
	}
	unreadSteps := map[string]string{}
	pactos, err := Cluster{Client: r.Client}.pactos(ctx)
	if err != nil {
		unread = append(unread, "the Pactos: "+err.Error())
		unreadSteps[delivery.PlacementFromPacto] = "the Pactos could not be listed"
	}
	services := map[string]bool{}
	for _, p := range pactos {
		if c := p.Status.Contract; c != nil && c.ServiceName != "" {
			services[c.ServiceName] = true
		}
	}
	in.Services = slices.Sorted(maps.Keys(services))
	in.Advertised = delivery.AdvertisedEnvironments(pactos, r.ClusterName, r.NameSource)
	lr := r.readLadder(ctx)
	if lr.unread != "" {
		unread = append(unread, lr.unread)
	}
	view.Confirmed, view.Notes = lr.rungs, lr.notes
	known := slices.Clone(r.Ladder)
	for _, c := range lr.rungs {
		known = append(known, c.Name)
	}
	if f := lr.from; f != nil {
		view.Record, view.ConfirmedBy, view.ConfirmedAt = f.Ref, f.User, f.SignedAt.UTC().Format(time.RFC3339)
		known = append(known, f.Record.Dropped...) // a person dropped them: never asked again
		view.Dropped = f.Record.Dropped
	}
	view.LadderUnread = lr.unread
	if r.LadderRepository != "" {
		view.Repository, view.Stream = r.LadderRepository, delivery.LadderStream(r.ClusterID)
	}
	view.Declared = r.declaredRungs()
	view.Proposal = delivery.ProposeLadder(known, in)
	view.ProposalDigest = delivery.ProposalDigest(view.Proposal)
	st := placementState{namespaces: map[string]bool{}, pactos: pactos, occupants: map[string][]delivery.NamespaceOccupant{},
		ladder: slices.Concat(view.Declared, view.Confirmed), candidates: map[string]bool{},
		desired: map[string]string{}, unsure: map[string]bool{}, complete: lr.unread == "", unreadSteps: unreadSteps}
	if lr.from != nil {
		st.namespacesDigest = lr.from.Record.NamespacesDigest
	}
	placed, err := r.place(ctx, &view, st, in.Namespaces)
	if err != nil {
		return reconcile.Result{}, err
	}
	unread = append(unread, placed...)
	if len(unread) > 0 {
		view.Completeness = fleet.CompletenessPartial
		view.CompletenessReason = "could not read " + strings.Join(unread, "; ")
	}
	return reconcile.Result{RequeueAfter: r.Resync}, r.writeView(ctx, view)
}

// declaredRungs is the declared installation ladder as a chain of rungs.
func (r *EnvironmentReconciler) declaredRungs() []delivery.LadderRung {
	out := make([]delivery.LadderRung, 0, len(r.Ladder))
	for i, name := range r.Ladder {
		rung := delivery.LadderRung{Name: name, Source: ladderSource, Provenance: delivery.RungDeclared}
		if i > 0 {
			rung.Upstream = r.Ladder[i-1]
		}
		out = append(out, rung)
	}
	return out
}

// writeView writes v as view.json of EnvironmentsConfigMap.
func (r *EnvironmentReconciler) writeView(ctx context.Context, v delivery.EnvironmentsView) error {
	b, _ := json.Marshal(v) // ponytail: plain strings, slices and ints: Marshal cannot fail
	return r.writeConfigMap(ctx, delivery.EnvironmentsConfigMap, map[string]string{"view.json": string(b)})
}

// writeConfigMap creates the ConfigMap name in the delivery namespace holding
// data, or merge-patches it to data when it exists. It reads through
// APIReader, so the promoter caches no ConfigMap and needs get on this one
// name only.
func (r *EnvironmentReconciler) writeConfigMap(ctx context.Context, name string, data map[string]string) error {
	cm := &corev1.ConfigMap{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: name}, cm)
	if apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: r.Namespace, Name: name},
			Data:       data})
	}
	if err != nil {
		return err
	}
	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data = data
	return r.Client.Patch(ctx, cm, patch)
}

// ladderRead is the confirmed installation ladder one read of the ladder
// records gave: the confirmed rungs and the record they come from, notes on
// the records that did not count, and why the read is partial ("" when not).
type ladderRead struct {
	rungs  []delivery.LadderRung
	from   *delivery.VerifiedLadder
	notes  []string
	unread string
}

// readLadder reads the ladder records of this cluster from LadderRepository,
// verifies each under an approver signer, maps its certificate to a person and
// asks the apiserver whether that person may approve environmentladders. The
// newest counted record by verified signing time confirms the ladder. A
// listing or review that fails keeps the last ladder, partial, since an
// unread newer record may have dropped a rung. A record that can never count
// (it does not verify, is signed by an agent or another identity than it
// names, names another cluster or is older than the one that counts) is
// rejected, so it never takes the record bound again.
func (r *EnvironmentReconciler) readLadder(ctx context.Context) ladderRead {
	switch {
	case r.LadderRepository == "":
		return ladderRead{}
	case len(r.Policy.Signers) == 0:
		return ladderRead{notes: []string{"no approver signer is configured (delivery.identities.signers), so no ladder record counts"}}
	}
	stream := delivery.LadderStream(r.ClusterID)
	repo, err := ocireferrer.Open(r.LadderRepository, r.RecordOptions)
	var recs []deliveryrecord.Fetched
	var cut bool
	var forks []string
	if err == nil {
		rctx, cancel := delivery.WithRegistryTimeout(ctx)
		defer cancel()
		recs, cut, forks, err = r.records.ListLadder(rctx, repo, r.ClusterID)
	}
	if err != nil {
		return r.keepLast("the ladder records: " + err.Error())
	}
	var counted []delivery.VerifiedLadder
	notes := slices.Clone(forks)
	for _, f := range recs {
		v, why := r.openLadder(f)
		if why != "" {
			r.records.Reject(stream, delivery.PredicateEnvironmentLadder, f.Manifest)
			notes = append(notes, why)
			continue
		}
		allowed, err := r.environmentLadderReview(ctx, v.User)
		if err != nil {
			return r.keepLast("whether " + v.User + " may approve environmentladders: " + err.Error())
		}
		if !allowed {
			notes = append(notes, "a ladder record by "+v.User+" was not counted: "+v.User+" may not approve environmentladders")
			continue
		}
		counted = append(counted, v)
	}
	lr := ladderRead{}
	var note string
	lr.rungs, lr.from, note = delivery.ConfirmedLadder(counted, r.ClusterID, r.Ladder)
	for _, v := range counted {
		if v.SignedAt.Before(lr.from.SignedAt) {
			r.records.Reject(stream, delivery.PredicateEnvironmentLadder, v.Ref)
		}
	}
	lr.notes = slices.DeleteFunc(append(notes, note), func(s string) bool { return s == "" })
	r.last = lr
	if cut {
		lr.unread = fmt.Sprintf("more than %d ladder records; the newest may not have been read", delivery.MaxLadderRecords)
	}
	return lr
}

// keepLast is the last ladder read in full, partial because of why.
func (r *EnvironmentReconciler) keepLast(why string) ladderRead {
	lr := r.last
	lr.unread = why
	return lr
}

// openLadder opens f, a ladder record, under the approver signer whose issuer
// its certificate names, and maps the certificate to the user it speaks for.
// why says why the record can never count: it does not verify, names another
// cluster, is signed by an agent or by another identity than the one it
// names; "" when it may.
func (r *EnvironmentReconciler) openLadder(f deliveryrecord.Fetched) (delivery.VerifiedLadder, string) {
	var signer delivery.PublisherIdentity
	st, at, err := delivery.OpenLadderRecord(f.Bundle, func(b []byte) (time.Time, error) {
		var errs []error
		for _, s := range r.Policy.Signers {
			id, t, err := signing.VerifyPerson(b, s.Issuer, s.TrustedRoot)
			if err == nil {
				signer = id
				return t, nil
			}
			errs = append(errs, err)
		}
		return time.Time{}, errors.Join(errs...)
	})
	if err != nil {
		return delivery.VerifiedLadder{}, "a ladder record was not counted: " + err.Error()
	}
	id, source, err := delivery.ApproverIdentity(signer.Issuer, signer.Subject, r.Policy)
	if code, why := delivery.KeylessRefusal(id, source); err == nil && code != "" {
		err = errors.New(why)
	}
	switch {
	case err != nil:
	case st.Predicate.ClusterID != r.ClusterID:
		err = fmt.Errorf("it confirms the installation ladder of cluster %s", st.Predicate.ClusterID)
	case st.Predicate.Signer.Issuer != signer.Issuer || st.Predicate.Signer.Subject != signer.Subject:
		err = fmt.Errorf("it names the signer %s but is signed by %s", st.Predicate.Signer.Subject, signer.Subject)
	}
	if err != nil {
		return delivery.VerifiedLadder{}, "a ladder record signed by " + signer.Subject + " was not counted: " + err.Error()
	}
	return delivery.VerifiedLadder{Record: st.Predicate, User: id.User, Ref: f.Manifest, SignedAt: at}, ""
}

// environmentLadderResource is the virtual resource a person needs approve on
// to confirm the installation ladder: no CRD serves it.
var environmentLadderResource = authorizationv1.ResourceAttributes{
	Group: "delivery.pacto.trianalab.io", Resource: "environmentladders", Verb: "approve"}

// environmentLadderReview asks the apiserver whether user, with no groups (a
// certificate names none, and a record's claims are never asked), may approve
// environmentladders. A review the authorizers could not evaluate is an
// error, never a refusal or a grant. No answer is cached.
// ponytail: one review per counted record per pass, at most
// MaxLadderRecords; cache by user for a minute if reviews load the apiserver.
func (r *EnvironmentReconciler) environmentLadderReview(ctx context.Context, user string) (bool, error) {
	return reviewUser(ctx, r.Client, user, environmentLadderResource)
}

// reviewUser asks the apiserver c reaches whether user, with no groups, may
// do res. A review the authorizers could not evaluate is an error, never a
// refusal or a grant.
func reviewUser(ctx context.Context, c client.Client, user string, res authorizationv1.ResourceAttributes) (bool, error) {
	sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{User: user, ResourceAttributes: &res}}
	ctx, cancel := context.WithTimeout(ctx, accessTimeout)
	defer cancel()
	if err := c.Create(ctx, sar); err != nil {
		return false, err
	}
	if !sar.Status.Allowed && sar.Status.EvaluationError != "" {
		return false, errors.New("the review could not be evaluated: " + sar.Status.EvaluationError)
	}
	return sar.Status.Allowed, nil
}

// maxPlacementRows bounds the private placement rows: a ConfigMap holds at
// most 1 MiB. A var so tests lower it.
// ponytail: 2000 rows (the index's own MaxServices); more mark the answer
// partial. Shard the rows over ConfigMaps if an installation delivers more.
var maxPlacementRows = 2000

// indexReadTimeout bounds one read of the organization index.
const indexReadTimeout = 30 * time.Second

// placementState is what one pass places every delivered service from: the
// installation ladder and the rungs this cluster hosts, the cluster's
// namespaces, Pactos, hand-written Stages and GitOps objects, the verified
// placement rules, and the occupants of each namespace read so far; and what
// it keeps: every Stage by namespace/name and the reconciler's private state.
type placementState struct {
	ladder     []delivery.LadderRung
	hosted     []string
	advertised []delivery.AdvertisedEnvironment
	namespaces map[string]bool
	pactos     []delivery.PactoView
	stages     []delivery.StageObject
	byName     map[string]*dv1.Stage
	state      managedState
	gitops     []delivery.GitOpsTarget
	policy     []delivery.PlacementRule
	occupants  map[string][]delivery.NamespaceOccupant
	unread     []string
	// missed are the objects of delivered services this pass could not
	// read, update or retire. They name services and namespaces, so they go
	// to PlacementsConfigMap alone; the view counts them.
	missed []string
	// unreadSteps is, by placement step source, why what it reads could not
	// be read (delivery.PlacementInput.Unread).
	unreadSteps map[string]string
	// namespacesDigest is the NamespacesDigest the counted ladder record
	// binds; candidates collects the namespaces this pass would create once
	// a person allows it.
	namespacesDigest string
	candidates       map[string]bool
	// desired holds, by envKey, each environment a service read in full
	// should run in here, with when it expired ("" while it has not);
	// unsure, by envKey with no environment, the services whose accepted
	// contract could not be read. complete is false when the index or the
	// installation ladder was read in part: nothing is then retired.
	desired  map[string]string
	unsure   map[string]bool
	complete bool
	// events are what the pass reports once it wrote the state.
	events []envEvent
}

// place reads the organization index and places each delivered service in
// each hosted rung it should run in, writes the rows to PlacementsConfigMap
// and counts the unplaced ones in view. No registry, or an index that cannot
// be read, leaves the promoter idle with the reason and the last rows as
// they are. It returns what could not be read.
func (r *EnvironmentReconciler) place(ctx context.Context, view *delivery.EnvironmentsView, st placementState, namespaces []string) ([]string, error) {
	if r.Registry == "" {
		view.Idle = "no registry is configured (the chart value registry), so Pacto reads no organization index and places no service"
		return nil, nil
	}
	repo := r.Registry + "/" + orgindex.RepositoryName
	entries, unread, err := r.readIndex(ctx, repo)
	if err != nil {
		view.Idle = "the organization index at " + repo + " could not be read (" + err.Error() + "), so Pacto places no service"
		return []string{"the organization index: " + err.Error()}, nil
	}
	st.complete = st.complete && len(unread) == 0
	for _, ns := range namespaces {
		st.namespaces[ns] = true
	}
	st.advertised = delivery.AdvertisedEnvironments(nil, r.ClusterName, r.NameSource)
	st.hosted = delivery.HostedRungs(st.ladder, st.advertised)
	if st.state, err = r.readState(ctx); err != nil {
		view.Idle = "the private state in " + delivery.PlacementsConfigMap + " could not be read (" + err.Error() + "), so Pacto places no service"
		return append(unread, "the private state: "+err.Error()), nil
	}
	if d := st.namespacesDigest; d != "" && d == delivery.NamespacesDigest(st.state.candidates) {
		st.state.AllowedNamespaces = slices.Compact(slices.Sorted(slices.Values(slices.Concat(st.state.AllowedNamespaces, st.state.candidates))))
	}
	if err = r.readStages(ctx, &st); err != nil {
		view.Idle = "the Stages could not be listed (" + err.Error() + "), so Pacto places no service"
		return append(unread, "the Stages: "+err.Error()), nil
	}
	st.unread = unread
	st.gitops = r.gitOps(ctx, &st)
	if r.Placement != nil {
		pol, why := r.Placement(ctx)
		if pol != nil {
			st.policy = pol.Rules
		}
		if why != "" {
			st.unread = append(st.unread, why)
			if pol == nil {
				st.unreadSteps[delivery.PlacementFromPolicy] = "the placement policy could not be read"
			}
		}
	}
	rows := []delivery.PlacementRow{}
	for _, e := range entries {
		rows = append(rows, r.placeService(ctx, &st, e)...)
	}
	if len(rows) > maxPlacementRows {
		st.unread = append(st.unread, fmt.Sprintf("%d placement rows; only the first %d are kept", len(rows), maxPlacementRows))
		rows = rows[:maxPlacementRows]
	}
	for _, row := range rows {
		if row.Stage == "" {
			view.UnplacedCount++
		}
	}
	view.NamespaceCandidates = len(st.candidates)
	err = r.finishPass(ctx, rows, &st)
	if n := len(st.missed); n > 0 {
		st.unread = append(st.unread, fmt.Sprintf("%d objects of delivered services (%s lists them under %s)", n, delivery.PlacementsConfigMap, unreadKey))
	}
	return st.unread, err
}

// finishPass retires what is due, bounds the private state, writes it with
// rows and then reports the environment events it records.
func (r *EnvironmentReconciler) finishPass(ctx context.Context, rows []delivery.PlacementRow, st *placementState) error {
	r.retireManaged(ctx, st)
	st.bound()
	if err := r.writePlacements(ctx, rows, st); err != nil {
		return err
	}
	r.emitFromState(ctx, st.events)
	return nil
}

// readIndex reads the newest entry of each service the index repo lists,
// at most orgindex.MaxServices of them, through the shared entry reader.
// unread says what a bound cut or which entries were refused.
// ponytail: one entry at a time, every pass; read orgindex.Workers at once
// and keep entries by manifest digest if a large index makes a pass slow.
func (r *EnvironmentReconciler) readIndex(ctx context.Context, repo string) ([]orgindex.Entry, []string, error) {
	rr, err := ocireferrer.Open(repo, r.RecordOptions)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, indexReadTimeout)
	defer cancel()
	tags, err := ocireferrer.Tags(ctx, rr, "")
	if err != nil {
		return nil, nil, err
	}
	newest, _ := orgindex.Newest(tags)
	prefixes := slices.Sorted(maps.Keys(newest))
	var unread []string
	if bound := cmp.Or(r.maxServices, orgindex.MaxServices); len(prefixes) > bound {
		unread = append(unread, fmt.Sprintf("the organization index lists %d services; only the first %d were read", len(prefixes), bound))
		prefixes = prefixes[:bound]
	}
	var entries []orgindex.Entry
	refused := 0
	for _, p := range prefixes {
		e, err := orgindex.ReadEntry(ctx, rr, repo, newest[p], nil)
		if err != nil {
			refused++
			continue
		}
		entries = append(entries, e)
	}
	if refused > 0 {
		unread = append(unread, fmt.Sprintf("%d organization index entries could not be read or were refused", refused))
	}
	return entries, unread, nil
}

// placeService is the rows of the service e names: one waiting row when no
// version of its bundle repository has an accepted publisher verdict, none
// when its accepted contract deploys nothing, else one per environment it
// should run in that this cluster hosts.
func (r *EnvironmentReconciler) placeService(ctx context.Context, st *placementState, e orgindex.Entry) []delivery.PlacementRow {
	domain, name := fleet.ParseServiceKey(fleet.ServiceKey(e.Service))
	svc := delivery.ServiceRef{Domain: domain, Name: name}
	bundleRepo, _ := contract.SplitOCIDigest(e.Bundle)
	waiting := func(why string) []delivery.PlacementRow {
		st.unsure[envKey(domain, name, "")] = true
		return []delivery.PlacementRow{{Service: name, Domain: domain, Reason: why}}
	}
	if r.Accepted == nil {
		return waiting("waiting: publisher not trusted yet (the promoter has no publisher check)")
	}
	v, why, err := r.Accepted(ctx, svc, bundleRepo)
	switch {
	case err != nil:
		st.missed = append(st.missed, "the publisher check of "+bundleRepo+": "+err.Error())
		return waiting("the publisher check could not run: " + err.Error())
	case v == nil:
		return waiting("waiting: publisher not trusted yet (" + why + ")")
	}
	b, err := r.Cluster.Registry().Pull(ctx, bundleRepo+"@"+v.Digest)
	if err != nil {
		st.missed = append(st.missed, "the accepted bundle "+bundleRepo+"@"+v.Digest+": "+err.Error())
		return waiting("the accepted bundle " + bundleRepo + "@" + v.Digest + " could not be read: " + err.Error())
	}
	c := b.Contract
	if c == nil || c.Service.Name != name {
		st.unsure[envKey(domain, name, "")] = true
		return nil
	}
	if c.Publish == nil || c.Publish.Deploy == "" {
		return nil // Pacto delivers only what publishes a deploy artifact
	}
	in := delivery.PlacementInput{Service: svc, Owner: c.Service.Owner.Team, HostedRungs: st.hosted, ClusterUID: r.ClusterID, Policy: st.policy,
		Pactos: st.pactos, Stages: st.stages, BundleRepository: bundleRepo, GitOps: st.gitops, Namespaces: st.namespaces,
		SystemNamespaces: []string{r.Namespace, "flux-system", "argocd"}, Taken: map[string]string{}, CreateNamespaces: r.CreateNamespaces,
		Unread: st.unreadSteps}
	if rec, _ := contract.ReadInferredRecord(b.FS); rec != nil && rec.Declaration != nil {
		in.InferredFrom = rec.Declaration.InferredFrom
	}
	if v.Identity != nil {
		in.Repository = v.Identity.Repository
	}
	deploy := deployInput(c, v.Identity)
	var envs []placedEnv
	desired := delivery.DesiredEnvironments(st.ladder, c.Environments, r.Now())
	for _, d := range desired {
		if st.hosts(d.Name) { // else another cluster's
			st.desired[envKey(domain, name, d.Name)] = d.RetireFrom
			e := r.placeEnvironment(ctx, st, &in, d, desired)
			e.deploy = deploy
			envs = append(envs, e)
		}
	}
	r.manageService(ctx, st, svc, envs)
	rows := make([]delivery.PlacementRow, 0, len(envs))
	for _, e := range envs {
		rows = append(rows, *e.row)
	}
	return rows
}

// placeEnvironment is the row of d, one of the service's desired
// environments this cluster hosts: why it is not placed, or where it is
// placed and who applies the service there.
func (r *EnvironmentReconciler) placeEnvironment(ctx context.Context, st *placementState, in *delivery.PlacementInput, d delivery.DesiredEnvironment, desired []delivery.DesiredEnvironment) placedEnv {
	row := &delivery.PlacementRow{Service: in.Service.Name, Domain: in.Service.Domain, Environment: d.Name, Source: d.Source}
	e := placedEnv{row: row, upstream: d.Upstream}
	switch {
	case d.RetireFrom != "":
		row.Reason = "expired at " + d.RetireFrom + ": retiring"
	case d.Upstream != "" && !slices.ContainsFunc(desired, func(o delivery.DesiredEnvironment) bool { return o.Name == d.Upstream }):
		row.Reason = "the contract opts out of " + d.Upstream + ", which feeds " + d.Name + ": add " + d.Upstream + " to its environments[], since Pacto never skips an upstream gate"
	case d.Upstream != "" && !slices.Contains(st.hosted, d.Upstream):
		if e.remote, row.Reason = r.Peers.upstream(ctx, in.Service, d.Upstream, st.policy); e.remote != nil {
			r.placeIn(ctx, st, in, d.Name, &e)
		}
	default:
		r.placeIn(ctx, st, in, d.Name, &e)
	}
	return e
}

// placeIn places e in rung.
func (r *EnvironmentReconciler) placeIn(ctx context.Context, st *placementState, in *delivery.PlacementInput, rung string, e *placedEnv) {
	in.Rung = rung
	var p delivery.Placement
	p, e.manager, e.workload = r.resolve(ctx, st, *in)
	e.row.Placement = &p
	if p.Placed() {
		in.Taken[p.Namespace] = rung
	}
}

// hosts reports whether this cluster hosts environment name: a ladder rung
// when HostedRungs names it, an environment only the contract declares when
// the cluster's name advertises none or advertises it.
func (st *placementState) hosts(name string) bool {
	if slices.ContainsFunc(st.ladder, func(r delivery.LadderRung) bool { return r.Name == name }) {
		return slices.Contains(st.hosted, name)
	}
	return len(delivery.HostedRungs([]delivery.LadderRung{{Name: name}}, st.advertised)) == 1
}

// resolve places in, then reads who manages the service's workload in the
// namespace it chose and who else runs there, and places it again with
// that: the order never depends on them, only the guard and refusal do. It
// also returns that manager and the workload, nil for the Deployment named
// after the service.
func (r *EnvironmentReconciler) resolve(ctx context.Context, st *placementState, in delivery.PlacementInput) (delivery.Placement, workloadmanager.Manager, *delivery.WorkloadRef) {
	p := delivery.ResolvePlacement(in)
	ns := p.Namespace
	if ns == "" || p.Source == delivery.PlacementFromStage {
		return p, workloadmanager.Manager{}, nil
	}
	in.Managers = map[string]workloadmanager.Manager{}
	m, w, found := r.serviceManager(ctx, st, in, ns)
	occupants := r.occupants(ctx, st, ns)
	if found {
		in.Managers[ns] = m
		occupants = ownWorkload(occupants, w, in)
	}
	in.Occupants = map[string][]delivery.NamespaceOccupant{ns: occupants}
	return delivery.ResolvePlacement(in), m, w
}

// ownWorkload is occupants with the service's own workload, the one
// serviceManager found (w, nil for the Deployment named after it), credited
// to the service and its team: a namespace only the service occupies passes.
func ownWorkload(occupants []delivery.NamespaceOccupant, w *delivery.WorkloadRef, in delivery.PlacementInput) []delivery.NamespaceOccupant {
	self := "Deployment/" + in.Service.Name
	if w != nil {
		self = w.Kind + "/" + w.Name
	}
	out := slices.Clone(occupants)
	for i, o := range out {
		if o.Workload == self && o.Team == "" {
			out[i].Service, out[i].Team = in.Service.Name, in.Owner
		}
	}
	return out
}

// serviceManager detects the manager of the service's workload in ns: the
// one its Pacto there targets, else the one its contract was inferred from,
// else the Deployment named after it (w nil). found is false when there is
// none. A workload that cannot be read reads ambiguous, so nothing is
// created.
func (r *EnvironmentReconciler) serviceManager(ctx context.Context, st *placementState, in delivery.PlacementInput, ns string) (m workloadmanager.Manager, w *delivery.WorkloadRef, found bool) {
	kind, name := "Deployment", in.Service.Name
	if _, ins, k, n, ok := fleet.ParseK8sCaller(in.InferredFrom); ok && ins == ns {
		kind, name = k, n
	}
	for _, p := range delivery.PactosFor(st.pactos, ns, in.Service.Name) {
		if w := p.Spec.Target.Workload(); w != nil {
			kind, name = w.Kind, w.Name
		}
	}
	if kind != "Deployment" || name != in.Service.Name {
		w = &delivery.WorkloadRef{Kind: kind, Name: name}
	}
	o, err := r.Cluster.workloadObject(ctx, ns, kind, name)
	switch {
	case apierrors.IsNotFound(err):
		return workloadmanager.Manager{}, w, false
	case err != nil:
		st.missed = append(st.missed, kind+" "+ns+"/"+name+": "+err.Error())
		return workloadmanager.Manager{Name: workloadmanager.Ambiguous, Evidence: "it could not be read"}, w, true
	}
	return r.Cluster.Managers.Detect(o), w, true
}

// workloadKinds are the workloads whose owner a namespace's refusal checks.
// ponytail: Deployments, StatefulSets and DaemonSets; add batch kinds if a
// bare Job turns out to hold a namespace.
var workloadKinds = []string{"DeploymentList", "StatefulSetList", "DaemonSetList"}

// occupants are the Pactos and workloads of ns and whom each belongs to: a
// Pacto's own service and team, and an unknown owner for a workload no Pacto
// there targets. A namespace that cannot be read holds an unknown owner.
func (r *EnvironmentReconciler) occupants(ctx context.Context, st *placementState, ns string) []delivery.NamespaceOccupant {
	if o, ok := st.occupants[ns]; ok {
		return o
	}
	var out []delivery.NamespaceOccupant
	targeted := map[string]bool{}
	for _, p := range st.pactos {
		if p.Metadata.Namespace != ns {
			continue
		}
		o := delivery.NamespaceOccupant{Workload: "Pacto " + p.Metadata.Name}
		if c := p.Status.Contract; c != nil && c.Owner != nil {
			o.Service, o.Team = c.ServiceName, c.Owner.Team
		}
		if w := p.Spec.Target.Workload(); w != nil {
			targeted[w.Kind+"/"+w.Name] = true
		}
		out = append(out, o)
	}
	for _, kind := range workloadKinds {
		list := &metav1.PartialObjectMetadataList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: kind})
		if err := r.APIReader.List(ctx, list, client.InNamespace(ns)); err != nil {
			st.missed = append(st.missed, "the workloads in "+ns+": "+err.Error())
			out = append(out, delivery.NamespaceOccupant{Workload: "unreadable " + strings.TrimSuffix(kind, "List") + "s"})
			continue
		}
		for _, w := range list.Items {
			if what := strings.TrimSuffix(kind, "List") + "/" + w.Name; !targeted[what] {
				out = append(out, delivery.NamespaceOccupant{Workload: what})
			}
		}
	}
	st.occupants[ns] = out
	return out
}

// readStages lists the cluster's Stages live, so a Stage this pass or the
// last created is never missed: the ones whose UID the state holds are
// Pacto's, every other is hand-written, whatever its labels say. A Stage the
// state holds that is gone leaves the state (noteStages).
func (r *EnvironmentReconciler) readStages(ctx context.Context, st *placementState) error {
	list := &dv1.StageList{}
	if err := r.APIReader.List(ctx, list); err != nil {
		return err
	}
	st.byName = map[string]*dv1.Stage{}
	live := map[types.UID]*dv1.Stage{}
	for i := range list.Items {
		s := &list.Items[i]
		st.byName[s.Namespace+"/"+s.Name], live[s.UID] = s, s
		if !st.state.owns(s.UID) {
			st.stages = append(st.stages, delivery.StageObject{Metadata: delivery.ObjectMeta{Name: s.Name, Namespace: s.Namespace, Labels: s.Labels}, Spec: s.Spec})
		}
	}
	st.noteStages(live)
	return nil
}

var (
	gvkApplicationList   = gvkApplication.GroupVersion().WithKind("ApplicationList")
	gvkGitRepositoryList = schema.GroupVersionKind{Group: "source.toolkit.fluxcd.io", Version: "v1", Kind: "GitRepositoryList"}
)

// gitOps are the Argo CD Applications (when the promoter watches them) and
// Flux Kustomizations with a targetNamespace (unless it runs with
// --flux=false), each with the Git repositories it deploys. A kind the
// cluster does not serve has none; one that cannot be listed is unread.
func (r *EnvironmentReconciler) gitOps(ctx context.Context, st *placementState) []delivery.GitOpsTarget {
	var out []delivery.GitOpsTarget
	list := func(gvk schema.GroupVersionKind) []unstructured.Unstructured {
		l := &unstructured.UnstructuredList{}
		l.SetGroupVersionKind(gvk)
		err := r.APIReader.List(ctx, l)
		if err != nil && !meta.IsNoMatchError(err) {
			st.unread = append(st.unread, "the "+strings.TrimSuffix(gvk.Kind, "List")+"s: "+err.Error())
			st.unreadSteps[delivery.PlacementFromGitOps] = "the " + strings.TrimSuffix(gvk.Kind, "List") + "s could not be listed"
		}
		return l.Items
	}
	if r.Cluster.CacheApplications {
		for _, a := range list(gvkApplicationList) {
			g := delivery.GitOpsTarget{Kind: "Application", Namespace: a.GetNamespace(), Name: a.GetName()}
			g.Destination, _, _ = unstructured.NestedString(a.Object, "spec", "destination", "namespace")
			if u, _, _ := unstructured.NestedString(a.Object, "spec", "source", "repoURL"); u != "" {
				g.Repositories = append(g.Repositories, u)
			}
			srcs, _, _ := unstructured.NestedSlice(a.Object, "spec", "sources")
			for _, src := range srcs {
				if m, ok := src.(map[string]any); ok {
					if u, _ := m["repoURL"].(string); u != "" {
						g.Repositories = append(g.Repositories, u)
					}
				}
			}
			out = append(out, g)
		}
	}
	if r.Cluster.NoFlux {
		return out
	}
	urls := map[string]string{}
	for _, g := range list(gvkGitRepositoryList) {
		urls[g.GetNamespace()+"/"+g.GetName()], _, _ = unstructured.NestedString(g.Object, "spec", "url")
	}
	for _, k := range list(gvkKustomization) {
		g := delivery.GitOpsTarget{Kind: "Kustomization", Namespace: k.GetNamespace(), Name: k.GetName()}
		g.Destination, _, _ = unstructured.NestedString(k.Object, "spec", "targetNamespace")
		kind, _, _ := unstructured.NestedString(k.Object, "spec", "sourceRef", "kind")
		name, _, _ := unstructured.NestedString(k.Object, "spec", "sourceRef", "name")
		ns, _, _ := unstructured.NestedString(k.Object, "spec", "sourceRef", "namespace")
		if u := urls[cmp.Or(ns, k.GetNamespace())+"/"+name]; kind == "GitRepository" && u != "" {
			g.Repositories = []string{u}
		}
		out = append(out, g)
	}
	return out
}

// unreadKey is the key of PlacementsConfigMap that lists the objects of
// delivered services the last pass could not read, update or retire.
const unreadKey = "unread.json"

// writePlacements writes rows as rows.json, the private state as
// stages.json, the namespace candidates as NamespaceCandidatesKey and the
// objects missed as unreadKey of PlacementsConfigMap, which only the
// promoter writes and no reader binding names.
func (r *EnvironmentReconciler) writePlacements(ctx context.Context, rows []delivery.PlacementRow, st *placementState) error {
	b, _ := json.Marshal(rows)     // ponytail: plain strings and bools: Marshal cannot fail
	s, _ := json.Marshal(st.state) // and plain strings
	n, _ := json.Marshal(append([]string{}, slices.Sorted(maps.Keys(st.candidates))...))
	u, _ := json.Marshal(append([]string{}, st.missed...))
	return r.writeConfigMap(ctx, delivery.PlacementsConfigMap, map[string]string{"rows.json": string(b), stateKey: string(s),
		delivery.NamespaceCandidatesKey: string(n), unreadKey: string(u)})
}
