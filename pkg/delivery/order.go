package delivery

import (
	"cmp"
	"context"
	"fmt"
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
func orderFor(ctx context.Context, in PlanInput, res *impact.Result, blocked []impact.AffectedConsumer, unseen []string) *Order {
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
// ponytail: Stages are matched on service name and environment, not domain;
// match the domain too when one cluster delivers same-named services from two.
func placeCandidate(ctx context.Context, in PlanInput, m *MoveFirst, newVersion, running string) (none string) {
	if in.StagesErr != nil {
		m.Unknown = "the Stages could not be listed (" + in.StagesErr.Error() + "), so Pacto cannot tell what would move " + m.Service
		return ""
	}
	var stages []StageObject
	for _, s := range in.Stages {
		if s.Spec.Service.Name == m.Service && s.Spec.Environment == in.Stage.Environment {
			stages = append(stages, s)
		}
	}
	switch len(stages) {
	case 0:
		m.Unknown = "no Stage delivers " + m.Service + " in " + in.Stage.Environment + ", so Pacto cannot tell whether a version of it that accepts " + newVersion + " exists"
		return ""
	case 1:
	default:
		refs := make([]string, 0, len(stages))
		for _, s := range stages {
			refs = append(refs, s.Metadata.Namespace+"/"+s.Metadata.Name)
		}
		m.Unknown = fmt.Sprintf("%d Stages deliver %s in %s (%s), so Pacto cannot tell which one must move", len(stages), m.Service, in.Stage.Environment, strings.Join(refs, " and "))
		return ""
	}
	stage := stages[0]
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
	upstream := in.Stages[i].Status
	var unreadable []string
	for _, d := range slices.Backward(upstream.Offered) {
		if !slices.Contains(upstream.Verified, d) || slices.Contains(stage.Status.FailedDigests, d) || d == stage.Status.Pointer {
			continue
		}
		var r Revision
		if in.Candidates != nil {
			r = in.Candidates(ctx, stage, d)
		} else {
			r = Revision{Digest: d, Err: "no registry access was given to read it"}
		}
		if r.Bundle == nil {
			unreadable = append(unreadable, short(d)+": "+r.Err)
			continue
		}
		rng, ok, err := acceptsBoth(r.Bundle.Contract, in.Stage.Service.Name, newVersion, running)
		if err != nil {
			unreadable = append(unreadable, short(d)+": "+err.Error())
			continue
		}
		if ok {
			m.Candidate = &Candidate{Digest: d, Version: r.Bundle.Contract.Service.Version, Accepts: rng, VerifiedOn: ref}
			return ""
		}
	}
	if len(unreadable) > 0 {
		m.Unknown = "Pacto could not read every version " + ref.Namespace + "/" + ref.Name + " verified (" + strings.Join(unreadable, "; ") + ")"
		return ""
	}
	both := newVersion
	if running != "" {
		both = "both " + newVersion + " and the " + running + " running here"
	}
	return "No version of " + m.Service + " that " + ref.Namespace + "/" + ref.Name + " verified accepts " + both + " yet. Ship one that does first"
}

// acceptsBoth reports whether a consumer contract accepts the new provider
// version and the one running now, so moving it first breaks nothing it
// declares. A contract that no longer depends on the provider accepts both.
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

// movableFirst names the consumers a verified version can move first.
func movableFirst(o *Order) []string {
	var out []string
	for _, m := range o.MoveFirst {
		if m.Candidate != nil {
			out = append(out, m.Service)
		}
	}
	return out
}
