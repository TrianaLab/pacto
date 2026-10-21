package delivery

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// orderFor works out what must move before a promote that blocked consumers
// do not accept: for each, the version running here, its range, its Stage in
// this environment and the newest version its upstream Stage verified that
// accepts both the provider version running here and the new one. unseen is
// what kept Pacto from seeing every consumer; it is carried so a short list
// never reads as the whole list.
//
// Every registry read the order makes shares one bound, however many versions
// it walks: a registry that does not answer costs one bound, not one per read.
func orderFor(ctx context.Context, in PlanInput, res *impact.Result, blocked []impact.AffectedConsumer, unseen []string) *Order {
	ctx, cancel := WithRegistryTimeout(ctx)
	defer cancel()
	o := &Order{MoveFirst: []MoveFirst{}, Unknown: unseen}
	provider := fleet.NewServiceKeyDomain(contract.OCIDomain(in.To.ContractRef), in.Stage.Service.Name)
	running := ""
	if in.From.Bundle != nil {
		running = in.From.Bundle.Contract.Service.Version
	}
	for _, c := range blocked {
		m := MoveFirst{Service: c.Service, Domain: c.Domain, Accepts: c.Compatibility,
			Version: runningVersion(in.Snapshot, fleet.NewServiceKeyDomain(c.Domain, c.Service), provider, c.Compatibility)}
		none := placeCandidate(ctx, in, &m, res.NewVersion, running)
		m.Message = moveFirstMessage(m, in.Stage, res.NewVersion, none)
		o.MoveFirst = append(o.MoveFirst, m)
	}
	return o
}

// runningVersion is the version of the consumer revision whose declared range
// is the one impact judged it by.
func runningVersion(snap *fleet.FleetSnapshot, consumer, provider fleet.ServiceKey, rng string) string {
	v := ""
	for _, r := range snap.Relationships {
		if r.Type == fleet.RelationshipDependency && r.Provenance == fleet.ProvenanceDeclared &&
			r.FromService == consumer && r.ToService == provider && r.Compatibility == rng {
			v = snap.Revisions[r.FromRevision].Version
		}
	}
	return v
}

// placeCandidate finds the consumer's Stage in this environment and, through
// its upstream Stage, a verified version that can move first. Anything it
// cannot read is said in m.Unknown, never guessed. With neither a candidate nor
// an unknown, none says in plain words why there is no candidate.
func placeCandidate(ctx context.Context, in PlanInput, m *MoveFirst, newVersion, running string) (none string) {
	stage, ok := consumerStage(in, m, newVersion)
	if !ok {
		return ""
	}
	m.Stage = &StageRef{Name: stage.Metadata.Name, Namespace: stage.Metadata.Namespace}
	up := stage.Spec.Candidates.UpstreamStage
	if up == nil {
		return m.Service + " takes its versions straight from its candidate source here, so no version of it is verified ahead of " +
			in.Stage.Environment + ". Ship one that accepts " + newVersion + " first"
	}
	ref := StageRef{Name: up.Name, Namespace: cmp.Or(up.Namespace, stage.Metadata.Namespace)}
	i := slices.IndexFunc(in.Stages, func(s StageObject) bool {
		return s.Metadata.Name == ref.Name && s.Metadata.Namespace == ref.Namespace
	})
	if i < 0 {
		m.Unknown = "its upstream Stage " + ref.Namespace + "/" + ref.Name + " does not exist or cannot be read"
		return ""
	}
	unreadable, rejected := verifiedCandidate(ctx, in, m, stage, in.Stages[i].Status, ref, newVersion, running)
	switch {
	case m.Candidate != nil:
		return ""
	case len(unreadable) > 0:
		m.Unknown = "Pacto could not read every version " + ref.Namespace + "/" + ref.Name + " verified (" + strings.Join(unreadable, "; ") + ")"
		return ""
	}
	both := newVersion
	if running != "" {
		both = "both " + newVersion + " and the " + running + " running here"
	}
	if len(rejected) > 0 {
		return "No version of " + m.Service + " that " + ref.Namespace + "/" + ref.Name + " verified can move first yet (" +
			strings.Join(rejected, "; ") + "). Ship a newer one that accepts " + both + " and what else runs here first"
	}
	return "No version of " + m.Service + " that " + ref.Namespace + "/" + ref.Name + " verified accepts " + both + " yet. Ship one that does first"
}

// consumerStage is the one Stage that delivers the consumer in this
// environment; anything else is said in m.Unknown.
// ponytail: Stages are matched on service name and environment, not domain;
// match the domain too when one cluster delivers same-named services from two.
func consumerStage(in PlanInput, m *MoveFirst, newVersion string) (StageObject, bool) {
	if in.StagesErr != nil {
		m.Unknown = "the Stages could not be listed (" + in.StagesErr.Error() + "), so Pacto cannot tell what would move " + m.Service
		return StageObject{}, false
	}
	var stages []StageObject
	var refs []string
	for _, s := range in.Stages {
		if s.Spec.Service.Name == m.Service && s.Spec.Environment == in.Stage.Environment {
			stages = append(stages, s)
			refs = append(refs, s.Metadata.Namespace+"/"+s.Metadata.Name)
		}
	}
	switch len(stages) {
	case 1:
		return stages[0], true
	case 0:
		m.Unknown = "no Stage delivers " + m.Service + " in " + in.Stage.Environment + ", so Pacto cannot tell whether a version of it that accepts " + newVersion + " exists"
	default:
		m.Unknown = fmt.Sprintf("%d Stages deliver %s in %s (%s), so Pacto cannot tell which one must move", len(stages), m.Service, in.Stage.Environment, strings.Join(refs, " and "))
	}
	return StageObject{}, false
}

// verifiedCandidate walks what the upstream Stage offered and verified, newest
// first, and sets m.Candidate to the first version newer than the one running
// here that accepts both provider versions, whose other dependencies accept
// what runs in this environment and that what runs here accepts. A version already verified on the consumer's
// own Stage is skipped: promoting it again would be a rollback, which holds the
// Stage. It returns the versions it could not read and, in plain words, why a
// version it could read cannot move first.
func verifiedCandidate(ctx context.Context, in PlanInput, m *MoveFirst, stage StageObject, upstream StageStatus, ref StageRef, newVersion, running string) (unreadable, rejected []string) {
	read := in.Candidates
	if read == nil {
		read = func(_ context.Context, _ StageObject, d string) Revision {
			return Revision{Digest: d, Err: "no registry access was given to read it"}
		}
	}
	outOfTime := func() bool { return errors.Is(ctx.Err(), context.DeadlineExceeded) }
	for _, d := range slices.Backward(upstream.Offered) {
		if !slices.Contains(upstream.Verified, d) || slices.Contains(stage.Status.FailedDigests, d) || slices.Contains(stage.Status.Verified, d) || d == stage.Status.Pointer {
			continue
		}
		if outOfTime() { // the shared bound is spent: say so instead of waiting again
			unreadable = append(unreadable, short(d)+": not read, the registry did not answer within "+registryTimeout.String())
			continue
		}
		r := read(ctx, stage, d)
		if r.Bundle == nil {
			if outOfTime() {
				r.Err = "the registry did not answer within " + registryTimeout.String()
			}
			unreadable = append(unreadable, short(d)+": "+r.Err)
			continue
		}
		c := r.Bundle.Contract
		rng, ok, why, err := canMoveFirst(in, c, fleet.NewServiceKeyDomain(m.Domain, m.Service), m.Version, newVersion, running)
		switch {
		case err != nil:
			unreadable = append(unreadable, short(d)+": "+err.Error())
		case why != "":
			rejected = append(rejected, why)
		case ok:
			m.Candidate = &Candidate{Digest: d, Version: c.Service.Version, Accepts: rng, VerifiedOn: ref}
			return unreadable, rejected
		}
	}
	return unreadable, rejected
}

// canMoveFirst judges one readable version of the consumer: ok when it can
// move first, why in plain words when it accepts both provider versions but
// still cannot, and an error when Pacto cannot tell.
func canMoveFirst(in PlanInput, c *contract.Contract, consumer fleet.ServiceKey, version, newVersion, running string) (rng string, ok bool, why string, err error) {
	rng, ok, err = acceptsBoth(c, in.Stage.Service.Name, newVersion, running)
	if err != nil || !ok {
		return rng, false, "", err
	}
	if why, err = newerThan(c, version); err != nil || why != "" {
		return rng, false, why, err
	}
	if why, err = acceptsWhatRunsHere(in.Snapshot, c, in.Stage.Service.Name); err != nil || why != "" {
		return rng, false, why, err
	}
	why, err = acceptedHere(in.Snapshot, consumer, c)
	return rng, why == "" && err == nil, why, err
}

// acceptedHere checks the declared range every Pacto running here holds on the
// consumer against a candidate version of it, so moving it first does not
// break its own consumers. It says why one of them rejects the version, or
// errors when Pacto cannot tell: a range or a version it cannot read. Observed
// calls carry no range and are not judged here.
func acceptedHere(snap *fleet.FleetSnapshot, consumer fleet.ServiceKey, c *contract.Contract) (string, error) {
	for _, r := range snap.Relationships {
		if r.Type != fleet.RelationshipDependency || r.Provenance != fleet.ProvenanceDeclared ||
			r.ToService != consumer || r.FromService == consumer {
			continue
		}
		from := snap.Revisions[r.FromRevision]
		who := strings.TrimSpace(from.Service + " " + from.Version)
		rng, err := contract.ParseRange(r.Compatibility)
		if err != nil {
			return "", fmt.Errorf("the range %q %s holds on %s cannot be read: %w", r.Compatibility, who, c.Service.Name, err)
		}
		ok, err := rng.Check(c.Service.Version)
		if err != nil {
			return "", fmt.Errorf("its version cannot be read: %w", err)
		}
		if !ok {
			return fmt.Sprintf("%s accepts %s %s, not %s", who, c.Service.Name, r.Compatibility, c.Service.Version), nil
		}
	}
	return "", nil
}

// newerThan says why a candidate is not newer than the consumer version
// running here: moving it first would roll that version back. With no running
// version known there is nothing to roll back.
func newerThan(c *contract.Contract, running string) (string, error) {
	if running == "" {
		return "", nil
	}
	newer, err := contract.ParseRange("> " + running)
	if err != nil {
		return "", fmt.Errorf("the %s running here has no usable version: %w", c.Service.Name, err)
	}
	ok, err := newer.Check(c.Service.Version)
	switch {
	case err != nil:
		return "", fmt.Errorf("its version cannot be read: %w", err)
	case !ok:
		return fmt.Sprintf("%s %s is not newer than the %s running here", c.Service.Name, c.Service.Version, running), nil
	}
	return "", nil
}

// acceptsWhatRunsHere checks every other dependency of a candidate against the
// versions running in this environment (snap is scoped to it). It says why the
// candidate would break against one of them, or errors when Pacto cannot tell:
// a range it cannot read, a running version it cannot read, a required
// dependency it sees nothing of here, or any dependency it sees nothing of when
// the graph is partial.
// ponytail: dependencies are matched on service name, not domain, like
// consumerStage; match the domain too when one environment runs same-named
// services from two.
func acceptsWhatRunsHere(snap *fleet.FleetSnapshot, c *contract.Contract, provider string) (string, error) {
	for _, dep := range c.Dependencies {
		if dep.Name == provider {
			continue
		}
		rng, err := contract.ParseRange(dep.Compatibility)
		if err != nil {
			return "", fmt.Errorf("its range %q on %s cannot be read: %w", dep.Compatibility, dep.Name, err)
		}
		seen := false
		// Every revision of the delivery snapshot but the provider's two sides
		// is a Pacto running here, so revisions are what runs.
		for _, k := range slices.Sorted(maps.Keys(snap.Revisions)) {
			v := snap.Revisions[k].Version
			if snap.Revisions[k].Service != dep.Name {
				continue
			}
			seen = true
			ok, err := rng.Check(v)
			if err != nil {
				return "", fmt.Errorf("the %s running here has no usable version: %w", dep.Name, err)
			}
			if !ok {
				return fmt.Sprintf("%s %s accepts %s %s, but %s %s runs here", c.Service.Name, c.Service.Version, dep.Name, dep.Compatibility, dep.Name, v), nil
			}
		}
		switch {
		case seen:
		case dep.Required:
			return "", fmt.Errorf("it needs %s %s and Pacto sees no %s running here", dep.Name, dep.Compatibility, dep.Name)
		case snap.Completeness == fleet.CompletenessPartial:
			// An optional dependency it sees nothing of may still run here, unread.
			return "", fmt.Errorf("it uses %s %s when it runs and the operational graph is partial, so Pacto cannot tell whether a %s it would break runs here", dep.Name, dep.Compatibility, dep.Name)
		}
	}
	return "", nil
}

// acceptsBoth reports whether a consumer contract's range on the provider
// accepts the new provider version and the one running now. It looks at that
// one range only; acceptsWhatRunsHere checks the rest. A contract that no
// longer depends on the provider accepts both.
func acceptsBoth(c *contract.Contract, provider, newVersion, running string) (string, bool, error) {
	for _, dep := range c.Dependencies {
		if dep.Name != provider {
			continue
		}
		rng, err := contract.ParseRange(dep.Compatibility)
		if err != nil {
			return dep.Compatibility, false, fmt.Errorf("its range %q on %s cannot be read: %w", dep.Compatibility, provider, err)
		}
		for _, v := range []string{newVersion, running} {
			if v == "" {
				continue
			}
			if ok, err := rng.Check(v); err != nil || !ok {
				return dep.Compatibility, false, err
			}
		}
		return dep.Compatibility, true, nil
	}
	return "", true, nil
}

func moveFirstMessage(m MoveFirst, stage StageSpec, newVersion, none string) string {
	who := strings.TrimSpace(m.Service + " " + m.Version)
	msg := fmt.Sprintf("%s runs in %s and accepts %s %s, not %s.", who, stage.Environment, stage.Service.Name, m.Accepts, newVersion)
	switch c := m.Candidate; {
	case c != nil:
		accepts := "accepts it (" + c.Accepts + ")"
		if c.Accepts == "" {
			accepts = "no longer depends on " + stage.Service.Name
		}
		return msg + fmt.Sprintf(" %s %s (%s) %s and verified on %s/%s. Ship %s first: promote %s on Stage %s/%s.",
			m.Service, c.Version, short(c.Digest), accepts, c.VerifiedOn.Namespace, c.VerifiedOn.Name,
			m.Service, c.Digest, m.Stage.Namespace, m.Stage.Name)
	case m.Unknown != "":
		return msg + " Pacto cannot tell what would move it first: " + m.Unknown + "."
	}
	return msg + " " + none + "."
}

// orderQuestion offers shipping the consumers first only when that would
// clear the change: a verified version of every one can move first and Pacto
// saw every consumer. Otherwise it says which can and what still stands.
func orderQuestion(o *Order) string {
	var can, cannot []string
	for _, m := range o.MoveFirst {
		if m.Candidate != nil {
			can = append(can, m.Service)
		} else {
			cannot = append(cannot, m.Service)
		}
	}
	first := strings.Join(can, ", ")
	switch {
	case len(can) == 0:
		return ""
	case len(cannot) > 0:
		return ". " + first + " can move first, but no version of " + strings.Join(cannot, ", ") + " can yet, so that alone does not clear it (see the plan's order)"
	case len(o.Unknown) > 0:
		return ". " + first + " can move first, but Pacto cannot see every consumer, so that alone may not clear it (see the plan's order)"
	}
	return ". Or ship " + first + " first: a version that accepts it already verified upstream (see the plan's order)"
}
