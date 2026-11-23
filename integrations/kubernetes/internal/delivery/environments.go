/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
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

	// records remembers what each ladder record manifest names, and the
	// records that never count; last is the ladder the last full read gave.
	records deliveryrecord.Reader
	last    ladderRead
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
	pactos, err := Cluster{Client: r.Client}.pactos(ctx)
	if err != nil {
		unread = append(unread, "the Pactos: "+err.Error())
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
	}
	if r.LadderRepository != "" {
		view.Repository, view.Stream = r.LadderRepository, delivery.LadderStream(r.ClusterID)
	}
	for i, name := range r.Ladder {
		rung := delivery.LadderRung{Name: name, Source: ladderSource, Provenance: delivery.RungDeclared}
		if i > 0 {
			rung.Upstream = r.Ladder[i-1]
		}
		view.Declared = append(view.Declared, rung)
	}
	view.Proposal = delivery.ProposeLadder(known, in)
	view.ProposalDigest = delivery.ProposalDigest(view.Proposal)
	if len(unread) > 0 {
		view.Completeness = fleet.CompletenessPartial
		view.CompletenessReason = "could not read " + strings.Join(unread, "; ")
	}
	return reconcile.Result{RequeueAfter: r.Resync}, r.writeView(ctx, view)
}

// writeView creates EnvironmentsConfigMap, or merge-patches its view.json
// when it exists. It reads through APIReader, so the promoter caches no
// ConfigMap and needs get on this one name only.
func (r *EnvironmentReconciler) writeView(ctx context.Context, v delivery.EnvironmentsView) error {
	b, _ := json.Marshal(v) // ponytail: plain strings, slices and ints: Marshal cannot fail
	cm := &corev1.ConfigMap{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: delivery.EnvironmentsConfigMap}, cm)
	if apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: r.Namespace, Name: delivery.EnvironmentsConfigMap},
			Data:       map[string]string{"view.json": string(b)}})
	}
	if err != nil {
		return err
	}
	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data = map[string]string{"view.json": string(b)}
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
	res := environmentLadderResource
	sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{User: user, ResourceAttributes: &res}}
	ctx, cancel := context.WithTimeout(ctx, accessTimeout)
	defer cancel()
	if err := r.Client.Create(ctx, sar); err != nil {
		return false, err
	}
	if !sar.Status.Allowed && sar.Status.EvaluationError != "" {
		return false, errors.New("the review could not be evaluated: " + sar.Status.EvaluationError)
	}
	return sar.Status.Allowed, nil
}
