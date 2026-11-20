package app

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/override"
)

// MaxRunningRevisions bounds the running revisions one --running answer
// judges. ponytail: each is a pull and an analysis; more are listed unread
// (so the answer never clears) and never judged. Raise it when a service
// really runs more distinct revisions at once.
const MaxRunningRevisions = 8

// maxUnreadLines bounds the unread lines one answer carries; the last says
// how many more there were.
const maxUnreadLines = 32

// ImpactRunning judges the new revision against the revisions of its
// service that run on known targets, read from snap or, when snap is nil,
// from a snapshot built from opts.Fleet. opts.OldPath is ignored. Each
// running revision is analysed as the old side and the answer whose gate is
// worst is returned. With none, the new revision is compared with itself and
// only the consumers' ranges speak (a first version). A running revision
// Pacto could not link, pull or trust never lets the answer clear. Callers
// answering from a restricted view must not call it: a hidden target could
// hide a running revision.
func (s *Service) ImpactRunning(ctx context.Context, opts ImpactOptions, snap *fleet.FleetSnapshot) (*impact.Result, error) {
	opts.OldPath = ""
	newBundle, err := s.resolveNewRevision(ctx, opts)
	if err != nil {
		return nil, err
	}
	if snap == nil {
		opts.Fleet.indexSrc, _ = s.consumerIndex(opts, newBundle)
	}
	opts, look, derived := s.deriveTargetSnapshot(ctx, opts)
	if snap == nil || derived {
		if snap, err = s.Fleet(ctx, opts.Fleet); err != nil {
			return nil, fmt.Errorf("build fleet snapshot: %w", err)
		}
	}
	name := newBundle.Contract.Service.Name
	if opts.Domain == "" && !graph.ParseDependencyRef(opts.NewPath).IsOCI() {
		domain, found, err := runningDomain(snap, name)
		if err != nil {
			return nil, fmt.Errorf("%w; judge its oci:// reference so Pacto knows its domain (in Argo CD, render a Pacto object whose contractRef names it)", err)
		}
		opts.Domain, opts.notInFleet = domain, !found
	}
	key, allowed := fleet.NewServiceKeyDomain(opts.domain(snap, name), name), runningHosts(opts)
	refs, unread := runningRefs(snap, key, name, allowed)
	// The analysis may rebuild the snapshot (an index, a missing service):
	// running refs read from a snapshot that no longer agrees never clear.
	drifted := func(s *fleet.FleetSnapshot) bool {
		again, _ := runningRefs(s, key, name, allowed)
		return !slices.Equal(again, refs)
	}
	res, err := s.judgeRunning(ctx, opts, snap, newBundle, refs, unread, drifted)
	if err != nil {
		return nil, err
	}
	res.Registry = look
	return res, nil
}

// judgeRunning pulls each running ref and analyses the change against it,
// keeping the worst gate; with no readable ref it analyses a first version.
// A snapshot the analysis rebuilt whose running refs drifted from refs adds
// an unread line.
func (s *Service) judgeRunning(ctx context.Context, opts ImpactOptions, snap *fleet.FleetSnapshot, newBundle *contract.Bundle, refs, unread []string,
	drifted func(*fleet.FleetSnapshot) bool) (*impact.Result, error) {
	type baseline struct {
		ref    string
		bundle *contract.Bundle
	}
	var read []baseline
	for _, ref := range refs {
		b, err := s.resolveBundleWithOverrides(ctx, ref, override.Overrides{})
		if err != nil {
			// The error is never echoed: it may carry credentials or hosts.
			unread = append(unread, ref+": could not be pulled")
			continue
		}
		read = append(read, baseline{ref, b})
	}
	if n := len(unread); n > maxUnreadLines {
		unread = append(unread[:maxUnreadLines-1:maxUnreadLines-1], strconv.Itoa(n-maxUnreadLines+1)+" more running revisions Pacto could not read")
	}
	opts.unreadBaselines, opts.running = unread, true
	running := &impact.RunningBaseline{Refs: refs, Unread: unread}
	if len(read) == 0 {
		// Nothing to compare with: ranges alone can block, and unread lines
		// still withhold a clear.
		read = []baseline{{"", newBundle}}
		opts.firstVersion, running.FirstVersion = true, true
	}
	var worst *impact.Result
	var judged baseline
	// One snapshot per repository: revisions of one repository need the
	// same index and the same rebuild.
	prepared := map[string]preparedImpact{}
	for _, b := range read {
		o := opts
		o.OldPath = cmp.Or(b.ref, opts.NewPath)
		p, ok := prepared[ociRepo(o.OldPath)]
		if !ok {
			var err error
			if p, err = s.prepareImpact(ctx, o, snap, true, newBundle); err != nil {
				return nil, err
			}
			if p.snap != snap && drifted(p.snap) {
				running.Unread = append(slices.Clip(running.Unread), "the revisions of "+newBundle.Contract.Service.Name+" that run changed while Pacto read them; run the analysis again")
				p.opts.unreadBaselines = running.Unread
			}
			prepared[ociRepo(o.OldPath)] = p
		}
		p.opts.OldPath = o.OldPath
		res, err := s.analyzePrepared(ctx, p, b.bundle, newBundle)
		if err != nil {
			return nil, err
		}
		if worst == nil || gateRank[res.Gate] > gateRank[worst.Gate] {
			worst, judged = res, b
		}
	}
	if opts.OnResolved != nil {
		opts.OnResolved(judged.bundle, newBundle)
	}
	running.Judged = judged.ref
	worst.Running = running
	return worst, nil
}

// gateRank orders gates from best to worst: the worst answer is returned.
var gateRank = map[string]int{impact.GateClear: 0, impact.GateUnverified: 1, impact.GateBlocked: 2}

// runningDomain is the fleet domain of the one declared service named name
// in snap, whatever the domain: a name several domains hold is refused with
// their keys (*fleet.AmbiguousError), even when one of them is the default
// domain, so services of the same name are never mixed or shadowed. found is
// false when no domain holds it.
func runningDomain(snap *fleet.FleetSnapshot, name string) (domain string, found bool, err error) {
	var keys []string
	for _, k := range slices.Sorted(maps.Keys(snap.Services)) {
		if s := snap.Services[k]; s.Name == name && !s.Inferred() {
			keys, domain = append(keys, string(k)), s.Domain
		}
	}
	if len(keys) > 1 {
		return "", false, &fleet.AmbiguousError{Kind: "service", ID: name, Matches: keys}
	}
	return domain, len(keys) == 1, nil
}

// runningRefs are the distinct registry refs of the revisions the targets
// of key run, sorted and bounded by MaxRunningRevisions, and the running
// revisions Pacto cannot judge, in words: a target with no linked revision,
// a revision with no registry ref, a ref linked by tag (judged, but never
// proof), a ref on a host allowed rejects, and an inferred workload of the
// service in any domain.
func runningRefs(snap *fleet.FleetSnapshot, key fleet.ServiceKey, name string, allowed func(ref string) bool) (refs, unread []string) {
	refs = []string{}
	seen := map[string]bool{}
	note := func(line string) {
		if !seen[line] {
			seen[line] = true
			unread = append(unread, line)
		}
	}
	for _, tk := range slices.Sorted(maps.Keys(snap.Targets)) {
		t := snap.Targets[tk]
		if t.ServiceKey != key {
			continue
		}
		ref, why := targetRef(snap, t)
		if why != "" {
			note(why)
		}
		if ref == "" {
			continue
		}
		if !allowed(ref) {
			note(ref + ": registry not allowed")
			continue
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	for _, line := range inferredRunning(snap, name) {
		note(line)
	}
	for _, st := range snap.Sources {
		// An unread workload set may hide an undeclared revision.
		if st.Kind == fleet.SourceKindKubernetesWorkloads && st.Status != fleet.SourceAvailable && !st.Advisory {
			note("the workloads " + st.ID + " reads could not all be read, so an undeclared revision of " + name + " may run there")
		}
	}
	slices.Sort(refs)
	if n := len(refs); n > MaxRunningRevisions {
		note(strconv.Itoa(n-MaxRunningRevisions) + " more running revisions were not judged")
		refs = refs[:MaxRunningRevisions]
	}
	return refs, unread
}

// targetRef is the registry ref of the revision t runs, "" when Pacto cannot
// judge it, and why it cannot clear the answer, "" when it can.
func targetRef(snap *fleet.FleetSnapshot, t *fleet.TargetRecord) (ref, why string) {
	rev := snap.Revisions[t.ContractRevision]
	switch {
	case rev == nil:
		return "", string(t.Key) + " runs a revision Pacto could not link"
	case rev.ResolvedRef == "":
		return "", string(t.Key) + " runs a revision with no registry ref"
	}
	ref = "oci://" + contract.TrimOCIScheme(rev.ResolvedRef)
	if t.RevisionMatch != "exact" {
		why = string(t.Key) + " is linked to " + ref + " by tag, not digest"
	}
	return ref, why
}

// inferredRunning names, in any domain, each inferred workload whose
// service name, workload name or group name is name: it may run a revision
// no contract describes.
func inferredRunning(snap *fleet.FleetSnapshot, name string) []string {
	var out []string
	for _, sk := range slices.Sorted(maps.Keys(snap.Services)) {
		s := snap.Services[sk]
		if w := s.Workload; s.Inferred() && w != nil && (s.Name == name || w.Name == name || w.Group != nil && w.Group.Name == name) {
			out = append(out, strings.TrimPrefix(w.Cluster+"/"+w.Namespace+"/"+w.Name, "/")+" runs an undeclared revision of "+name)
		}
	}
	return out
}

// runningHosts reports whether a running ref's registry host is one
// ImpactRunning may pull from: the hosts of the target snapshots, of an
// oci:// NewPath and of opts.RunningRegistries. Any other host gets no
// request and no credentials.
func runningHosts(opts ImpactOptions) func(string) bool {
	hosts := map[string]bool{}
	for _, h := range opts.RunningRegistries {
		hosts[strings.TrimSpace(h)] = true
	}
	for _, ref := range append([]string{opts.NewPath}, opts.Fleet.TargetSnapshot...) {
		if graph.ParseDependencyRef(ref).IsOCI() {
			hosts[refHost(ref)] = true
		}
	}
	delete(hosts, "")
	return func(ref string) bool { return hosts[refHost(ref)] }
}

// refHost is the registry host of an oci:// ref.
func refHost(ref string) string {
	host, _, _ := strings.Cut(contract.TrimOCIScheme(ref), "/")
	return host
}
