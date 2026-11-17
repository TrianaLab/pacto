package stats

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// LimitationLeadTimeUnjoined says some promotions have no lead time.
const LimitationLeadTimeUnjoined = "LEAD_TIME_UNJOINED"

// The operation types the rates name, as an operation record writes them (the
// last path segment). Any other type counts in operations and in no rate.
var (
	opPromote  = path.Base(delivery.TypePromote)
	opRollback = path.Base(delivery.TypeRollback)
)

// EnvironmentMetrics are the delivery numbers of one environment.
type EnvironmentMetrics struct {
	Environment        string   `json:"environment" doc:"Environment name"`
	Source             string   `json:"source" enum:"stage,context" doc:"Where the name came from: the record's environment (context) or, for an operation without one, its Stage name (stage)"`
	Operations         int      `json:"operations" doc:"Operations of every type in the window"`
	Held               Ratio    `json:"held" doc:"Promote operations that waited on a person, of promote operations"`
	Failed             Ratio    `json:"failed" doc:"Promote operations that ended Failed or Errored, of promote operations"`
	RolledBack         Ratio    `json:"rolledBack" doc:"Succeeded rollback operations, of Succeeded promote operations"`
	VerificationFailed Ratio    `json:"verificationFailed" doc:"Promote operations whose verification is Unverified, of promote operations verified or unverified"`
	AcceptedUnknown    int      `json:"acceptedUnknown" doc:"Promote operations accepted with verification unknown; neither verified nor failed"`
	LeadTime           LeadTime `json:"leadTime" doc:"Time from the publish record to the first verified promotion of its digest per Stage"`
}

// LeadTime is the lead-time percentiles of the promotions that joined a
// publish record. With none joined it is unknown, never 0.
type LeadTime struct {
	P50Seconds *int64 `json:"p50Seconds,omitempty" doc:"Median lead time in seconds, nearest rank; absent when none joined"`
	P90Seconds *int64 `json:"p90Seconds,omitempty" doc:"90th percentile lead time in seconds, nearest rank; absent when none joined"`
	Joined     int    `json:"joined" doc:"Promotions joined to a publish record"`
	Unjoined   int    `json:"unjoined" doc:"Promotions with no publish record of their digest; their lead time is unknown"`
	Unknown    bool   `json:"unknown,omitempty" doc:"True when no promotion joined"`
}

// environmentOf is the environment a record counts under: its environment,
// else an operation's Stage name.
func environmentOf(r runrecord.Record) (env, source string, ok bool) {
	switch {
	case r.Environment != "":
		return r.Environment, "context", true
	case r.Kind == runrecord.KindOperation && r.Stage != nil:
		return r.Stage.Name, "stage", true
	}
	return "", "", false
}

// finishedAt parses an operation record's finishedAt.
func finishedAt(r runrecord.Record) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, r.FinishedAt)
	return t, err == nil
}

// heldFor parses an operation record's heldFor.
func heldFor(r runrecord.Record) (time.Duration, bool) {
	d, err := time.ParseDuration(r.HeldFor)
	return d, err == nil
}

// envCounts are the raw counts behind one environment's rates.
type envCounts struct {
	m                                                 EnvironmentMetrics
	promotes, succeeded, held, failed, rolled, vf, vs int
}

func (c *envCounts) add(r runrecord.Record) {
	c.m.Operations++
	if isRolledBack(r) {
		c.rolled++
	}
	if r.Type != opPromote {
		return
	}
	c.promotes++
	if isHeld(r) {
		c.held++
	}
	if r.Outcome == string(delivery.Succeeded) {
		c.succeeded++
	}
	if isFailed(r) {
		c.failed++
	}
	if isVerificationFailed(r) {
		c.vf++
	}
	switch delivery.Verification(r.Verification) {
	case delivery.Unverified, delivery.Verified:
		c.vs++
	case delivery.AcceptedUnknown:
		c.m.AcceptedUnknown++
	}
}

// The numerators of the rates, one definition each, shared by the counts and
// by Behind.

// isHeld is a promote that waited on a person.
func isHeld(r runrecord.Record) bool {
	d, ok := heldFor(r)
	return r.Type == opPromote && ok && d > 0
}

// isFailed is a promote that ended Failed or Errored.
func isFailed(r runrecord.Record) bool {
	o := delivery.Outcome(r.Outcome)
	return r.Type == opPromote && (o == delivery.Failed || o == delivery.Errored)
}

// isRolledBack is a Succeeded rollback.
func isRolledBack(r runrecord.Record) bool {
	return r.Type == opRollback && r.Outcome == string(delivery.Succeeded)
}

// isVerificationFailed is a promote whose verification is Unverified.
func isVerificationFailed(r runrecord.Record) bool {
	return r.Type == opPromote && r.Verification == string(delivery.Unverified)
}

func (c *envCounts) metrics(lt LeadTime) EnvironmentMetrics {
	m := c.m
	m.Held, m.Failed = ratio(c.held, c.promotes), ratio(c.failed, c.promotes)
	m.RolledBack, m.VerificationFailed = ratio(c.rolled, c.succeeded), ratio(c.vf, c.vs)
	m.LeadTime = lt
	return m
}

// environmentMetrics counts the operation entries per environment, joining
// lead times against history, alphabetically.
func environmentMetrics(entries, history []Entry) []EnvironmentMetrics {
	byEnv := map[string]*envCounts{}
	var ops []Entry
	for _, e := range entries {
		env, src, ok := environmentOf(e.Record)
		if !ok || e.Record.Kind != runrecord.KindOperation {
			continue
		}
		ops = append(ops, e)
		c := byEnv[env]
		if c == nil {
			c = &envCounts{m: EnvironmentMetrics{Environment: env, Source: src}}
			byEnv[env] = c
		}
		if src == "context" {
			c.m.Source = src
		}
		c.add(e.Record)
	}
	lts := leadTimes(ops, history)
	var out []EnvironmentMetrics
	for _, env := range slices.SortedFunc(maps.Keys(byEnv), fleet.NaturalCompare) {
		lt, ok := lts[env]
		if !ok {
			lt = LeadTime{Unknown: true}
		}
		out = append(out, byEnv[env].metrics(lt))
	}
	return out
}

// leadSample is one environment's joined leads in seconds and its count of
// unjoined promotions.
type leadSample struct {
	leads    []int64
	unjoined int
}

// leadDurations joins, per environment, the first Succeeded and Verified
// promote of each digest per Stage to the earliest publish record of the same
// service with that deploy digest, else with the operation's contract as its
// bundle digest. history is every visible record, in the window or before it:
// a promote among ops counts only when no earlier one in history promoted its
// digest to its Stage, and a publish rerun never moves the start. A promotion
// that joins nothing, or would lead by a negative or unparsable time, is
// unjoined. This is the only D7-specific function.
func leadDurations(ops, history []Entry) map[string]*leadSample {
	byEnv := map[string]*leadSample{}
	for _, j := range joinPromotes(ops, history) {
		env, _, ok := environmentOf(j.op.Record)
		if !ok {
			continue
		}
		a := byEnv[env]
		if a == nil {
			a = &leadSample{}
			byEnv[env] = a
		}
		if j.pub != nil {
			a.leads = append(a.leads, j.lead)
		} else {
			a.unjoined++
		}
	}
	return byEnv
}

// leadTimeOf is the nearest-rank p50 and p90 of a sample; unknown when
// nothing joined.
func leadTimeOf(a leadSample) LeadTime {
	slices.Sort(a.leads)
	return LeadTime{P50Seconds: percentile(a.leads, 50), P90Seconds: percentile(a.leads, 90), Joined: len(a.leads), Unjoined: a.unjoined, Unknown: len(a.leads) == 0}
}

// leadTimes is the lead time per environment.
func leadTimes(ops, history []Entry) map[string]LeadTime {
	out := map[string]LeadTime{}
	for env, a := range leadDurations(ops, history) {
		out[env] = leadTimeOf(*a)
	}
	return out
}

// LeadTimesIn is the lead time of each named group of environments: one
// percentile over the pooled promotions of its environments, never a
// percentile of percentiles. It selects and joins once, with the visibility,
// window and history Aggregate uses, however many groups there are. A group
// whose environments joined nothing is unknown. It does no I/O.
func LeadTimesIn(set RecordSet, q Query, sc Scope, now time.Time, groups map[string][]string) map[string]LeadTime {
	sel := selectRecords(set, q, sc, now)
	byEnv := leadDurations(sel.inWindow, sel.history)
	out := make(map[string]LeadTime, len(groups))
	for key, names := range groups {
		var pooled leadSample
		for _, env := range names {
			if a := byEnv[env]; a != nil {
				pooled.leads = append(pooled.leads, a.leads...)
				pooled.unjoined += a.unjoined
			}
		}
		out[key] = leadTimeOf(pooled)
	}
	return out
}

// promoteJoin is a first promotion and the publish record it joined; pub is
// nil when it joined none.
type promoteJoin struct {
	op   Entry
	pub  *Entry
	lead int64
}

// joinPromotes is the first promotions among ops, each joined to its publish
// in history: the one join leadTimes and Behind share.
func joinPromotes(ops, history []Entry) []promoteJoin {
	pub, first := publishIndex(history), firstPromotes(history)
	var out []promoteJoin
	for _, e := range ops {
		key, ok := promoteKey(e.Record)
		if !ok || key != "" && first[key] != e.Record.ID {
			continue
		}
		j := promoteJoin{op: e}
		j.pub, j.lead = promotionLead(e.Record, pub)
		out = append(out, j)
	}
	return out
}

// publishIndex is the earliest publish record per service and digest: the
// publish that produced the digest, not a rerun that found it already pushed.
func publishIndex(history []Entry) map[string]*Entry {
	pub := map[string]*Entry{}
	for i := range history {
		r := history[i].Record
		if r.Kind != runrecord.KindPublish {
			continue
		}
		for kind, d := range map[string]string{"deploy": r.DeployDigest, "bundle": r.BundleDigest} {
			k := r.Service + "|" + kind + "|" + d
			if p, ok := pub[k]; d != "" && (!ok || r.RecordedAt.Before(p.Record.RecordedAt)) {
				pub[k] = &history[i]
			}
		}
	}
	return pub
}

// promoteKey is the Stage and digest of a Succeeded and Verified promote; ok
// is false for any other record, and the key is empty for a promote naming
// no digest, which stands alone.
func promoteKey(r runrecord.Record) (string, bool) {
	if r.Kind != runrecord.KindOperation || r.Type != opPromote || r.Outcome != string(delivery.Succeeded) || r.Verification != string(delivery.Verified) {
		return "", false
	}
	digest := r.To + "|" + r.Contract
	if digest == "|" {
		return "", true
	}
	stage := "//"
	if st := r.Stage; st != nil {
		stage = st.Namespace + "/" + st.Name + "/" + st.Cluster
	}
	return stage + "|" + digest, true
}

// firstPromotes is the record id of the earliest Succeeded and Verified
// promote per Stage and digest.
func firstPromotes(history []Entry) map[string]string {
	first, at := map[string]string{}, map[string]time.Time{}
	for _, e := range history {
		key, ok := promoteKey(e.Record)
		if !ok || key == "" {
			continue
		}
		if t, seen := at[key]; !seen || recordTime(e.Record).Before(t) {
			first[key], at[key] = e.Record.ID, recordTime(e.Record)
		}
	}
	return first
}

// promotionLead is the publish r joins and its lead in seconds; nil when it
// joins none or would lead by a negative or unparsable time.
func promotionLead(r runrecord.Record, pub map[string]*Entry) (*Entry, int64) {
	p, ok := pub[r.Service+"|deploy|"+r.To]
	if !ok {
		p, ok = pub[r.Service+"|bundle|"+r.Contract]
	}
	done, parsed := finishedAt(r)
	if !ok || !parsed || done.Before(p.Record.RecordedAt) {
		return nil, 0
	}
	return p, int64(done.Sub(p.Record.RecordedAt) / time.Second)
}

// percentile is the nearest-rank p-th percentile of sorted, nil when empty.
func percentile(sorted []int64, p int) *int64 {
	if len(sorted) == 0 {
		return nil
	}
	rank := (p*len(sorted) + 99) / 100
	v := sorted[max(rank, 1)-1]
	return &v
}

// unjoinedLimitation reports promotions with no lead time. It is
// informational: an unjoined lead time is already unknown.
func unjoinedLimitation(envs []EnvironmentMetrics) []fleet.Limitation {
	n := 0
	for _, e := range envs {
		n += e.LeadTime.Unjoined
	}
	if n == 0 {
		return nil
	}
	return []fleet.Limitation{{Code: LimitationLeadTimeUnjoined, Message: fmt.Sprintf("%d promotions have no publish record with their digest, so their lead time is unknown", n)}}
}
