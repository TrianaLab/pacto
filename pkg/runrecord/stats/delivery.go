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
	switch r.Type {
	case opRollback:
		if r.Outcome == string(delivery.Succeeded) {
			c.rolled++
		}
	case opPromote:
		c.addPromote(r)
	}
}

func (c *envCounts) addPromote(r runrecord.Record) {
	c.promotes++
	if d, ok := heldFor(r); ok && d > 0 {
		c.held++
	}
	switch delivery.Outcome(r.Outcome) {
	case delivery.Succeeded:
		c.succeeded++
	case delivery.Failed, delivery.Errored:
		c.failed++
	}
	switch delivery.Verification(r.Verification) {
	case delivery.Unverified:
		c.vf++
		c.vs++
	case delivery.Verified:
		c.vs++
	case delivery.AcceptedUnknown:
		c.m.AcceptedUnknown++
	}
}

func (c *envCounts) metrics(lt LeadTime) EnvironmentMetrics {
	m := c.m
	m.Held, m.Failed = ratio(c.held, c.promotes), ratio(c.failed, c.promotes)
	m.RolledBack, m.VerificationFailed = ratio(c.rolled, c.succeeded), ratio(c.vf, c.vs)
	m.LeadTime = lt
	return m
}

// environmentMetrics counts the operation entries per environment, joining
// lead times against publishes, alphabetically.
func environmentMetrics(entries, publishes []Entry) []EnvironmentMetrics {
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
	lts := leadTimes(ops, publishes)
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

// leadTimes joins, per environment, the first Succeeded and Verified promote
// of each digest per Stage to the newest publish record of the same service
// with that deploy digest, else with the operation's contract as its bundle
// digest. A promotion that joins nothing, or would lead by a negative or
// unparsable time, is unjoined. This is the only D7-specific function.
func leadTimes(ops, publishes []Entry) map[string]LeadTime {
	pub := publishIndex(publishes)
	type acc struct {
		leads    []int64
		unjoined int
	}
	byEnv := map[string]*acc{}
	for _, r := range firstPromotes(ops) {
		env, _, _ := environmentOf(r)
		a := byEnv[env]
		if a == nil {
			a = &acc{}
			byEnv[env] = a
		}
		if lead, ok := promotionLead(r, pub); ok {
			a.leads = append(a.leads, lead)
		} else {
			a.unjoined++
		}
	}
	out := map[string]LeadTime{}
	for env, a := range byEnv {
		slices.Sort(a.leads)
		out[env] = LeadTime{P50Seconds: percentile(a.leads, 50), P90Seconds: percentile(a.leads, 90), Joined: len(a.leads), Unjoined: a.unjoined, Unknown: len(a.leads) == 0}
	}
	return out
}

func publishIndex(publishes []Entry) map[string]time.Time {
	pub := map[string]time.Time{}
	for _, e := range publishes {
		r := e.Record
		for kind, d := range map[string]string{"deploy": r.DeployDigest, "bundle": r.BundleDigest} {
			k := r.Service + "|" + kind + "|" + d
			if t, ok := pub[k]; d != "" && (!ok || r.RecordedAt.After(t)) {
				pub[k] = r.RecordedAt
			}
		}
	}
	return pub
}

// firstPromotes keeps the earliest Succeeded and Verified promote per Stage
// and digest. A promote naming no digest stands alone.
func firstPromotes(ops []Entry) []runrecord.Record {
	first := map[string]runrecord.Record{}
	var alone []runrecord.Record
	for _, e := range ops {
		r := e.Record
		if r.Type != opPromote || r.Outcome != string(delivery.Succeeded) || r.Verification != string(delivery.Verified) {
			continue
		}
		digest := r.To + "|" + r.Contract
		if digest == "|" {
			alone = append(alone, r)
			continue
		}
		key := r.Stage.Namespace + "/" + r.Stage.Name + "/" + r.Stage.Cluster + "|" + digest
		if f, ok := first[key]; !ok || recordTime(r).Before(recordTime(f)) {
			first[key] = r
		}
	}
	return append(alone, slices.Collect(maps.Values(first))...)
}

func promotionLead(r runrecord.Record, pub map[string]time.Time) (int64, bool) {
	at, ok := pub[r.Service+"|deploy|"+r.To]
	if !ok {
		at, ok = pub[r.Service+"|bundle|"+r.Contract]
	}
	done, parsed := finishedAt(r)
	if !ok || !parsed || done.Before(at) {
		return 0, false
	}
	return int64(done.Sub(at) / time.Second), true
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
