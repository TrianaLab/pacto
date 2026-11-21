package app

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
)

// propose overlays what pacto ci proposes for f on the bundle the plan
// judged and publishes (f.judged) and on the planned revision (f.b): the
// index ref of each dependency by name, a range for each unknown range from
// the target snapshot snap (nil when none was read) and the expiry of each
// undated break. A row decided before its repository proposes nothing. No
// proposal reaches a verdict: impact already ran, and a proposal never
// changes a revision's declared dependencies.
func (pl *ciPlanner) propose(ctx context.Context, f *ciFolder, snap *fleet.FleetSnapshot, opts CIOptions) {
	if f.judged == nil || f.row.Repository == "" {
		return
	}
	rec, _ := contract.ReadInferredRecord(f.judged.FS) // ponytail: completion just wrote and verified it
	out := discover.Propose(rec, f.judged.Contract, pl.s.proposeInputs(ctx, snap, f.judged, f.dir, f.row.Repository, opts))
	if out == rec {
		return
	}
	data := out.Marshal()
	f.judged.FS = contract.WithInferredRecord(f.judged.FS, data)
	if f.b != f.judged {
		f.b.FS = contract.WithInferredRecord(f.b.FS, data)
	}
	f.row.Inferred = len(out.Fields)
	if opts.proposed != nil {
		opts.proposed[f.row.Dir] = true
	}
}

// proposeInputs reads what b, published to repo from dir, gets proposals
// from: the organization index for its dependencies by name, snap for its
// unknown ranges and HEAD's commit time for its undated breaks. Each is
// read only when b has something it would propose for.
func (s *Service) proposeInputs(ctx context.Context, snap *fleet.FleetSnapshot, b *contract.Bundle, dir, repo string, opts CIOptions) discover.ProposeInput {
	domain := contract.OCIDomain(repo)
	in := discover.ProposeInput{}
	var byName []string
	for _, d := range b.Contract.Dependencies {
		if !d.External && d.Ref == "" {
			byName = append(byName, d.Name)
		}
	}
	if len(byName) > 0 && opts.Fleet.Index != IndexNone {
		in.Refs = s.indexRefs(ctx, indexCandidates(opts.Fleet.Index, repo), domain, byName)
	}
	in.Running = runningProviders(snap, fleet.NewServiceKeyDomain(domain, b.Contract.Service.Name))
	if slices.ContainsFunc(b.Contract.Breaks, func(br contract.Break) bool { return br.Expires == "" }) {
		in.Commit = s.localInputs(dir, opts.revision).Commit
		in.Epoch = commitEpoch(ctx, opts.Getenv, s.env().run, dir)
	}
	return in
}

// indexRefs are the refs the organization index among candidates names for
// the services called names in domain, each with its index: source. The
// index holds one newest entry per service key, so a name in one domain is
// never ambiguous. An index that is not read proposes nothing.
func (s *Service) indexRefs(ctx context.Context, candidates []string, domain string, names []string) map[string]discover.Sourced {
	rd := s.IndexEntries(ctx, candidates)
	if rd.status != impact.IndexRead {
		return nil
	}
	refs := map[string]discover.Sourced{}
	for _, e := range rd.entries {
		for _, name := range names {
			if key := fleet.NewServiceKeyDomain(domain, name); e.Service == string(key) {
				repo, _ := contract.SplitOCIDigest(e.Bundle)
				tag, _ := orgindex.Tag(key, e.Version) // ponytail: a confirmed entry was read under this tag
				refs[name] = discover.Sourced{Value: contract.OCIScheme + repo, Source: contract.SourceIndex + rd.repo + ":" + tag}
			}
		}
	}
	return refs
}

// runningProviders are, by dependency name, the versions of each provider of
// consumer running in a cluster and namespace where consumer runs, with the
// pinned target snapshot they were read from. Only targets a target snapshot
// reported count: with none, nothing is proposed.
func runningProviders(snap *fleet.FleetSnapshot, consumer fleet.ServiceKey) map[string]discover.Sourced {
	if snap == nil {
		return nil
	}
	refs := map[string]string{} // source id -> pinned snapshot ref
	for _, st := range snap.Sources {
		if st.Kind == fleet.SourceKindTargetSnapshot && strings.Contains(st.Ref, "@sha256:") {
			refs[st.ID] = st.Ref
		}
	}
	scopes := map[string]bool{}
	for _, t := range snap.Targets {
		if t.ServiceKey == consumer && refs[t.Source] != "" {
			scopes[targetScope(t.Key)] = true
		}
	}
	out := map[string]discover.Sourced{}
	for _, rel := range snap.Relationships {
		if rel.FromService != consumer || rel.Type != fleet.RelationshipDependency || !rel.Resolved {
			continue
		}
		if run, ok := providerVersions(snap, rel.ToService, scopes, refs); ok {
			out[rel.To] = run
		}
	}
	return out
}

// providerVersions are the distinct versions provider runs at in scopes and
// the smallest snapshot ref that reported one.
//
// ponytail: one source per proposal; with providers in two snapshots the
// first ref by string names them both.
func providerVersions(snap *fleet.FleetSnapshot, provider fleet.ServiceKey, scopes map[string]bool, refs map[string]string) (discover.Sourced, bool) {
	var versions []string
	ref := ""
	for _, t := range snap.Targets {
		rev := targetRevision(snap, t)
		if t.ServiceKey != provider || refs[t.Source] == "" || !scopes[targetScope(t.Key)] || rev == nil || rev.Version == "" {
			continue
		}
		if !slices.Contains(versions, rev.Version) {
			versions = append(versions, rev.Version)
		}
		if ref == "" || refs[t.Source] < ref {
			ref = refs[t.Source]
		}
	}
	if len(versions) == 0 {
		return discover.Sourced{}, false
	}
	slices.Sort(versions)
	return discover.Sourced{Value: strings.Join(versions, ", "), Source: contract.SourceSnapshot + ref}, true
}

// targetScope is a target's cluster and namespace.
func targetScope(k fleet.TargetKey) string {
	cluster, scope, _, _, _ := fleet.ParseTargetKey(k)
	return cluster + "/" + scope
}

// targetRevision is the revision the fleet linked t to, nil when it made no
// link (an ambiguous or inconsistent identity never proposes a range).
func targetRevision(snap *fleet.FleetSnapshot, t *fleet.TargetRecord) *fleet.ContractRevision {
	return snap.Revisions[t.ContractRevision]
}

// ciRanges is the range coverage of every walked service's dependencies on
// other services: declared when the folder's pacto.yaml declares a range,
// accepted when a carried acceptance pinned it, else unknown with the range
// the plan proposes for it.
func ciRanges(folders []*ciFolder, root string) *impact.RangeCoverage {
	cov := &impact.RangeCoverage{}
	n := 0
	for _, f := range folders {
		if f.judged == nil {
			continue
		}
		n++
		rec, _ := contract.ReadInferredRecord(f.judged.FS) // ponytail: completion and propose just wrote it
		for _, d := range f.judged.Contract.Dependencies {
			if d.External {
				continue
			}
			cov.Edges++
			switch {
			case declaresRange(f.asWritten, d.Name):
				cov.Declared++
			case contract.IsRange(d.Compatibility):
				cov.Accepted++
			default:
				cov.AddUnknown(impact.UnknownRange{From: f.row.Service, To: d.Name, Proposed: rec.Proposed("dependencies[" + d.Name + "].compatibility")})
			}
		}
	}
	services := " services in "
	if n == 1 {
		services = " service in "
	}
	cov.Scope = "dependencies of " + strconv.Itoa(n) + services + root
	return cov
}

// declaresRange reports whether b's pacto.yaml declares a range on its
// dependency name.
func declaresRange(b *contract.Bundle, name string) bool {
	return slices.ContainsFunc(b.Contract.Dependencies, func(d contract.Dependency) bool { return d.Name == name && contract.IsRange(d.Compatibility) })
}
