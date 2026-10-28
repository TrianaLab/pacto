package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/diff"
)

// The browser demo's delivery: an in-memory dashboard.DeliveryAPI over the
// payments story (examples/story). There is no cluster, no promoter and no
// registry. Every plan is delivery.PlanFor over the story's real contracts,
// every check after release is ContractVerdict or RuntimeVerdict over a fixed
// Pacto evaluation (the health check is a recorded verdict), every record is
// built by delivery.NewOperationRecord, signed with a key minted in the tab
// and read back with OpenRecord, FoldStream and the history, blame and undo
// assembly the CLI and the cluster dashboard use. What stands in for the
// promoter is the small lifecycle below: it moves the pointer, runs the
// checks and concludes. Nothing leaves the tab and a reload starts over.

// clusterID names the stream of every Stage here.
const clusterID = "browser-demo"

var personas = map[string]story.Persona{
	story.PayAgent.Name: story.PayAgent,
	story.Alice.Name:    story.Alice,
	story.Carol.Name:    story.Carol,
}

// displayNames are the names the simulated identity provider gives.
var displayNames = map[string]string{story.PayAgent.Name: "pay-agent", story.Alice.Name: "Alice", story.Carol.Name: "Carol"}

type stream struct {
	recs       []delivery.StoredRecord
	unreadable []string
}

type demoDelivery struct {
	mu    sync.Mutex
	now   func() time.Time
	later func(func()) // runs the promoter's next step
	at    time.Time    // the clock while the story is replayed at load

	persona string
	authAt  map[string]time.Time

	stages    []*delivery.StageObject
	ops       []*delivery.OperationObject
	approvals map[string]delivery.ApprovalSpec
	streams   map[string]*stream
	n         int

	key     ed25519.PrivateKey
	keyHint string

	// views are the copies the reads hand out, made once per change: a
	// copy per Stage on every read was most of each answer's time on a
	// large fleet. changed drops them.
	views *demoViews
}

type demoViews struct {
	stages []delivery.StageObject
	ops    []delivery.OperationObject
}

// changed drops the views after the world moved.
func (d *demoDelivery) changed() { d.views = nil }

// snapshot is the views of the world as it is now, under the lock.
func (d *demoDelivery) snapshot() *demoViews {
	if d.views == nil {
		v := &demoViews{}
		for _, s := range d.stages {
			v.stages = append(v.stages, d.view(s))
		}
		for _, o := range d.ops {
			v.ops = append(v.ops, clone(*o))
		}
		d.views = v
	}
	return d.views
}

// newDemoDelivery replays the story up to the change that waits for a person
// (B5) and starts as pay-agent. later schedules each next step of the
// promoter, which then takes the lock; nil runs it at once, under the lock
// the caller holds.
func newDemoDelivery(now func() time.Time, later func(func())) (*demoDelivery, error) {
	_, key, _ := ed25519.GenerateKey(rand.Reader) // crypto/rand never fails
	sum := sha256.Sum256(key.Public().(ed25519.PublicKey))
	d := &demoDelivery{now: now, later: later, persona: story.PayAgent.Name, approvals: map[string]delivery.ApprovalSpec{},
		streams: map[string]*stream{}, key: key, keyHint: hex.EncodeToString(sum[:4])}
	t := now()
	d.authAt = map[string]time.Time{story.PayAgent.Name: t, story.Alice.Name: t.Add(-30 * time.Minute), story.Carol.Name: t.Add(-30 * time.Minute)}
	return d, d.replay(t.Add(-6 * time.Hour))
}

// KeyHint names the key the tab signs records with.
func (d *demoDelivery) KeyHint() string { return d.keyHint }

// SetPersona switches who the tab acts as; fresh signs them in again now.
func (d *demoDelivery) SetPersona(name string, fresh bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := personas[name]; !ok {
		return fmt.Errorf("no persona %q", name)
	}
	d.persona = name
	if fresh {
		d.authAt[name] = d.now()
	}
	return nil
}

// State is the simulated session: always signed in.
func (d *demoDelivery) State(*http.Request) dashboard.AuthState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return dashboard.AuthState{SignedIn: true, CSRF: "browser-demo", Name: displayNames[d.persona]}
}

// ── the world ───────────────────────────────────────────────────────────

func stageKey(ns, name string) string { return ns + "/" + name }

func newStage(service, env string) *delivery.StageObject {
	s := &delivery.StageObject{Metadata: delivery.ObjectMeta{Name: service, Namespace: env, UID: "stage-" + env + "-" + service}}
	s.Spec = delivery.StageSpec{Service: delivery.ServiceRef{Name: service}, Environment: env, ApplyRef: delivery.ApplyRef{Name: service}}
	s.Spec.Gates.Impact.DeclaredConsumersOnly = true // every service in the story has a contract
	s.Spec.Gates.Verify.Checks = story.Checks(service, env)
	// Most simulated Stages have already climbed the autonomy ladder, so the
	// story's agent promotes plan as they would on a real autonomous Stage.
	// fraud is new and observes (replay).
	s.Status.Level = delivery.LevelAutonomous
	if env == story.Test {
		s.Spec.Candidates.OCIRepositoryRef = &delivery.LocalRef{Name: service + "-candidates"}
	} else {
		s.Spec.Candidates.UpstreamStage = &delivery.StageRef{Name: service, Namespace: story.Test}
	}
	return s
}

func (d *demoDelivery) stage(ns, name string) *delivery.StageObject {
	for _, s := range d.stages {
		if s.Metadata.Namespace == ns && s.Metadata.Name == name {
			return s
		}
	}
	return nil
}

func (d *demoDelivery) op(ns, name string) *delivery.OperationObject {
	for _, o := range d.ops {
		if o.Metadata.Namespace == ns && o.Metadata.Name == name {
			return o
		}
	}
	return nil
}

// versionOf is the story version a digest carries, "" when none does.
func versionOf(service, digest string) string {
	for _, v := range storyVersions[service] {
		if story.ArtifactDigest(service, v) == digest {
			return v
		}
	}
	return ""
}

var storyVersions = map[string][]string{
	"payments":  {"2.3.0", "2.4.0", "2.4.2", "2.4.3", "3.0.0"},
	"checkout":  {"1.8.0", "1.9.0"},
	"orders":    {"1.4.0"},
	"fraud":     {"1.0.0"},
	"pay-agent": {"1.0.0", "1.1.0"},
}

// uncontracted are the workloads the node agent saw call a service in an
// environment that have no contract, by "namespace/service": the cold start.
var uncontracted = map[string][]string{"test/payments": {"ledger-export"}}

func (d *demoDelivery) clock() time.Time {
	if !d.at.IsZero() {
		d.at = d.at.Add(7 * time.Minute)
		return d.at
	}
	return d.now()
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ── replay at load ──────────────────────────────────────────────────────

// replay builds the world the guided path starts from: every Stage, what
// pacto publish published, and the story's beats up to B4 played through the
// same plan and lifecycle as a live change, then the agent beat. fraud is new:
// its Stages observe and its history is read from status only, as on a
// cluster without the recorder. The payments test stream holds a deliberate
// gap and a record signed by a key nobody trusts.
func (d *demoDelivery) replay(start time.Time) error {
	d.at = start
	defer func() { d.at = time.Time{} }()
	for _, svc := range []string{"checkout", "fraud", "orders", "pay-agent", "payments"} {
		for _, env := range []string{story.Test, story.Prod} {
			s := newStage(svc, env)
			if svc != "fraud" {
				s.Status.Stream = delivery.Stream{ID: clusterID + "/" + env + "/" + svc, Anchor: "registry.example/story/records@sha256:" + svc + env}
				s.Status.Conditions = []delivery.Condition{{Type: delivery.ConditionRecordStoreReady, Status: "True"}}
				d.streams[stageKey(env, svc)] = &stream{}
			} else {
				// pacto init starts a new Stage on observe. On test, fraud's
				// team labelled the applyRef and asks for a proposal after one
				// verified version (the default is 10), so the demo can show one.
				s.Status.Level = delivery.LevelObserve
				s.Status.Conditions = []delivery.Condition{{Type: "Ready", Status: "True", Reason: delivery.ReasonObserving}}
				if env == story.Test {
					s.Status.Conditions[0].Reason = delivery.ReasonObservingGuarded
					s.Spec.Autonomy.ProposeAfter = 1
				}
			}
			if env == story.Test {
				for _, v := range storyVersions[svc] {
					if v != "2.4.3" { // refused by the version rule (B4), so never published
						s.Status.Published = append(s.Status.Published, story.ArtifactDigest(svc, v))
					}
				}
			}
			d.stages = append(d.stages, s)
		}
	}
	// Placed before the story starts.
	for _, p := range []struct{ svc, env, v string }{
		{"payments", story.Test, "2.3.0"}, {"payments", story.Prod, "2.3.0"},
		{"checkout", story.Test, "1.8.0"}, {"checkout", story.Prod, "1.8.0"}, {"orders", story.Prod, "1.4.0"},
	} {
		s, dg := d.stage(p.env, p.svc), story.ArtifactDigest(p.svc, p.v)
		s.Status.Pointer, s.Status.Verified, s.Status.Offered = dg, []string{dg}, []string{dg}
	}
	// pactify: GitOps applied fraud 1.0.0 in test, and Pacto only noted what
	// it would have done.
	if err := d.observe(story.Test, "fraud", "1.0.0"); err != nil {
		return err
	}
	steps := []struct{ svc, env, to string }{
		{"payments", story.Test, "2.4.0"}, // B1 in test
		{"payments", story.Prod, "2.4.0"}, // B1, then the health check fails, B2 rolls it back and prod steps down
		{"payments", story.Test, "2.4.2"}, // B3: the claim check fails, and test steps down
		{"checkout", story.Test, "1.9.0"},
		{"payments", story.Test, "3.0.0"}, // B4: the signed 3.0.0, which ledger-export's missing contract holds
		{"orders", story.Test, "1.4.0"},
		{"pay-agent", story.Test, "1.0.0"}, // the agent beat: 1.0.0 passes its evals
		{"pay-agent", story.Prod, "1.0.0"},
		{"pay-agent", story.Test, "1.1.0"}, // and 1.1.0 passes 1 of 2, rolls back and steps test down
	}
	for _, s := range steps {
		req := delivery.Request{Namespace: s.env, Stage: s.svc, To: story.ArtifactDigest(s.svc, s.to)}
		o, _, err := d.propose(req, story.PayAgent)
		if err != nil {
			return err
		}
		if o.Status.Phase == delivery.PhaseHeld {
			// Test auto-promotes only non-breaking changes since 2.4.2 failed,
			// and a workload with no contract calls payments: alice, on its
			// team, accepts that unknown.
			d.decide(o, d.callerOf(story.Alice), o.Status.Plan.Floor.Decision, "ledger-export only reads payment ids, which 3.0.0 keeps")
		}
		if s.to == "2.4.2" {
			d.gapAndStranger(d.stage(s.env, s.svc))
		}
	}
	for _, s := range d.stages {
		s.Status.Proposal = d.proposal(s)
	}
	return nil
}

// observe is the stage reconciler on a Stage that observes, after GitOps
// applied version: it plans what a fully automatic Stage would have done with
// it (the shadow plan, advisory and unsigned), then runs the Stage's checks
// on it once its verify window passed.
func (d *demoDelivery) observe(env, svc, version string) error {
	st := d.stage(env, svc)
	dg, from := story.ArtifactDigest(svc, version), ""
	if r := st.Status.ObservedRevision; r != nil {
		from = r.Digest
	}
	saved := st.Status
	st.Status.Level, st.Status.Pointer = delivery.LevelAutonomous, from
	p, _, err := d.plan(delivery.Request{Namespace: env, Stage: svc, To: dg, Reason: "shadow plan while the Stage observes"}, story.Promoter)
	st.Status = saved
	if err != nil {
		return err
	}
	at := d.clock()
	e := delivery.Shadow{Candidate: dg, From: from, Predicted: p.Predicted, IntentDigest: p.IntentDigest, ImpactDigest: p.ImpactDigest,
		PlannedAt: stamp(at), ObservedAt: stamp(at), Question: p.Question}
	if p.Floor.Human {
		e.Decision, e.Owners = p.Floor.Decision, p.Floor.Owners
	}
	st.Status.ObservedRevision = &delivery.ObservedRevision{Digest: dg, Since: stamp(at)}
	if delivery.Worst(d.checks(env, svc, dg, delivery.GateInput{Facet: delivery.FacetPointer}, at)) == delivery.Pass {
		e.Verification = delivery.Verified
		st.Status.Verified = delivery.AddDigest(st.Status.Verified, dg, delivery.MaxVerified)
		if slices.Contains(st.Status.Published, dg) {
			st.Status.Offered = delivery.AddDigest(st.Status.Offered, dg, delivery.MaxOffered)
		}
	}
	st.Status.Shadow = append([]delivery.Shadow{e}, st.Status.Shadow...)
	return nil
}

// proposal is the level Pacto proposes next for s, or why not, as the stage
// reconciler writes it: on auto-nonbreaking it reads the graph from the plan
// of the operation that last moved the pointer.
func (d *demoDelivery) proposal(s *delivery.StageObject) *delivery.Proposal {
	var latest *delivery.Plan
	if delivery.CurrentLevel(s.Spec, s.Status) == delivery.LevelAutoNonBreaking {
		for _, o := range d.ops {
			if o.Metadata.UID == s.Status.LastOp {
				latest = o.Status.Plan
			}
		}
	}
	return delivery.ProposeLevel(s.Spec, s.Status, latest)
}

// health is the recorded Mira verdict that fails payments 2.4.0 in prod (B2).
var health = []delivery.Verdict{{Gate: "health", Phase: "verify", Result: delivery.Fail,
	Citation: map[string]string{"provider": "mira", "query": `sum(rate(http_server_errors{service="payments"}[5m])) / sum(rate(http_server_requests{service="payments"}[5m]))`,
		"threshold": "0.01", "observed": "0.074"},
	Message: "recorded for this demo: the error ratio was 7.4% against a limit of 1% in the verify window"}}

// thin is the recorded Mira verdict on payments 3.0.0 in prod (B8): too
// little telemetry arrived to judge it, so the check says Unknown.
var thin = []delivery.Verdict{{Gate: "health", Phase: "verify", Result: delivery.Unknown,
	Citation: map[string]string{"provider": "mira", "query": health[0].Citation["query"], "spans": "3", "needed": "50"},
	Message:  "recorded for this demo: only 3 spans arrived in the verify window, too few to judge the error ratio (50 needed)"}}

// recorded holds the recorded verdicts that fail a version on a Stage, or
// leave it Unknown, by "namespace/stage@version".
var recorded = map[string][]delivery.Verdict{"prod/payments@2.4.0": health, "prod/payments@3.0.0": thin}

// gapAndStranger adds the two deliberate problems to a stream: a seq whose
// operation was gone before it was recorded and a record signed by a key the
// tab does not trust.
func (d *demoDelivery) gapAndStranger(st *delivery.StageObject) {
	s := d.streams[stageKey(st.Metadata.Namespace, st.Metadata.Name)]
	st.Status.Stream.Seq++
	chain := delivery.Chain{Stream: st.Status.Stream.ID, Seq: int64(len(s.recs) + 1), Parent: st.Status.Stream.Head}
	gap := delivery.NewGap(clusterID, st.Metadata, st.Status.Stream.Seq, chain, st.Status.Stream.Anchor, stamp(d.clock()))
	d.store(st, gap, d.key)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	odd := gap
	odd.Predicate.Chain.Seq, odd.Predicate.Time.Finished = 99, stamp(d.clock())
	d.store(st, odd, stranger)
}

// ── signing ─────────────────────────────────────────────────────────────

// pae is DSSE's pre-authentication encoding, what the signature covers.
func pae(payloadType string, payload []byte) []byte {
	return fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload)
}

type edSigner struct{ key ed25519.PrivateKey }

// Sign writes a minimal Sigstore bundle: one DSSE envelope, no certificate
// and no transparency log, because the key exists only in this tab.
func (s edSigner) Sign(_ context.Context, payloadType string, payload []byte) ([]byte, error) {
	sig := ed25519.Sign(s.key, pae(payloadType, payload))
	b := map[string]any{"mediaType": delivery.BundleMediaType, "dsseEnvelope": map[string]any{
		"payload": payload, "payloadType": payloadType,
		"signatures": []map[string]string{{"sig": base64.StdEncoding.EncodeToString(sig)}}}}
	return json.Marshal(b)
}

// verifier checks a bundle was signed by the tab's key.
func (d *demoDelivery) verifier(_ string, _ int64, bundle []byte) error {
	if err := verifyDSSE(d.key.Public().(ed25519.PublicKey), bundle); err != nil {
		return errors.New("not signed by the key this tab trusts")
	}
	return nil
}

// verifyDSSE checks bundle is one DSSE envelope signed by pub.
func verifyDSSE(pub ed25519.PublicKey, bundle []byte) error {
	var b struct {
		DSSEEnvelope struct {
			Payload     []byte `json:"payload"`
			PayloadType string `json:"payloadType"`
			Signatures  []struct {
				Sig string `json:"sig"`
			} `json:"signatures"`
		} `json:"dsseEnvelope"`
	}
	if err := json.Unmarshal(bundle, &b); err != nil || len(b.DSSEEnvelope.Signatures) != 1 {
		return errors.New("not one DSSE signature")
	}
	sig, _ := base64.StdEncoding.DecodeString(b.DSSEEnvelope.Signatures[0].Sig)
	if !ed25519.Verify(pub, pae(b.DSSEEnvelope.PayloadType, b.DSSEEnvelope.Payload), sig) {
		return errors.New("the signature does not verify")
	}
	return nil
}

// ── the eval runner ─────────────────────────────────────────────────────

// evalCounts are the cases the story's stand-in runner counts as passed of
// all, by pay-agent version: 1.1.0 swaps its model and passes 1 of its 2.
var evalCounts = map[string][2]int{"1.0.0": {2, 2}, "1.1.0": {1, 2}}

// storyRunner answers a suite gate's request in the tab, as the story's
// stand-in runner: it counts with evalCounts and signs its answer with the
// story runner key (story.SignEval). It is a delivery.Doer, not an
// http.Client, which would link an HTTP client and TLS into the download.
type storyRunner struct{}

func (storyRunner) Do(r *http.Request) (*http.Response, error) {
	var q struct {
		Input delivery.GateInput `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil || len(q.Input.Subjects) == 0 {
		return nil, errors.New("not a suite gate request")
	}
	n := evalCounts[versionOf(q.Input.Service.Name, q.Input.Subjects[0])]
	body := story.SignEval(q.Input, "story-evals/"+q.Input.OperationUID, n[0], n[1])
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
}

// runners checks an answer was signed by a runner key the gate provider
// trusts: the story's runner, by its PEM public key.
var runners, _ = delivery.RunnersOf([]delivery.TrustedKey{story.RunnerTrust()}, func(k delivery.TrustedKey) (func([]byte) error, error) {
	blk, _ := pem.Decode([]byte(k.PublicKey))
	pub, err := x509.ParsePKIXPublicKey(blk.Bytes) // story.RunnerTrust is always an Ed25519 PEM key
	return func(b []byte) error { return verifyDSSE(pub.(ed25519.PublicKey), b) }, err
})

// gates are the providers a Stage check may name: the suite runner.
var gates = map[string]delivery.Gate{"evals": delivery.Eval{URL: "https://evals.story.example/run", Client: storyRunner{},
	Verify: runners, Revision: func(_ context.Context, in delivery.GateInput) delivery.Revision {
		r, _ := story.Revision(in.Service.Name, versionOf(in.Service.Name, in.Subjects[0]))
		return r
	}}}

// store signs st with key and reads it back as a registry reader would: a
// record that verifies joins the stream, one that does not is unreadable.
func (d *demoDelivery) store(stage *delivery.StageObject, st delivery.Statement, key ed25519.PrivateKey) string {
	s := d.streams[stageKey(stage.Metadata.Namespace, stage.Metadata.Name)]
	bundle, _, _ := delivery.SignRecord(context.Background(), edSigner{key}, st) // ed25519 signing cannot fail
	sum := sha256.Sum256(bundle)
	manifest := "sha256:" + hex.EncodeToString(sum[:])
	got, id, err := delivery.OpenRecord(bundle, d.verifier)
	if err != nil {
		s.unreadable = append(s.unreadable, manifest+": "+err.Error())
		return ""
	}
	s.recs = append(s.recs, delivery.StoredRecord{ID: id, Manifest: manifest, Statement: got})
	stage.Status.Stream.Head, stage.Status.Stream.RecordedSeq = id, st.Predicate.Chain.Seq
	return id
}

// ── planning ────────────────────────────────────────────────────────────

func (d *demoDelivery) caller() delivery.Caller { return d.callerOf(personas[d.persona]) }

// callerOf is p as the simulated sign-in names them.
func (d *demoDelivery) callerOf(p story.Persona) delivery.Caller {
	return delivery.Caller{Identity: delivery.Identity{User: p.User, Groups: p.Groups}, Class: p.Class(),
		AuthTime: stamp(d.authAt[p.Name]), Policy: story.Policy}
}

// spec turns a request into the intent, as the CLI and the cluster
// dashboard do.
func (d *demoDelivery) spec(req delivery.Request, st *delivery.StageObject, actor story.Persona) (delivery.OperationSpec, error) {
	typ := cmpOr(req.Type, delivery.TypePromote)
	t, ok := delivery.TypeByID(typ)
	if !ok {
		return delivery.OperationSpec{}, dashboard.Refuse(http.StatusBadRequest, "UnknownType", "unknown operation type "+typ)
	}
	spec := delivery.OperationSpec{Type: typ, Stage: req.Stage, Facet: t.Facet, Reason: req.Reason, Cause: req.Cause,
		Actor: delivery.Actor{User: actor.User, Groups: actor.Groups}}
	switch {
	case typ == delivery.TypeRollback:
		cited := d.op(req.Namespace, req.Undoes)
		if cited == nil {
			return spec, dashboard.Refuse(http.StatusNotFound, "NotFound", "a rollback undoes one operation, and "+req.Undoes+" is not here")
		}
		spec.Params = delivery.Params{From: cited.Status.Pointer.After, To: cited.Status.Pointer.Before, Undoes: cited.Metadata.UID}
	case t.Facet == delivery.FacetPointer:
		if versionOf(req.Stage, req.To) == "" {
			return spec, dashboard.Refuse(http.StatusBadRequest, "UnknownVersion", req.To+" is not a version of "+req.Stage+" in this demo")
		}
		spec.Params = delivery.Params{From: cmpOr(req.From, st.Status.Pointer), To: req.To}
	case t.Facet == delivery.FacetLevel:
		spec.Params = delivery.Params{From: cmpOr(req.From, string(delivery.CurrentLevel(st.Spec, st.Status))), To: req.To}
	}
	spec.IntentDigest = delivery.IntentDigest(spec, st.Spec)
	return spec, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// plan is delivery.PlanFor over the story's contracts and this world: what
// else runs in the Stage's environment and what each consumer's test Stage
// verified, read from the Stages as they are now.
func (d *demoDelivery) plan(req delivery.Request, actor story.Persona) (delivery.Plan, delivery.OperationSpec, error) {
	st := d.stage(req.Namespace, req.Stage)
	if st == nil {
		return delivery.Plan{}, delivery.OperationSpec{}, dashboard.Refuse(http.StatusNotFound, "NotFound", "no Stage "+stageKey(req.Namespace, req.Stage))
	}
	spec, err := d.spec(req, st, actor)
	if err != nil {
		return delivery.Plan{}, spec, err
	}
	b := story.Beat{ID: "live", Service: req.Stage, Environment: req.Namespace, Type: spec.Type, Actor: actor,
		From: versionOf(req.Stage, spec.Params.From), To: versionOf(req.Stage, spec.Params.To),
		Running: map[string]string{}, Verified: map[string][]string{}, Uncontracted: uncontracted[stageKey(req.Namespace, req.Stage)]}
	switch b.Type {
	case delivery.TypePromote:
		b.Type = ""
	case delivery.TypeAutonomy: // From and To are levels; Version is what runs
		running := st.Status.Pointer
		if r := st.Status.ObservedRevision; running == "" && r != nil {
			running = r.Digest
		}
		b.From, b.To, b.Version = spec.Params.From, spec.Params.To, versionOf(req.Stage, running)
	}
	d.surroundings(&b, req)
	in, err := b.PlanInput(context.Background())
	if err != nil {
		return delivery.Plan{}, spec, dashboard.Refuse(http.StatusBadRequest, "Unplannable", err.Error())
	}
	in.Spec, in.Stage, in.Status = spec, st.Spec, st.Status
	in.Status.Proposal = d.proposal(st)
	if up := st.Spec.Candidates.UpstreamStage; up != nil {
		in.Upstream = &d.stage(up.Namespace, up.Name).Status
	}
	return delivery.PlanFor(context.Background(), in), spec, nil
}

// surroundings fills b with what else runs in the Stage's environment and,
// for prod, what each consumer's test Stage verified.
func (d *demoDelivery) surroundings(b *story.Beat, req delivery.Request) {
	for _, o := range d.stages {
		svc := o.Metadata.Name
		if svc == req.Stage || o.Status.Pointer == "" {
			continue
		}
		if o.Metadata.Namespace == req.Namespace {
			b.Running[svc] = versionOf(svc, o.Status.Pointer)
		}
		if req.Namespace == story.Prod && o.Metadata.Namespace == story.Test {
			for _, v := range o.Status.Verified {
				b.Verified[svc] = append(b.Verified[svc], versionOf(svc, v))
			}
		}
	}
}

// ── the promoter, in miniature ──────────────────────────────────────────

func (d *demoDelivery) propose(req delivery.Request, actor story.Persona) (*delivery.OperationObject, delivery.Plan, error) {
	p, spec, err := d.plan(req, actor)
	if err != nil {
		return nil, p, err
	}
	if req.IntentDigest != "" && req.IntentDigest != p.IntentDigest {
		e := dashboard.Refuse(http.StatusConflict, "PlanStale", "the Stage moved since you planned; review the fresh plan and propose again")
		e.Plan = &p
		return nil, p, e
	}
	st := d.stage(req.Namespace, req.Stage)
	if spec.Type == delivery.TypeRollback {
		cited := d.op(req.Namespace, req.Undoes)
		why := delivery.AdmitRollback(spec, st.Status, &delivery.CitedOp{UID: cited.Metadata.UID, Type: cited.Spec.Type,
			Applied: cited.Status.Applied, Pointer: cited.Status.Pointer}, actor.Class())
		if why != "" {
			return nil, p, dashboard.Refuse(http.StatusForbidden, "Refused", why)
		}
	}
	d.n++
	t := d.clock()
	o := &delivery.OperationObject{Metadata: delivery.ObjectMeta{Namespace: req.Namespace, UID: fmt.Sprintf("op-%d", d.n),
		Name: fmt.Sprintf("%s-%s-%d", req.Stage, strings.TrimPrefix(spec.Type, "delivery.pacto.trianalab.io/"), d.n), CreationTimestamp: stamp(t)}, Spec: spec}
	o.Status.ActorClass, o.Status.Plan, o.Status.Verdicts, o.Status.Times.Admitted = actor.Class(), &p, p.Verdicts, stamp(t)
	d.ops = append(d.ops, o)
	switch p.Predicted {
	case delivery.PredictRejected:
		o.Status.Reason = strings.Join(p.Conflicts, "; ")
		d.conclude(o, delivery.Rejected, "")
	case delivery.PredictHeld:
		o.Status.Phase, o.Status.Question = delivery.PhaseHeld, p.Question
	case delivery.PredictWaits:
		o.Status.Phase, o.Status.Reason = delivery.PhasePending, strings.Join(p.Conflicts, "; ")
	default:
		d.start(o)
	}
	return o, p, nil
}

// start runs an admitted operation: it moves the pointer, then checks the
// version in its verify window, then concludes.
func (d *demoDelivery) start(o *delivery.OperationObject) {
	o.Status.Phase, o.Status.Times.Started = delivery.PhaseRunning, stamp(d.clock())
	if o.Spec.Facet == delivery.FacetLevel { // a level change is the swap itself: nothing to check after it
		o.Status.Applied, o.Status.Times.Applied = true, o.Status.Times.Started
		d.conclude(o, delivery.Succeeded, "")
		return
	}
	d.step(func() {
		st := d.stage(o.Metadata.Namespace, o.Spec.Stage)
		o.Status.Pointer = delivery.PointerChange{Before: st.Status.Pointer, After: o.Spec.Params.To}
		o.Status.Applied, o.Status.Phase, o.Status.Times.Applied = true, delivery.PhaseVerifying, stamp(d.clock())
		st.Status.Pointer, st.Status.LastOp = o.Spec.Params.To, o.Metadata.UID
		if delivery.SetsAutoHold(o.Spec, o.Status.ActorClass, st.Status) {
			st.Status.AutoHold = true
		}
		d.step(func() { d.verify(o) })
	})
}

// step runs f as the promoter's next move, under the lock.
func (d *demoDelivery) step(f func()) {
	if !d.at.IsZero() || d.later == nil {
		f() // replaying at load, or no waiting asked for
		return
	}
	d.later(func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		f()
		d.changed()
	})
}

// pactoView is the Pacto evaluation of service at version in env, a fixed
// input: Compliant, with the node agent's full view, plus findings.
func pactoView(env string, r delivery.Revision, findings ...delivery.FindingView) delivery.PactoView {
	var p delivery.PactoView
	c := r.Bundle.Contract
	p.Metadata.Namespace, p.Metadata.Name = env, c.Service.Name
	p.Status.ContractStatus, p.Status.LastReconciledAt, p.Status.Findings = "Compliant", stamp(time.Now()), findings
	p.Status.EvaluatedRevision = &delivery.EvaluatedView{ContractDigest: delivery.ContractDigest(r.ContractRef), KernelCoverage: "Complete"}
	return p
}

// undeclared is what the node agent saw payments 2.4.2 do (B3).
var undeclared = delivery.FindingView{Code: "DEPENDENCY_UNDECLARED", Severity: "error", Subject: "fraud",
	Message: "payments calls fraud, which its contract does not declare", EvidenceRefs: []delivery.EvidenceRefView{{Collector: "ebpf", DetectedAt: "in the verify window"}}}

// checks are the checks after release of digest on a Stage: the contract and
// runtime claim checks over the fixed Pacto evaluation, the recorded health
// verdict and the Stage's own checks (pay-agent's evals, run by the story's
// signing runner). in names the operation, if any, and the facet.
func (d *demoDelivery) checks(env, svc, digest string, in delivery.GateInput, at time.Time) []delivery.Verdict {
	v := versionOf(svc, digest)
	r, _ := story.Revision(svc, v)
	var findings []delivery.FindingView
	if svc == "payments" && v == "2.4.2" {
		findings = append(findings, undeclared)
	}
	c := delivery.ContractCheck{ContractRef: r.ContractRef, Applied: true, Pactos: []delivery.PactoView{pactoView(env, r, findings...)}}
	verdicts := []delivery.Verdict{delivery.ContractVerdict(c), delivery.RuntimeVerdict(c)}
	if f := recorded[stageKey(env, svc)+"@"+v]; f != nil {
		verdicts = append(verdicts, f...)
	} else {
		verdicts = append(verdicts, delivery.Verdict{Gate: "health", Phase: "verify", Result: delivery.Pass,
			Citation: map[string]string{"provider": "mira"}, Message: "recorded for this demo: the error ratio stayed under 1% in the verify window"})
	}
	if checks := d.stage(env, svc).Spec.Gates.Verify.Checks; len(checks) > 0 {
		in.Stage, in.Service, in.Subjects, in.Version = delivery.StageRef{Name: svc, Namespace: env}, delivery.ServiceRef{Name: svc}, []string{digest}, v
		in.Window = delivery.Window{From: at, To: at.Add(5 * time.Minute)}
		verdicts = append(verdicts, delivery.RunChecks(context.Background(), gates, checks, in)...)
	}
	return verdicts
}

// verify runs the checks after release, then concludes or rolls back.
func (d *demoDelivery) verify(o *delivery.OperationObject) {
	verdicts := d.checks(o.Metadata.Namespace, o.Spec.Stage, o.Spec.Params.To,
		delivery.GateInput{OperationUID: o.Metadata.UID, Facet: o.Spec.Facet}, d.clock())
	o.Status.Verdicts = append(o.Status.Verdicts, verdicts...)
	switch delivery.Worst(verdicts) {
	case delivery.Fail:
		d.conclude(o, delivery.Failed, delivery.Unverified)
		d.autoRollback(o, verdicts, "a check failed after release")
		return
	case delivery.Unknown:
		// As the promoter does when the verify window ends Unknown: the
		// version stays and a person accepts the unknown or rejects it.
		var msgs []string
		for _, x := range verdicts {
			if x.Result == delivery.Unknown {
				msgs = append(msgs, x.Gate+": "+x.Message)
			}
		}
		msg := strings.Join(msgs, "; ")
		o.Status.Phase, o.Status.Reason, o.Status.Message = delivery.PhaseHeld, "VerifyUnknown", msg
		o.Status.Question = "The verify gates could not confirm " + o.Spec.Params.To + " in time: " + msg +
			". Accept the unknown (the version stays), or reject it (it is rolled back)?"
		return
	}
	d.conclude(o, delivery.Succeeded, delivery.Verified)
}

// autoRollback is the promoter undoing a version that failed its checks,
// once, citing the failed verdict.
func (d *demoDelivery) autoRollback(failed *delivery.OperationObject, verdicts []delivery.Verdict, reason string) {
	var cause *delivery.Verdict
	for _, v := range verdicts {
		if v.Result == delivery.Fail {
			cause = &v
			break
		}
	}
	if failed.Status.Pointer.Before != "" {
		req := delivery.Request{Namespace: failed.Metadata.Namespace, Stage: failed.Spec.Stage, Type: delivery.TypeRollback,
			Undoes: failed.Metadata.Name, Reason: reason,
			Cause: &delivery.Cause{Operation: failed.Metadata.UID, Verdict: cause}}
		_, _, _ = d.propose(req, story.Promoter) // bound to the failed change by construction
	}
	d.stepDown(failed, cause)
}

// stepDown is the promoter lowering the Stage one level after a promote that
// shipped with no person on an automatic level failed a check, as the
// operator's raiseStepDown does: it needs no approval, cites the failure and
// never goes below advise.
func (d *demoDelivery) stepDown(failed *delivery.OperationObject, cause *delivery.Verdict) {
	st := d.stage(failed.Metadata.Namespace, failed.Spec.Stage)
	from := delivery.CurrentLevel(st.Spec, st.Status)
	to, _ := from.Below()
	if failed.Spec.Type != delivery.TypePromote || failed.Status.ActorClass == delivery.ClassHuman || len(failed.Status.Approvals) > 0 ||
		cause == nil || !from.Automatic() {
		return
	}
	req := delivery.Request{Namespace: failed.Metadata.Namespace, Stage: failed.Spec.Stage, Type: delivery.TypeAutonomy,
		From: string(from), To: string(to),
		Reason: "automatic step-down: " + failed.Metadata.Name + ", which shipped with no person on " + string(from) + ", failed (" + cause.Gate + ")",
		Cause:  &delivery.Cause{Operation: failed.Metadata.UID, Verdict: cause}}
	_, _, _ = d.propose(req, story.Promoter)
}

// conclude ends an operation: it takes the Stage's next seq and, where the
// Stage has a stream, its signed record. Then every held change is planned
// again, as the promoter does when what it waits for moves.
func (d *demoDelivery) conclude(o *delivery.OperationObject, outcome delivery.Outcome, verification delivery.Verification) {
	st := d.stage(o.Metadata.Namespace, o.Spec.Stage)
	st.Status.Stream.Seq++
	seq := st.Status.Stream.Seq
	o.Status.Phase, o.Status.Outcome, o.Status.Verification = delivery.PhaseConcluded, outcome, verification
	o.Status.Seq, o.Status.StageUID, o.Status.Times.Finished, o.Status.Policy = seq, st.Metadata.UID, stamp(d.clock()), &st.Spec
	done := delivery.Concluded{UID: o.Metadata.UID, Seq: seq, Outcome: outcome, Verification: verification, Reason: o.Status.Reason,
		Type: o.Spec.Type, Person: len(o.Status.Approvals) > 0 || o.Status.ActorClass == delivery.ClassHuman,
		ClearedBreaking: delivery.ClearedBreaking(o.Status.Plan)}
	if c := o.Spec.Cause; c != nil && o.Status.ActorClass == delivery.ClassController {
		done.Cause = c.Operation // only the promoter's own cause excuses what follows
	}
	st.Status.Stream.Concluded = append(st.Status.Stream.Concluded, done)
	st.Status.Stream.Concluded = st.Status.Stream.Concluded[max(0, len(st.Status.Stream.Concluded)-delivery.MaxConcluded):]
	before := st.Status.Pointer
	// The promoter's own rules for what the Stage remembers.
	delivery.ApplyEffects(&st.Status, o.Metadata.Name, o.Metadata.UID, o.Spec, o.Status,
		delivery.Conclusion{Outcome: outcome, Verification: verification, Reason: o.Status.Reason})
	to, from := o.Spec.Params.To, o.Spec.Params.From
	if o.Spec.Facet == delivery.FacetLevel { // leaving observe adopts the pointer; the record shows it
		o.Status.Pointer = delivery.PointerChange{}
		if st.Status.Pointer != before {
			o.Status.Pointer = delivery.PointerChange{Before: before, After: st.Status.Pointer}
		}
		to, from = o.Status.Pointer.After, o.Status.Pointer.Before
	}
	st.Status.Proposal = d.proposal(st)
	if s := d.streams[stageKey(st.Metadata.Namespace, st.Metadata.Name)]; s != nil {
		r, _ := story.Revision(o.Spec.Stage, versionOf(o.Spec.Stage, to))
		in := delivery.RecordInput{ClusterID: clusterID, Stage: *st, Operation: *o, Approvals: d.approvals,
			Subjects: delivery.RecordSubjects{To: to, From: from, Contract: delivery.ContractDigest(r.ContractRef),
				Anchor: "sha256:" + strings.Repeat("a", 64)},
			Chain: delivery.Chain{Stream: st.Status.Stream.ID, Seq: int64(len(s.recs) + 1), Parent: st.Status.Stream.Head}}
		if id := d.store(st, delivery.NewOperationRecord(in), d.key); id != "" {
			o.Status.Record = &delivery.RecordRef{Digest: id, Seq: in.Chain.Seq, Kind: delivery.RecordOperation}
		}
	}
	d.replanHeld()
}

// replanHeld plans every held change again and, when what must move first
// changed, asks the new question: once a consumer moved first, only the teams
// still affected are asked.
func (d *demoDelivery) replanHeld() {
	for _, o := range d.ops {
		prev := o.Status.Plan
		if o.Status.Phase != delivery.PhaseHeld || o.Status.Applied || prev == nil || prev.Order == nil {
			continue
		}
		cur, _, err := d.plan(delivery.Request{Namespace: o.Metadata.Namespace, Stage: o.Spec.Stage, Type: o.Spec.Type,
			To: o.Spec.Params.To, From: o.Spec.Params.From}, personaOf(o.Spec.Actor.User))
		if err != nil || cur.Predicted == delivery.PredictRejected {
			continue
		}
		if !cur.Floor.Human {
			next := *prev
			next.Order, next.Question = nil, "Nothing Pacto sees holds this change any more: what it waited for has moved. Approve it to ship it, or cancel it and propose it again."
			cur = next
		}
		o.Status.Plan, o.Status.Question = &cur, cur.Question
	}
}

func personaOf(user string) story.Persona {
	for _, p := range []story.Persona{story.PayAgent, story.Alice, story.Carol, story.Promoter} {
		if p.User == user {
			return p
		}
	}
	return story.Persona{User: user}
}

// ── dashboard.DeliveryAPI ───────────────────────────────────────────────

func clone[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

// view is a copy of s with the candidate the stage reconciler would see:
// the newest version its upstream Stage offers, or the newest pacto publish
// published for a test Stage.
func (d *demoDelivery) view(s *delivery.StageObject) delivery.StageObject {
	c := clone(*s)
	var up *delivery.StageStatus
	if u := s.Spec.Candidates.UpstreamStage; u != nil {
		up = &d.stage(u.Namespace, u.Name).Status
	}
	rev := ""
	if p := s.Status.Published; len(p) > 0 {
		rev = "latest@" + p[len(p)-1]
	}
	c.Status.Candidate = delivery.NewestCandidate(c.Status, rev, up)
	return c
}

func (d *demoDelivery) WhoAmI(context.Context) (delivery.Caller, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.caller(), nil
}

// inNamespace is a fresh slice of the items in ns ("" for all), so a caller
// may sort or filter it without touching the views.
func inNamespace[T any](all []T, ns string, nsOf func(*T) string) []T {
	out := []T{}
	for i := range all {
		if ns == "" || nsOf(&all[i]) == ns {
			out = append(out, all[i])
		}
	}
	return out
}

func (d *demoDelivery) Stages(_ context.Context, ns string) ([]delivery.StageObject, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return inNamespace(d.snapshot().stages, ns, func(s *delivery.StageObject) string { return s.Metadata.Namespace }), nil
}

func (d *demoDelivery) Operations(_ context.Context, ns string) ([]delivery.OperationObject, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return inNamespace(d.snapshot().ops, ns, func(o *delivery.OperationObject) string { return o.Metadata.Namespace }), nil
}

func notFound(what string) error {
	return dashboard.Refuse(http.StatusNotFound, "NotFound", what+" is not here")
}

func (d *demoDelivery) Get(_ context.Context, ns, name string) (*delivery.OperationObject, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if o := d.op(ns, name); o != nil {
		c := clone(*o)
		return &c, nil
	}
	return nil, notFound("operation " + stageKey(ns, name))
}

func (d *demoDelivery) Stage(_ context.Context, ns, name string) (*delivery.StageObject, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.stage(ns, name); s != nil {
		c := d.view(s)
		return &c, nil
	}
	return nil, notFound("Stage " + stageKey(ns, name))
}

func (d *demoDelivery) Explain(ctx context.Context, ns, name string) (*delivery.Explanation, error) {
	op, err := d.Get(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	st, _ := d.Stage(ctx, ns, op.Spec.Stage)
	return delivery.ExplainOperation(op, st), nil
}

// history assembles a Stage's history with the records the tab signed.
func (d *demoDelivery) history(ns, name string) (*delivery.History, []delivery.StoredRecord, []delivery.OperationObject, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.stage(ns, name)
	if st == nil {
		return nil, nil, nil, notFound("Stage " + stageKey(ns, name))
	}
	ops := inNamespace(d.snapshot().ops, ns, func(o *delivery.OperationObject) string { return o.Metadata.Namespace })
	var r *delivery.RecordsRead
	if s := d.streams[stageKey(ns, name)]; s != nil {
		r = &delivery.RecordsRead{Fold: delivery.FoldStream(s.recs), Unreadable: slices.Clone(s.unreadable),
			Trust: "the key this tab minted at load (" + d.keyHint + ")"}
	}
	stc := clone(*st)
	h, ops := delivery.AssembleHistory(&stc, ops, r, nil, stamp(d.now()))
	if r == nil {
		return h, nil, ops, nil
	}
	return h, r.Fold.Records, ops, nil
}

func (d *demoDelivery) History(_ context.Context, ns, name string, _ *delivery.StageObject) (*delivery.History, error) {
	h, _, _, err := d.history(ns, name)
	return h, err
}

func (d *demoDelivery) Blame(_ context.Context, ns, name, digest string) (*delivery.Blame, error) {
	h, recs, ops, err := d.history(ns, name)
	if err != nil {
		return nil, err
	}
	return delivery.BlameOf(h, recs, ops, digest), nil
}

func (d *demoDelivery) Undo(ctx context.Context, ns, name, _ string) (*delivery.Undo, error) {
	op, err := d.Get(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	st, _ := d.Stage(ctx, ns, op.Spec.Stage)
	u, req := delivery.UndoOf(op, false, st)
	if req == nil {
		return u, nil
	}
	p, err := d.Plan(ctx, *req)
	if err != nil {
		return nil, err
	}
	u.Planned(*req, p)
	return u, nil
}

func (d *demoDelivery) Plan(_ context.Context, req delivery.Request) (delivery.Plan, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, _, err := d.plan(req, personas[d.persona])
	return p, err
}

func (d *demoDelivery) Propose(_ context.Context, req delivery.Request) (*delivery.OperationObject, delivery.Plan, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	o, p, err := d.propose(req, personas[d.persona])
	d.changed()
	if err != nil {
		return nil, p, err
	}
	c := clone(*o)
	return &c, p, nil
}

// Approve is the promoter consuming a person's decision; the dashboard
// already refused agents, stale sign-ins and other teams.
func (d *demoDelivery) Approve(_ context.Context, ns, name string, decision delivery.Decision, note string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer d.changed()
	o := d.op(ns, name)
	if o == nil || o.Status.Phase != delivery.PhaseHeld {
		return "", dashboard.Refuse(http.StatusConflict, "NotHeld", name+" is not waiting for a decision")
	}
	return d.decide(o, d.caller(), decision, note), nil
}

// decide records c's decision on the held o and acts on it: what the
// promoter does when it consumes an Approval.
func (d *demoDelivery) decide(o *delivery.OperationObject, c delivery.Caller, decision delivery.Decision, note string) string {
	name := o.Metadata.Name
	d.n++
	approval := fmt.Sprintf("%s-approval-%d", name, d.n)
	d.approvals[approval] = delivery.ApprovalSpec{Operation: name, OperationUID: o.Metadata.UID, IntentDigest: o.Spec.IntentDigest,
		Decision: decision, Note: note, By: c.User, AuthTime: c.AuthTime, Groups: c.Groups}
	o.Status.Approvals = append(o.Status.Approvals, delivery.ApprovalRecord{Name: approval, By: c.User, Decision: decision, Note: note, AuthTime: c.AuthTime})
	var owners []string
	if o.Status.Plan != nil {
		owners = o.Status.Plan.Floor.Owners
	}
	o.Status.ApprovedTeams = delivery.AnsweredTeams(c.Policy, owners, c.Groups, o.Status.ApprovedTeams)
	waiting := delivery.TeamsWaiting(owners, o.Status.ApprovedTeams)
	switch {
	case decision != delivery.Reject && len(waiting) > 0: // every team asked answers
		o.Status.Message = c.User + " answered; the change still waits for " + strings.Join(waiting, ", ")
	case decision == delivery.Reject && o.Status.Applied: // a rejected unknown: it fails and rolls back
		o.Status.Reason = "RejectedByHuman: " + c.User + ": " + note
		d.conclude(o, delivery.Failed, "")
		d.autoRollback(o, nil, "a person rejected the unknown")
	case decision == delivery.Reject:
		o.Status.Reason = "Rejected: " + c.User + " rejected it"
		d.conclude(o, delivery.Rejected, "")
	case o.Status.Applied: // an accepted unknown after release
		o.Status.Reason = "AcceptedUnknown: " + c.User + " accepted the unknown"
		d.conclude(o, delivery.Succeeded, delivery.AcceptedUnknown)
	default:
		o.Status.Question = ""
		d.start(o)
	}
	return approval
}

func (d *demoDelivery) Cancel(_ context.Context, ns, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := slices.IndexFunc(d.ops, func(o *delivery.OperationObject) bool { return o.Metadata.Namespace == ns && o.Metadata.Name == name })
	switch {
	case i < 0:
		return notFound("operation " + stageKey(ns, name))
	case d.ops[i].Status.Applied || d.ops[i].Status.Phase == delivery.PhaseConcluded:
		return dashboard.Refuse(http.StatusConflict, "TooLate", name+" already moved the pointer or concluded")
	}
	d.ops = slices.Delete(d.ops, i, i+1)
	d.changed()
	return nil
}

func (d *demoDelivery) Describe(_ context.Context, st *delivery.StageObject, digest string) delivery.Revision {
	r, err := story.Revision(st.Spec.Service.Name, versionOf(st.Spec.Service.Name, digest))
	if err != nil || r.Digest == "" {
		return delivery.Revision{Digest: digest, Err: "not a version in this demo"}
	}
	return r
}

// PublishRefusals is the one refused publish of the story (B4): payments
// 2.4.3 removes a field, so the version rule asks for a major. The
// classification is pkg/diff over the two story contracts; the refusal
// itself is fixed data, and nothing here signed or checked it.
func (d *demoDelivery) PublishRefusals(context.Context, string) (*dashboard.PublishRefusals, error) {
	base, _ := story.Bundle("payments", "2.4.2")
	tried, _ := story.Bundle("payments", "2.4.3")
	c := diff.Compare(context.Background(), base.Contract, tried.Contract, base.FS, tried.FS).Classification
	bump := diff.RequiredBump(c, "2.4.2")
	return &dashboard.PublishRefusals{Completeness: "complete",
		Note: "Simulated: one fixed refusal from the payments story. The bump is computed by Pacto's diff; nothing here signed or checked the refusal.",
		Refusals: []dashboard.PublishRefusal{{Service: "payments", Version: "2.4.3", Check: "version", Baseline: "2.4.2",
			RequiredBump: string(bump), Actor: story.PayAgent.User, At: stamp(d.now().Add(-3 * time.Hour)), Verified: false,
			Reason: fmt.Sprintf("the change is %s, so it needs a %s bump from 2.4.2", c, bump)}}}, nil
}

// wire serves d from opts: the delivery views and the simulated signed-in
// session. Callers carry the story's policy (team-orders is team orders;
// service accounts are agents).
func (d *demoDelivery) wire(opts *dashboard.Options) {
	opts.Delivery = func(*http.Request) (dashboard.DeliveryAPI, error) { return d, nil }
	opts.Auth = &dashboard.Auth{Login: true, State: d.State}
}

// pactifyPlan is what `pacto init --plan --owner risk` plans for the story's
// plain fraud service (examples/story/plain/fraud), as JSON. It is recorded:
// the planner reads the disk and is not in the wasm build, so
// TestPactifyPlanIsCurrent runs it and fails when this copy drifts
// (`make -C examples/demo pactify-plan` rewrites it).
//
//go:embed pactify-fraud.json
var pactifyPlan string

// Can: the demo has no RBAC, so every persona may ask for anything; the
// promoter's own rules still shape what is offered.
func (d *demoDelivery) Can(context.Context, string, string, string) (bool, error) { return true, nil }

// StageNamespaces: everyone reads every Stage cluster-wide here, so the
// dashboard never asks.
func (d *demoDelivery) StageNamespaces(context.Context) ([]string, error) { return nil, nil }
