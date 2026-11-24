/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package member

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// RequestLabel marks an Operation raised for a member request, by the first
// 40 hex of the request's manifest digest; RequestAnnotation holds the whole
// digest.
const (
	RequestLabel      = "delivery.pacto.trianalab.io/member-request"
	RequestAnnotation = "delivery.pacto.trianalab.io/member-request"
)

const (
	// raisedWait is how long a raised Operation waits for its intent record
	// before its raised outcome is written without one.
	raisedWait = 2 * time.Minute
	// hubTTL is how long the hub derived from this cluster's own join
	// records is remembered.
	hubTTL = time.Minute
	// maxRemembered bounds each in-memory set; it only saves registry reads.
	maxRemembered = 4096
	// stagePart is how much of a Stage's name an Operation name keeps, so
	// "-req-" and 10 hex always fit in 63 characters.
	stagePart = 63 - len("-req-") - 10
)

// unverifiedRequests counts the member requests that did not verify: they
// are never answered, so an unauthenticated writer cannot make the member
// write.
var unverifiedRequests = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "pacto_member_requests_unverified_total",
	Help: "Member requests read from the registry that did not verify under the approver signers and were not answered.",
})

func init() { metrics.Registry.MustRegister(unverifiedRequests) }

// Intake reads this member's request stream in <registry>/pacto-targets and
// raises one promote, as an agent on the requester's behalf, for each
// request a person signed that is timely, allowed here and that the person
// may make here. Every refusal and conclusion is an outcome signed with the
// installation key, tied to its request; the outcome stream is the ledger
// that keeps each request to one Operation. It is a plain Runnable, so only
// the elected leader runs it.
type Intake struct {
	// Reader reads Stages and Operations uncached; Writer only creates
	// Operations.
	Reader client.Reader
	Writer client.Writer
	// Records reads the request stream in Repo; Signer signs outcomes with
	// Key, the installation key.
	Records *deliveryrecord.Reader
	Repo    *remote.Repository
	Signer  core.Signer
	Key     ed25519.PrivateKey
	// ClusterID is this cluster's uid; ClusterName and NameSource its
	// display name, which says the environments it hosts.
	ClusterID, ClusterName, NameSource string
	// WhoAmI says who the operator is, once at start; Self is that answer.
	WhoAmI func(context.Context) (core.Actor, error)
	Self   core.Actor
	// Policy reads the approver signers; Review asks whether a user may
	// create Operations in a namespace here.
	Policy func(context.Context) (core.ClassPolicy, error)
	Review func(ctx context.Context, user, namespace string) (bool, error)
	// Allow narrows what requests are taken for; Observed are the watched
	// namespaces (empty: every namespace).
	Allow    core.RequestAllowlist
	Observed []string
	// RecordedTTL is the recorder's Operation TTL; 0 adds no bound.
	RecordedTTL time.Duration
	Now         func() time.Time
	Interval    time.Duration
	Log         logr.Logger

	// open and openJoin verify a request and a join record; tests replace
	// them so they need no Sigstore.
	open     func(bundle []byte, p core.ClassPolicy) (core.OperationRequest, string, time.Time, core.ActorClass, string)
	openJoin func(p core.ClassPolicy, bundle []byte) core.VerifiedJoin

	done, logged map[string]bool
	hub          string
	hubAt        time.Time

	// issuers are the approver issuers of the last policy read, nil before
	// one; the target publisher reads them through JoinRequests.
	mu      sync.Mutex
	issuers []string
}

// JoinRequests is what the join block says about requests: nil when the
// intake is off, before it read the approver signers, with no signer or
// with no environment the allowlist takes, so a member never claims
// requests it cannot verify or take. Otherwise the signers' issuers and the
// environments RequestAllowlist.Hosted takes (nil: every environment).
func (i *Intake) JoinRequests() *core.JoinRequests {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	issuers := i.issuers
	i.mu.Unlock()
	hosted := i.Allow.Hosted(core.AdvertisedEnvironments(nil, i.ClusterName, i.NameSource))
	if len(issuers) == 0 || (hosted != nil && len(hosted) == 0) {
		return nil
	}
	return &core.JoinRequests{Issuers: issuers, Environments: hosted}
}

// Start reads the operator's identity, then polls until ctx ends. A failed
// identity read stops the intake with a logged reason; the operator keeps
// running.
func (i *Intake) Start(ctx context.Context) error {
	if i.open == nil {
		i.open = openRequest
	}
	if i.openJoin == nil {
		i.openJoin = signing.VerifyJoinRecord
	}
	self, err := i.WhoAmI(ctx)
	if err != nil {
		i.Log.Error(err, "member requests are off: the operator could not read its own identity")
		return nil
	}
	i.Self = self
	t := time.NewTicker(i.Interval)
	defer t.Stop()
	for {
		i.poll(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// poll answers the requests and writes the outcomes of raised Operations.
func (i *Intake) poll(ctx context.Context) {
	policy, err := i.Policy(ctx)
	if err != nil {
		i.Log.Error(err, "member requests wait: the delivery identity policy could not be read")
		return
	}
	issuers := []string{}
	for _, sg := range policy.Signers {
		issuers = append(issuers, sg.Issuer)
	}
	slices.Sort(issuers)
	i.mu.Lock()
	i.issuers = slices.Compact(issuers)
	i.mu.Unlock()
	rctx, cancel := core.WithRegistryTimeout(ctx)
	recs, _, err := i.Records.ListOperationRequests(rctx, i.Repo, i.ClusterID)
	cancel()
	if err != nil {
		i.Log.Error(err, "the member request stream could not be read; retrying on the next poll")
	}
	for _, f := range recs {
		i.handle(ctx, policy, f)
	}
	i.watchConcluded(ctx)
}

// request is one verified request and its manifest digest.
type request struct {
	manifest string
	req      core.OperationRequest
	user     string
	signedAt time.Time
}

// refusal is an intake refusal: its code, reason and the Stage's namespace.
type refusal struct{ code, why, namespace string }

// handle answers one request read from the stream.
func (i *Intake) handle(ctx context.Context, policy core.ClassPolicy, f deliveryrecord.Fetched) {
	if i.done[f.Manifest] {
		i.finish(f.Manifest)
		return
	}
	r, class, ok := i.verify(policy, f)
	if !ok || !i.unanswered(ctx, f.Manifest) {
		return
	}
	if class != core.ClassHuman {
		i.refuse(ctx, r, refusal{code: core.RefusalNotAPerson,
			why: r.user + " signed it as " + string(class) + ": only a person signs a member request"})
		return
	}
	if raised, err := i.raisedAlready(ctx, f.Manifest); err != nil || raised {
		return // its raised outcome follows from watchConcluded
	}
	ref, stage, err := i.admitRequest(ctx, r)
	switch {
	case err != nil:
		i.Log.Error(err, "a member request waits for the next poll", "request", f.Manifest)
	case ref.code != "":
		i.refuse(ctx, r, ref)
	default:
		if err := i.raise(ctx, policy, r, stage); err != nil {
			i.Log.Error(err, "the Operation of a member request could not be created", "request", f.Manifest)
		}
	}
}

// verify opens f under policy. A request that does not verify, or whose
// person signer the policy does not accept, is counted, logged once and
// never read again; it is answered with nothing.
func (i *Intake) verify(policy core.ClassPolicy, f deliveryrecord.Fetched) (request, core.ActorClass, bool) {
	r := request{manifest: f.Manifest}
	var class core.ActorClass
	var why string
	r.req, r.user, r.signedAt, class, why = i.open(f.Bundle, policy)
	if class == "" || class == core.ClassHuman && why != "" {
		unverifiedRequests.Inc()
		if !i.logged[f.Manifest] {
			i.logged = remember(i.logged, f.Manifest)
			i.Log.Info("a member request did not verify and was not answered", "request", f.Manifest, "why", why)
		}
		i.finish(f.Manifest)
		return r, "", false
	}
	return r, class, true
}

// unanswered says whether no outcome this member signed answers the request
// yet. A refused or raised one finishes it; a listing that fails, or is cut
// without such an outcome, answers nothing until the next poll.
func (i *Intake) unanswered(ctx context.Context, manifest string) bool {
	states, ok := i.outcomes(ctx, manifest)
	if states[core.RequestRefused] || states[core.RequestRaised] {
		i.finish(manifest)
		return false
	}
	return ok
}

// outcomes are the states of the outcomes of request this member's own key
// signed, and whether the listing was read whole. Any other outcome is
// rejected so it leaves the bound.
func (i *Intake) outcomes(ctx context.Context, request string) (map[core.RequestState]bool, bool) {
	rctx, cancel := core.WithRegistryTimeout(ctx)
	defer cancel()
	recs, cut, err := i.Records.ListRequestOutcomes(rctx, i.Repo, i.ClusterID, request)
	if err != nil {
		return nil, false
	}
	pub, _ := core.PublicKeyPEM(i.Key.Public()) // an Ed25519 key always encodes
	trust := &core.TrustConfig{Keys: []core.TrustedKey{{Name: "installation", PublicKey: pub, Streams: []string{i.ClusterID + "/*/*"}}}}
	states := map[core.RequestState]bool{}
	for _, f := range recs {
		st, err := core.OpenRequestOutcome(f.Bundle, trust, i.ClusterID, signing.OpenKey)
		if err != nil || st.Predicate.Request != request {
			i.Records.Reject(core.ClusterRequestStream(i.ClusterID), core.PredicateRequestOutcome, f.Manifest)
			continue
		}
		states[st.Predicate.State] = true
	}
	return states, !cut
}

// raisedAlready says whether an Operation carries the request's label: the
// intake raised it and its raised outcome is not written yet.
func (i *Intake) raisedAlready(ctx context.Context, manifest string) (bool, error) {
	var ops dv1.OperationList
	if err := i.Reader.List(ctx, &ops, client.MatchingLabels{RequestLabel: label(manifest)}); err != nil {
		return false, err
	}
	return slices.ContainsFunc(ops.Items, func(o dv1.Operation) bool { return o.Spec.Actor.User == i.Self.User }), nil
}

// admitRequest says why r is refused here, or the one Stage it promotes. An
// error (a read or a review that failed) answers nothing until the next poll.
func (i *Intake) admitRequest(ctx context.Context, r request) (refusal, *dv1.Stage, error) {
	if r.req.Cluster != i.ClusterID {
		return refusal{code: core.RefusalNotForThisCluster, why: "the request names cluster " + r.req.Cluster + "; this member is " + i.ClusterID}, nil, nil
	}
	if code, why := r.req.Timely(r.signedAt, i.Now(), i.RecordedTTL); code != "" {
		return refusal{code: code, why: why}, nil, nil
	}
	stage, ref, err := i.resolveStage(ctx, r.req)
	if err != nil || ref.code != "" {
		return ref, nil, err
	}
	if ref, err := i.moved(ctx, r, stage); err != nil || ref.code != "" {
		return ref, nil, err
	}
	allowed, err := i.Review(ctx, r.user, stage.Namespace)
	if err != nil {
		return refusal{}, nil, err
	}
	if !allowed {
		return refusal{code: core.RefusalNotAllowedHere, namespace: stage.Namespace,
			why: r.user + " may not create operations.delivery.pacto.trianalab.io in namespace " + stage.Namespace + " on this member"}, nil, nil
	}
	return refusal{}, stage, nil
}

// resolveStage is the one Stage of r's service and environment in a
// namespace the allowlist takes.
func (i *Intake) resolveStage(ctx context.Context, r core.OperationRequest) (*dv1.Stage, refusal, error) {
	var list dv1.StageList
	if err := i.Reader.List(ctx, &list); err != nil {
		return nil, refusal{}, err
	}
	advertised := core.AdvertisedEnvironments(nil, i.ClusterName, i.NameSource)
	var found int
	var allowed []dv1.Stage
	var outside refusal
	for _, s := range list.Items {
		if s.Spec.Service != r.Service || s.Spec.Environment != r.Environment {
			continue
		}
		found++
		if code, why := i.Allow.Refusal(r.Service, s.Namespace, r.Environment, i.Observed, advertised); code != "" {
			outside = refusal{code: code, why: why, namespace: s.Namespace}
			continue
		}
		allowed = append(allowed, s)
	}
	switch {
	case found == 0:
		return nil, refusal{code: core.RefusalNoStage, why: "this member has no Stage of " + r.Service.Name + " in " + r.Environment}, nil
	case len(allowed) == 0:
		return nil, outside, nil
	case len(allowed) > 1:
		var names []string
		for _, s := range allowed {
			names = append(names, s.Namespace+"/"+s.Name)
		}
		return nil, refusal{code: core.RefusalAmbiguousStage,
			why: fmt.Sprintf("%d Stages of %s in %s: %s", len(allowed), r.Service.Name, r.Environment, strings.Join(names, ", "))}, nil
	}
	return &allowed[0], refusal{}, nil
}

// moved says why the Stage changed since r was signed: it runs the digest,
// it shipped it before, or the Operation that last moved it was created
// after the signature. A late or replayed request never rolls it back.
func (i *Intake) moved(ctx context.Context, r request, st *dv1.Stage) (refusal, error) {
	why := ""
	switch {
	case r.req.Digest == st.Status.Pointer:
		why = "the Stage already runs " + r.req.Digest
	case slices.Contains(st.Status.Verified, r.req.Digest):
		why = "the Stage shipped " + r.req.Digest + " before and moved on"
	case st.Status.LastOp != "":
		var ops dv1.OperationList
		if err := i.Reader.List(ctx, &ops, client.InNamespace(st.Namespace)); err != nil {
			return refusal{}, err
		}
		if k := slices.IndexFunc(ops.Items, func(o dv1.Operation) bool { return string(o.UID) == st.Status.LastOp }); k >= 0 &&
			ops.Items[k].CreationTimestamp.After(r.signedAt) {
			why = "operation " + ops.Items[k].Name + " moved the Stage after the request was signed"
		}
	}
	if why == "" {
		return refusal{}, nil
	}
	return refusal{code: core.RefusalPointerMoved, why: why + ": the Stage changed after " + r.user + " asked; plan again"}, nil
}

// label is the request label value of a request manifest digest.
func label(manifest string) string { return strings.TrimPrefix(manifest, "sha256:")[:40] }

// operationName is the Operation a request raises on stage: the Stage's
// name cut to 48 characters, "-req-" and 10 hex of the request digest.
func operationName(stage, manifest string) string {
	return stage[:min(len(stage), stagePart)] + "-req-" + strings.TrimPrefix(manifest, "sha256:")[:10]
}

// raise creates the promote r asks for on st, as an agent on the
// requester's behalf. An Operation of that name already there writes
// nothing new.
func (i *Intake) raise(ctx context.Context, policy core.ClassPolicy, r request, st *dv1.Stage) error {
	hub := r.req.Hub
	if hub != i.ownHub(ctx, policy) {
		hub += " (claimed)"
	}
	reason := fmt.Sprintf("requested by %s from hub %s (request %s)", r.user, hub, r.manifest[:min(len(r.manifest), 19)])
	if r.req.Reason != "" {
		reason += ": " + r.req.Reason
	}
	spec := core.OperationSpec{Type: core.TypePromote, Stage: st.Name, Facet: core.FacetPointer,
		Params: core.Params{From: st.Status.Pointer, To: r.req.Digest}, Reason: reason, CorrelationID: r.req.CorrelationID,
		Actor: i.Self, OnBehalfOf: &core.Principal{User: r.user}}
	spec.IntentDigest = core.IntentDigest(spec, st.Spec)
	op := &dv1.Operation{ObjectMeta: metav1.ObjectMeta{Namespace: st.Namespace, Name: operationName(st.Name, r.manifest),
		Labels: map[string]string{RequestLabel: label(r.manifest)}, Annotations: map[string]string{RequestAnnotation: r.manifest},
		Finalizers: []string{dv1.RecordFinalizer}}, Spec: spec}
	if err := i.Writer.Create(ctx, op); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	i.Log.Info("raised a promote for a member request", "request", r.manifest, "operation", st.Namespace+"/"+op.Name, "requester", r.user)
	return nil
}

// ownHub is the hub that approved this cluster's own key, by the join
// records its approver signers verify (core.OwnHub); "" when none or
// several did, or the records could not be read. It is remembered a minute.
func (i *Intake) ownHub(ctx context.Context, policy core.ClassPolicy) string {
	if now := i.Now(); now.Before(i.hubAt.Add(hubTTL)) {
		return i.hub
	}
	rctx, cancel := core.WithRegistryTimeout(ctx)
	defer cancel()
	recs, _, _, err := i.Records.ListClusterJoins(rctx, i.Repo, i.ClusterID)
	if err != nil {
		return ""
	}
	var vs []core.VerifiedJoin
	for _, f := range recs {
		vs = append(vs, i.openJoin(policy, f.Bundle))
	}
	var why string
	i.hub, why = core.OwnHub(i.ClusterID, signing.Hint(i.Key.Public()), vs)
	i.hubAt = i.Now()
	if why != "" {
		i.Log.Info("member requests name their hub as claimed", "why", why)
	}
	return i.hub
}

// refuse writes r's refused outcome and, once written, finishes r.
func (i *Intake) refuse(ctx context.Context, r request, ref refusal) {
	o := core.RequestOutcome{Request: r.manifest, Cluster: i.ClusterID, State: core.RequestRefused, Code: ref.code, Reason: ref.why,
		Next: core.RefusalNext(ref.code, r.req, ref.namespace), Digest: r.req.Digest}
	if i.record(ctx, o) {
		i.finish(r.manifest)
	}
}

// record signs o with the installation key and pushes it to this member's
// request stream; false when it could not, so the next poll tries again.
func (i *Intake) record(ctx context.Context, o core.RequestOutcome) bool {
	o.At = i.Now().UTC().Format(time.RFC3339)
	bundle, err := core.SignRequestOutcome(ctx, i.Signer, core.NewRequestOutcomeStatement(o))
	if err == nil {
		rctx, cancel := core.WithRegistryTimeout(ctx)
		_, _, err = deliveryrecord.PushRequestOutcome(rctx, i.Repo, bundle)
		cancel()
	}
	if err != nil {
		i.Log.Error(err, "a member request outcome could not be written; retrying on the next poll", "request", o.Request, "state", o.State)
		return false
	}
	return true
}

// finish rejects a request this member is done with, so it leaves the
// listing bound and newer ones are read.
func (i *Intake) finish(manifest string) {
	i.Records.Reject(core.ClusterRequestStream(i.ClusterID), core.PredicateOperationRequest, manifest)
	i.done = remember(i.done, manifest)
}

// watchConcluded writes the raised and concluded outcomes of the Operations
// this operator raised for requests. A label someone else writes produces
// none: the actor must be the operator itself.
func (i *Intake) watchConcluded(ctx context.Context) {
	var ops dv1.OperationList
	if err := i.Reader.List(ctx, &ops, client.HasLabels{RequestLabel}); err != nil {
		i.Log.Error(err, "the Operations of member requests could not be listed; retrying on the next poll")
		return
	}
	for k := range ops.Items {
		op := &ops.Items[k]
		if req := op.Annotations[RequestAnnotation]; op.Spec.Actor.User == i.Self.User && req != "" && !i.done[req+"/concluded"] {
			i.conclude(ctx, req, op)
		}
	}
}

// conclude writes op's raised outcome once its intent record is written (or
// after raisedWait without one, or once it concluded), then its concluded
// outcome once it concluded.
func (i *Intake) conclude(ctx context.Context, req string, op *dv1.Operation) {
	states, ok := i.outcomes(ctx, req)
	if !ok {
		return
	}
	ref := &core.RequestedOperation{Namespace: op.Namespace, Name: op.Name, UID: string(op.UID), Stage: op.Spec.Stage}
	if op.Status.IntentRecord != nil {
		ref.IntentRecord = op.Status.IntentRecord.Ref
	}
	base := core.RequestOutcome{Request: req, Cluster: i.ClusterID, Operation: ref, Digest: op.Spec.Params.To}
	if !states[core.RequestRaised] {
		if ref.IntentRecord == "" && op.Status.Outcome == "" && i.Now().Sub(op.CreationTimestamp.Time) < raisedWait {
			return
		}
		raised := base
		raised.State = core.RequestRaised
		if ref.IntentRecord == "" {
			raised.Reason = "this member keeps no intent records"
		}
		if !i.record(ctx, raised) {
			return
		}
	}
	if op.Status.Outcome == "" {
		return
	}
	if !states[core.RequestConcluded] {
		concluded := base
		concluded.State, concluded.Outcome, concluded.Published = core.RequestConcluded, op.Status.Outcome, published(op.Status.Verdicts)
		concluded.Reason = strings.TrimSuffix(op.Status.Reason+": "+op.Status.Message, ": ")
		if !i.record(ctx, concluded) {
			return
		}
	}
	i.done = remember(i.done, req+"/concluded")
}

// published is the message of the pacto.published verdict, "" without one.
func published(vs []core.Verdict) string {
	if k := slices.IndexFunc(vs, func(v core.Verdict) bool { return v.Gate == core.GatePublished }); k >= 0 {
		return string(vs[k].Result) + ": " + vs[k].Message
	}
	return ""
}

// remember adds k to m, clearing m when it is full.
// ponytail: cleared whole when full; it only saves registry reads.
func remember(m map[string]bool, k string) map[string]bool {
	if m == nil || len(m) >= maxRemembered {
		m = map[string]bool{}
	}
	m[k] = true
	return m
}

// openRequest verifies a request bundle under the approver signers of p,
// as a person's record that names its requester.
func openRequest(bundle []byte, p core.ClassPolicy) (core.OperationRequest, string, time.Time, core.ActorClass, string) {
	var req core.OperationRequest
	user, at, class, why := signing.VerifyPersonRecord(p, "member request", func(verify func([]byte) error) (string, error) {
		st, err := core.OpenOperationRequest(bundle, verify)
		req = st.Predicate
		return req.Requester, err
	})
	return req, user, at, class, why
}

// SelfActor says who this cluster's apiserver says c's user is: the actor
// every Operation the intake raises names, which admission holds it to.
func SelfActor(c client.Client) func(context.Context) (core.Actor, error) {
	return func(ctx context.Context) (core.Actor, error) {
		ssr := &authenticationv1.SelfSubjectReview{}
		if err := c.Create(ctx, ssr); err != nil {
			return core.Actor{}, err
		}
		return core.Actor{User: ssr.Status.UserInfo.Username, Groups: ssr.Status.UserInfo.Groups}, nil
	}
}

// MayPropose asks this cluster, as c's user, whether a user with no groups
// (a certificate names none) may create Operations in a namespace. A
// refusal that carries an evaluation error is no answer.
func MayPropose(c client.Client) func(ctx context.Context, user, namespace string) (bool, error) {
	return func(ctx context.Context, user, namespace string) (bool, error) {
		sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{User: user,
			ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: namespace, Verb: "create",
				Group: dv1.GroupVersion.Group, Resource: "operations"}}}
		if err := c.Create(ctx, sar); err != nil {
			return false, err
		}
		if !sar.Status.Allowed && sar.Status.EvaluationError != "" {
			return false, errors.New(sar.Status.EvaluationError)
		}
		return sar.Status.Allowed, nil
	}
}

// ClassPolicyFrom reads the delivery identity policy from the ConfigMap key
// names, as clients read it.
func ClassPolicyFrom(r client.Reader, key types.NamespacedName) func(context.Context) (core.ClassPolicy, error) {
	return func(ctx context.Context) (core.ClassPolicy, error) {
		var cm corev1.ConfigMap
		if err := r.Get(ctx, key, &cm); err != nil {
			return core.ClassPolicy{}, err
		}
		return core.ParseClassPolicy([]byte(cm.Data[core.PolicyKey]))
	}
}
