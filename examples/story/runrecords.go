package story

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// The demo run records: about 90 days of the unsigned statistics Pacto writes,
// placed at fixed offsets before a caller's now so a window that ends at now
// always holds the same records. The newest 30 days are the payments story;
// days 31 to 90 repeat a small weekly pattern over synthetic revisions no
// fleet holds.

// StatsStage is a Stage the demo records operations on. Upstream is
// "stage:<namespace>/<name>", "artifact" or "records".
type StatsStage struct {
	Namespace, Name, Environment string
	Upstream                     string
}

// StatsRevision is one version of a service: its contract digest, the
// revision the fleet keys it with, and its deploy artifact digest.
type StatsRevision struct {
	Version, Digest, Artifact string
}

// StatsService is a service the demo records runs for: its revisions oldest
// to newest and its Stages.
type StatsService struct {
	Name      string
	Revisions []StatsRevision
	Stages    []StatsStage
}

// StatsEvent is one run, placed At before now. From and To index the
// service's Revisions (-1 is none), or synthetic history revisions when
// History is set. Kind is a run record kind; an operation event names its
// Stage by namespace (the Stage is named after the service), its Type, its
// Outcome and its Actor.
type StatsEvent struct {
	At                   time.Duration
	Kind, Service, Stage string
	From, To             int
	History              bool
	Classification       string
	Verdict, Reason      string
	Blocking, Compatible int
	Type, Outcome        string
	Verification         string
	Held                 time.Duration
	Actor                Persona
}

// StatsServices is the demo's topology: the story services on test then prod,
// checkout's prod-eu and prod-us fed by test, audit-log's prod Stage fed by
// new artifacts and notification-worker's prod Stage fed by another cluster's
// records.
func StatsServices() []StatsService {
	var out []StatsService
	for _, name := range []string{"payments", "checkout", "orders", "fraud", "pay-agent"} {
		s := StatsService{Name: name, Stages: []StatsStage{
			{Namespace: Test, Name: name, Environment: Test, Upstream: "artifact"},
			{Namespace: Prod, Name: name, Environment: Prod, Upstream: "stage:" + Test + "/" + name},
		}}
		versions, err := fs.ReadDir(Files, "contracts/"+name)
		if err != nil {
			panic(err) // ponytail: embedded at build time; TestEveryContractValidates reads the same files
		}
		for _, v := range versions {
			rev, err := Revision(name, v.Name())
			if err != nil {
				panic(err)
			}
			s.Revisions = append(s.Revisions, StatsRevision{Version: v.Name(), Digest: delivery.ContractDigest(rev.ContractRef), Artifact: rev.Digest})
		}
		if name == "checkout" {
			for _, region := range []string{"prod-eu", "prod-us"} {
				s.Stages = append(s.Stages, StatsStage{Namespace: region, Name: name, Environment: region, Upstream: "stage:" + Test + "/" + name})
			}
		}
		out = append(out, s)
	}
	return append(out,
		StatsService{Name: "audit-log", Stages: []StatsStage{{Namespace: Prod, Name: "audit-log", Environment: Prod, Upstream: "artifact"}}},
		StatsService{Name: "notification-worker", Stages: []StatsStage{{Namespace: Prod, Name: "notification-worker", Environment: Prod, Upstream: "records"}}})
}

// historyDigest is the contract digest of synthetic history revision n.
func historyDigest(service string, n int) string {
	return sha("history " + service + " " + strconv.Itoa(n))
}

func historyRevision(service string, n int) StatsRevision {
	return StatsRevision{Version: fmt.Sprintf("0.%d.0", n), Digest: historyDigest(service, n),
		Artifact: sha("history manifests " + service + " " + strconv.Itoa(n))}
}

// operationUID is a Kubernetes-shaped UID for an operation.
func operationUID(service, stage string, n int) string {
	sum := sha256.Sum256([]byte("op " + service + " " + stage + " " + strconv.Itoa(n)))
	h := hex.EncodeToString(sum[:16])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func (s StatsService) revision(i int, history bool) (StatsRevision, error) {
	switch {
	case i == -1:
		return StatsRevision{}, nil
	case i < -1 || (!history && i >= len(s.Revisions)):
		return StatsRevision{}, fmt.Errorf("%s has no revision %d", s.Name, i)
	case history:
		return historyRevision(s.Name, i), nil
	}
	return s.Revisions[i], nil
}

func (s StatsService) stage(namespace string) (StatsStage, bool) {
	i := slices.IndexFunc(s.Stages, func(st StatsStage) bool { return st.Namespace == namespace && st.Name == s.Name })
	if i < 0 {
		return StatsStage{}, false
	}
	return s.Stages[i], true
}

// Records builds the run record of every event at its offset before now,
// sorted by recordedAt then id. An event naming a service, revision or Stage
// services lacks is an error.
func Records(now time.Time, services []StatsService, events []StatsEvent) ([]runrecord.Record, error) {
	out := make([]runrecord.Record, 0, len(events))
	for _, ev := range events {
		r, err := record(now, services, ev)
		if err != nil {
			return nil, fmt.Errorf("%s %s %v before now: %w", ev.Kind, ev.Service, ev.At, err)
		}
		if _, err := runrecord.Encode(r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b runrecord.Record) int {
		return cmp.Or(a.RecordedAt.Compare(b.RecordedAt), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

func record(now time.Time, services []StatsService, ev StatsEvent) (runrecord.Record, error) {
	i := slices.IndexFunc(services, func(s StatsService) bool { return s.Name == ev.Service })
	if i < 0 {
		return runrecord.Record{}, fmt.Errorf("service %s is not in the topology", ev.Service)
	}
	s := services[i]
	from, err := s.revision(ev.From, ev.History)
	if err != nil {
		return runrecord.Record{}, err
	}
	to, err := s.revision(ev.To, ev.History)
	if err != nil {
		return runrecord.Record{}, err
	}
	switch ev.Kind {
	case runrecord.KindDiff, runrecord.KindImpact, runrecord.KindPublish:
		return ciRecord(now, s, ev, from, to), nil
	case runrecord.KindOperation:
		return operationRecord(now, s, ev, from, to)
	}
	return runrecord.Record{}, fmt.Errorf("unknown kind %q", ev.Kind)
}

var impactOutcomes = map[string]string{
	runrecord.VerdictClear: runrecord.OutcomePassed, runrecord.VerdictBlocked: runrecord.OutcomeFailed,
	runrecord.VerdictUnverified: runrecord.OutcomeUnverified,
}

// ciRecord is a diff, impact or publish record as the CLI writes it with
// --stats-context repository: diff and impact name bundle digests, publish
// names versions.
func ciRecord(now time.Time, s StatsService, ev StatsEvent, from, to StatsRevision) runrecord.Record {
	at := now.Add(-ev.At).UTC().Truncate(time.Second)
	r := runrecord.Record{Version: runrecord.Version, Kind: ev.Kind, Service: s.Name, OldRevision: from.Digest, NewRevision: to.Digest,
		Classification: ev.Classification, Outcome: runrecord.OutcomePassed, PactoVersion: "dev", RecordedAt: at, Repository: "story/" + s.Name}
	switch ev.Kind {
	case runrecord.KindDiff:
		if ev.Classification == "BREAKING" {
			r.Outcome = runrecord.OutcomeFailed
		}
	case runrecord.KindImpact:
		asOf := at.Add(-time.Minute)
		r.Gate, r.Outcome = &runrecord.Gate{Verdict: ev.Verdict, Reason: ev.Reason}, impactOutcomes[ev.Verdict]
		r.Consumers = &runrecord.Consumers{Incompatible: ev.Blocking, Compatible: ev.Compatible, Blocking: ev.Blocking}
		r.Completeness, r.AsOf = "complete", &asOf
	case runrecord.KindPublish:
		r.OldRevision, r.NewRevision = from.Version, to.Version
		if ev.Verdict == runrecord.VerdictRefused {
			r.Gate, r.Outcome = &runrecord.Gate{Verdict: ev.Verdict, Reason: ev.Reason}, runrecord.OutcomeRefused
		} else {
			r.Outcome, r.BundleDigest, r.DeployDigest = runrecord.OutcomePublished, to.Digest, to.Artifact
		}
	}
	r.ID = runrecord.ID(r.Kind, r.Service, r.OldRevision, r.NewRevision)
	return r
}

var verifyResults = map[string]delivery.Result{
	string(delivery.Verified): delivery.Pass, string(delivery.Unverified): delivery.Fail, string(delivery.AcceptedUnknown): delivery.Unknown,
}

// operationRecord builds the record of a concluded operation with the
// promoter's own builder. It took six minutes plus the time it was held.
func operationRecord(now time.Time, s StatsService, ev StatsEvent, from, to StatsRevision) (runrecord.Record, error) {
	st, ok := s.stage(ev.Stage)
	if !ok {
		return runrecord.Record{}, fmt.Errorf("%s has no Stage in %s", s.Name, ev.Stage)
	}
	typ, ok := delivery.TypeByID(ev.Type)
	if !ok {
		return runrecord.Record{}, fmt.Errorf("unknown operation type %q", ev.Type)
	}
	finished := now.Add(-ev.At).UTC()
	admitted := finished.Add(-6*time.Minute - ev.Held)
	times := delivery.Times{Admitted: admitted.Format(time.RFC3339), Finished: finished.Format(time.RFC3339)}
	if ev.Held > 0 {
		times.Held, times.Applied = admitted.Add(time.Minute).Format(time.RFC3339), admitted.Add(time.Minute+ev.Held).Format(time.RFC3339)
	}
	var verdicts []delivery.Verdict
	if to.Digest != "" {
		verdicts = append(verdicts, delivery.Verdict{Gate: delivery.GateImpact, Phase: "pre", Result: delivery.Pass,
			Citation: map[string]string{"contract": "oci://" + Domain + "/" + s.Name + "@" + to.Digest}})
		if res, ok := verifyResults[ev.Verification]; ok {
			verdicts = append(verdicts, delivery.Verdict{Gate: delivery.GateContract, Phase: "verify", Result: res})
		}
	}
	r, ok := runrecord.FromOperation(runrecord.OperationInput{
		UID: operationUID(s.Name, st.Namespace+"/"+st.Name, int(ev.At/time.Minute)), Namespace: st.Namespace,
		Spec: delivery.OperationSpec{Type: ev.Type, Stage: st.Name, Facet: typ.Facet, Params: delivery.Params{From: from.Artifact, To: to.Artifact}},
		Status: delivery.OperationStatus{Outcome: delivery.Outcome(ev.Outcome), Verification: delivery.Verification(ev.Verification),
			Reason: ev.Reason, ActorClass: ev.Actor.Class(), Verdicts: verdicts, Times: times,
			Policy: &delivery.StageSpec{Service: delivery.ServiceRef{Name: s.Name}, Environment: st.Environment}},
		ClusterID: "demo", PactoVersion: "dev", RecordedAt: finished,
	})
	if !ok {
		return runrecord.Record{}, fmt.Errorf("the operation on %s/%s builds no record", st.Namespace, st.Name)
	}
	return r, nil
}

// RunRecords is the browser demo's records: the story, then the history.
func RunRecords(now time.Time, services []StatsService) ([]runrecord.Record, error) {
	return Records(now, services, append(storyEvents(), historyEvents()...))
}

// HistoryRecords is only the history older than the story, over synthetic
// revisions, for a demo that records the story's runs for real.
func HistoryRecords(now time.Time, services []StatsService) ([]runrecord.Record, error) {
	return Records(now, services, historyEvents())
}

const day = 24 * time.Hour

func ago(days, hours int) time.Duration {
	return time.Duration(days)*day + time.Duration(hours)*time.Hour
}

func diffRun(at time.Duration, service string, from, to int, classification string) StatsEvent {
	return StatsEvent{At: at, Kind: runrecord.KindDiff, Service: service, From: from, To: to, Classification: classification}
}

func impactRun(at time.Duration, service string, from, to int, classification, verdict, reason string, blocking, compatible int) StatsEvent {
	return StatsEvent{At: at, Kind: runrecord.KindImpact, Service: service, From: from, To: to, Classification: classification,
		Verdict: verdict, Reason: reason, Blocking: blocking, Compatible: compatible}
}

func clearImpact(at time.Duration, service string, from, to, compatible int) StatsEvent {
	return impactRun(at, service, from, to, "NON_BREAKING", runrecord.VerdictClear, "NOT_BREAKING", 0, compatible)
}

func blockedImpact(at time.Duration, service string, from, to, blocking, compatible int) StatsEvent {
	return impactRun(at, service, from, to, "BREAKING", runrecord.VerdictBlocked, "CONSUMERS_INCOMPATIBLE", blocking, compatible)
}

func publishRun(at time.Duration, service string, from, to int) StatsEvent {
	return StatsEvent{At: at, Kind: runrecord.KindPublish, Service: service, From: from, To: to, Classification: "NON_BREAKING"}
}

func operation(at time.Duration, typ, service, stage string, from, to int, outcome delivery.Outcome, v delivery.Verification, actor Persona) StatsEvent {
	return StatsEvent{At: at, Kind: runrecord.KindOperation, Type: typ, Service: service, Stage: stage, From: from, To: to,
		Outcome: string(outcome), Verification: string(v), Actor: actor}
}

func promote(at time.Duration, service, stage string, from, to int) StatsEvent {
	return operation(at, delivery.TypePromote, service, stage, from, to, delivery.Succeeded, delivery.Verified, Promoter)
}

func failedPromote(at time.Duration, service, stage string, from, to int, actor Persona) StatsEvent {
	e := operation(at, delivery.TypePromote, service, stage, from, to, delivery.Failed, delivery.Unverified, actor)
	e.Reason = "VerifyFailed"
	return e
}

func rollback(at time.Duration, service, stage string, from, to int) StatsEvent {
	return operation(at, delivery.TypeRollback, service, stage, from, to, delivery.Succeeded, delivery.Verified, Promoter)
}

func held(e StatsEvent, d time.Duration, by Persona) StatsEvent {
	e.Held, e.Actor = d, by
	return e
}

// storyEvents are the newest 30 days: the payments story. Revision indexes
// are the story versions in order (payments 2.3.0, 2.4.0, 2.4.1, 2.4.2, 2.4.3,
// 3.0.0). orders' promotes have no publish record, so their lead time is
// unknown; prod-us promotes once, so its other weeks have no rate.
func storyEvents() []StatsEvent {
	refused := StatsEvent{At: ago(15, 10), Kind: runrecord.KindPublish, Service: "payments", From: 3, To: 4,
		Classification: "BREAKING", Verdict: runrecord.VerdictRefused, Reason: "version"}
	rejected := operation(ago(21, 10), delivery.TypePromote, "pay-agent", Prod, -1, 0, delivery.Rejected, "", Promoter)
	rejected.Reason = "NotOffered"
	return []StatsEvent{
		// orders 1.4.0 runs in test and prod; it was published before records were kept.
		promote(ago(25, 10), "orders", Test, -1, 0),
		promote(ago(25, 8), "orders", Prod, -1, 0),
		// payments 2.4.0 is safe, ships to test, fails verification in prod and is rolled back.
		diffRun(ago(27, 6), "payments", 0, 1, "NON_BREAKING"),
		diffRun(ago(27, 5), "payments", 0, 1, "NON_BREAKING"),
		clearImpact(ago(27, 5), "payments", 0, 1, 3),
		publishRun(ago(27, 4), "payments", 0, 1),
		promote(ago(27, 2), "payments", Test, 0, 1),
		failedPromote(ago(26, 20), "payments", Prod, 0, 1, Promoter),
		rollback(ago(26, 19), "payments", Prod, 1, 0),
		// payments 2.4.1 fails in test and never reaches prod.
		diffRun(ago(24, 8), "payments", 1, 2, "NON_BREAKING"),
		publishRun(ago(24, 7), "payments", 1, 2),
		failedPromote(ago(24, 5), "payments", Test, 1, 2, Promoter),
		// pay-agent 1.0.0 ships to test once alice accepts the unknown its model leaves; prod is never offered it.
		publishRun(ago(22, 10), "pay-agent", -1, 0),
		held(operation(ago(22, 6), delivery.TypePromote, "pay-agent", Test, -1, 0, delivery.Succeeded, delivery.AcceptedUnknown, Promoter), 2*time.Hour, Alice),
		rejected,
		// payments 2.4.2 ships to test and prod.
		diffRun(ago(20, 9), "payments", 2, 3, "NON_BREAKING"),
		clearImpact(ago(20, 9), "payments", 2, 3, 3),
		publishRun(ago(20, 8), "payments", 2, 3),
		promote(ago(20, 6), "payments", Test, 1, 3),
		promote(ago(19, 22), "payments", Prod, 0, 3),
		// checkout 1.9.0 ships to test, waits three hours for dana in prod, then to both regions.
		diffRun(ago(18, 10), "checkout", 0, 1, "NON_BREAKING"),
		clearImpact(ago(18, 10), "checkout", 0, 1, 0),
		publishRun(ago(18, 9), "checkout", 0, 1),
		promote(ago(18, 7), "checkout", Test, 0, 1),
		held(promote(ago(17, 3), "checkout", Prod, 0, 1), 3*time.Hour, Dana),
		promote(ago(16, 9), "checkout", "prod-eu", -1, 1),
		promote(ago(16, 8), "checkout", "prod-us", -1, 1),
		// payments 2.4.3 is refused: it is not the version its change needs.
		refused,
		// payments 3.0.0 is BREAKING: checkout and orders block it, run after run.
		diffRun(ago(12, 9), "payments", 3, 5, "BREAKING"),
		diffRun(ago(12, 7), "payments", 3, 5, "BREAKING"),
		diffRun(ago(11, 10), "payments", 3, 5, "BREAKING"),
		blockedImpact(ago(12, 8), "payments", 3, 5, 2, 1),
		blockedImpact(ago(11, 10), "payments", 3, 5, 2, 1),
		diffRun(ago(10, 9), "payments", 0, 5, "BREAKING"),
		blockedImpact(ago(10, 9), "payments", 0, 5, 2, 1),
		// audit-log's Stage is frozen while its candidates verify no publisher.
		operation(ago(9, 10), delivery.TypeFreeze, "audit-log", Prod, -1, -1, delivery.Succeeded, "", Alice),
		// fraud publishes for the first time.
		publishRun(ago(8, 10), "fraud", -1, 0),
		promote(ago(8, 8), "fraud", Test, -1, 0),
		promote(ago(7, 9), "fraud", Prod, -1, 0),
		// pay-agent 1.1.0 swaps its model, fails its evals in test and is rolled back.
		diffRun(ago(6, 10), "pay-agent", 0, 1, "NON_BREAKING"),
		publishRun(ago(6, 9), "pay-agent", 0, 1),
		failedPromote(ago(6, 7), "pay-agent", Test, 0, 1, PayAgent),
		rollback(ago(6, 6), "pay-agent", Test, 1, 0),
		// notification-worker accepts a record from another cluster.
		operation(ago(5, 10), delivery.TypeAcknowledge, "notification-worker", Prod, -1, -1, delivery.Succeeded, "", Alice),
	}
}

// historyEvents are days 31 to 90: eight weeks of the same pattern. They use
// only Stages the Kubernetes demo also has (payments, checkout and pay-agent
// test and prod, orders prod), so HistoryRecords runs over its topology too.
func historyEvents() []StatsEvent {
	var out []StatsEvent
	for w := range 8 {
		for _, e := range historyWeek(w) {
			e.History = true
			out = append(out, e)
		}
	}
	return out
}

// historyWeek is week w of the history, w = 0 the newest: each service moves
// from synthetic revision w to w+1. Every third week payments' change is
// BREAKING and blocked, so nothing of it ships.
func historyWeek(w int) []StatsEvent {
	start := ago(31+7*w+5, 0)
	at := func(days, hours int) time.Duration { return start - ago(days, hours) }
	n := w + 1
	out := []StatsEvent{diffRun(at(0, 9), "payments", w, n, "NON_BREAKING"), diffRun(at(0, 11), "payments", w, n, "NON_BREAKING")}
	switch {
	case w%3 == 1:
		out[0].Classification, out[1].Classification = "BREAKING", "BREAKING"
		out = append(out, blockedImpact(at(0, 11), "payments", w, n, 1, 2))
	case w == 5:
		out = append(out, clearImpact(at(0, 11), "payments", w, n, 3), publishRun(at(0, 13), "payments", w, n), promote(at(0, 15), "payments", Test, w, n),
			failedPromote(at(1, 10), "payments", Prod, w, n, Promoter), rollback(at(1, 12), "payments", Prod, n, w))
	default:
		prod := promote(at(1, 10), "payments", Prod, w, n)
		if w%2 == 0 {
			prod = held(prod, 30*time.Minute, Alice)
		}
		out = append(out, clearImpact(at(0, 11), "payments", w, n, 3), publishRun(at(0, 13), "payments", w, n), promote(at(0, 15), "payments", Test, w, n), prod)
	}
	out = append(out, diffRun(at(2, 9), "checkout", w, n, "NON_BREAKING"), diffRun(at(2, 10), "checkout", w, n, "NON_BREAKING"), clearImpact(at(2, 10), "checkout", w, n, 0),
		publishRun(at(2, 11), "checkout", w, n), promote(at(2, 13), "checkout", Test, w, n), promote(at(3, 9), "checkout", Prod, w, n))
	if w%2 == 0 {
		out = append(out, diffRun(at(3, 10), "orders", w, n, "NON_BREAKING"), publishRun(at(3, 11), "orders", w, n), promote(at(3, 14), "orders", Prod, w, n))
	}
	if w%3 == 0 {
		out = append(out, diffRun(at(4, 9), "pay-agent", w, n, "NON_BREAKING"), publishRun(at(4, 10), "pay-agent", w, n),
			promote(at(4, 12), "pay-agent", Test, w, n), promote(at(4, 16), "pay-agent", Prod, w, n))
	}
	return out
}
