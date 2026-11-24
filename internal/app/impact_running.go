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
	"github.com/trianalab/pacto/v4/pkg/oci"
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
	opts.Fleet = opts.Fleet.withoutListings()
	opts.OldPath = ""
	newBundle, newDigest, err := s.newRevision(ctx, opts)
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
			return nil, fmt.Errorf("%w; judge its oci:// reference so Pacto knows its domain", err)
		}
		opts.Domain, opts.notInFleet = domain, !found
	}
	key, allowed := fleet.NewServiceKeyDomain(opts.domain(snap, name), name), runningHosts(opts)
	refs, excluded, unread := runningRefs(snap, key, name, newDigest, allowed)
	if newDigest == "" && graph.ParseDependencyRef(opts.NewPath).IsOCI() {
		// Nothing can be excluded, so a running revision may be the new one
		// judged against itself: the answer never clears.
		unread = append(unread, "the registry did not say which digest "+opts.NewPath+" names, so a running revision may be the new one")
	}
	opts.newRevisionRuns = len(excluded) > 0
	// The analysis may rebuild the snapshot (an index, a missing service):
	// running refs read from a snapshot that no longer agrees never clear.
	drifted := func(s *fleet.FleetSnapshot) bool {
		again, _, _ := runningRefs(s, key, name, newDigest, allowed)
		return !slices.Equal(again, refs)
	}
	res, err := s.judgeRunning(ctx, opts, snap, newBundle, refs, unread, drifted)
	if err != nil {
		return nil, err
	}
	res.Running.Excluded = excluded
	res.Registry = look
	return res, nil
}

// newRevision is resolveNewRevision plus, for an oci:// new ref, the
// manifest digest of the artifact pulled, from the same read
// (oci.PullPinned): a second lookup of a mutable tag may name other bytes.
// The digest is "" for a local folder or when the registry would not say.
func (s *Service) newRevision(ctx context.Context, opts ImpactOptions) (*contract.Bundle, string, error) {
	parsed := graph.ParseDependencyRef(opts.NewPath)
	if !parsed.IsOCI() {
		b, err := s.resolveNewRevision(ctx, opts)
		return b, "", err
	}
	read, digest, err := s.pullOCIPinned(ctx, parsed.Location)
	var b *contract.Bundle
	if err == nil {
		b, err = applyOverrides(read, opts.NewOverrides)
	}
	if err != nil {
		return nil, "", fmt.Errorf("new revision: %w", err)
	}
	return b, digest, nil
}

// pullOCIPinned is resolveOCIBundle plus the digest of the artifact pulled.
func (s *Service) pullOCIPinned(ctx context.Context, location string) (*contract.Bundle, string, error) {
	if err := s.requireBundleStore(); err != nil {
		return nil, "", err
	}
	resolved, err := oci.ResolveRef(ctx, s.BundleStore, location, "")
	if err != nil {
		return nil, "", err
	}
	return oci.PullPinned(ctx, s.BundleStore, resolved)
}

// runsNew reports whether t is observed to run the revision with digest
// newDigest through ref: a post-deploy gate verifies after the new revision
// already runs, and a revision is never its own baseline. A tag link counts
// only when the target's own digest says so, never on the tag alone.
func runsNew(t *fleet.TargetRecord, ref, newDigest string) bool {
	_, d := contract.SplitOCIDigest(ref)
	return newDigest != "" && d == newDigest && (t.RevisionMatch == "exact" || t.Digest == newDigest)
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
// of key run, each pinned to its digest, sorted and bounded by
// MaxRunningRevisions; the refs of the targets observed to run newDigest,
// left out (runsNew); and the
// running revisions Pacto cannot judge, in words: a target with no linked
// revision, a revision with no registry ref, a ref linked by tag (judged,
// but never proof), a ref on a host allowed rejects, a linked workload whose
// version is a guess, and an inferred workload of the service in any domain.
func runningRefs(snap *fleet.FleetSnapshot, key fleet.ServiceKey, name, newDigest string, allowed func(ref string) bool) (refs, excluded, unread []string) {
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
		guessed := guessedRevision(t, note)
		if ref == "" {
			continue
		}
		if !allowed(ref) {
			note(ref + ": registry not allowed")
			continue
		}
		switch {
		case runsNew(t, ref, newDigest):
			if !guessed && !slices.Contains(excluded, ref) {
				excluded = append(excluded, ref)
			}
		case !slices.Contains(refs, ref):
			refs = append(refs, ref)
		}
	}
	for _, line := range inferredRunning(snap, name) {
		note(line)
	}
	for _, line := range unreadWorkloads(snap, name) {
		note(line)
	}
	slices.Sort(refs)
	slices.Sort(excluded)
	if n := len(refs); n > MaxRunningRevisions {
		note(strconv.Itoa(n-MaxRunningRevisions) + " more running revisions were not judged")
		refs = refs[:MaxRunningRevisions]
	}
	return refs, excluded, unread
}

// guessedRevision reports whether t is a linked workload whose images other
// versions of its service also record, and notes why: its revision is a
// guess, judged as a baseline but never proof the new revision runs.
func guessedRevision(t *fleet.TargetRecord, note func(string)) bool {
	if t.Link == nil || len(t.Link.OtherVersions) == 0 {
		return false
	}
	note(string(t.Key) + " runs images that other versions of the service also record, so its running revision is a guess")
	return true
}

// targetRef is the registry ref of the revision t runs, "" when Pacto cannot
// judge it, and why it cannot clear the answer, "" when it can.
func targetRef(snap *fleet.FleetSnapshot, t *fleet.TargetRecord) (ref, why string) {
	rev := snap.Revisions[t.ContractRevision]
	switch {
	case rev == nil && pinnedTargetRef(t) != "":
		// The target itself names the exact revision it runs, pinned by its own
		// digest: a live cluster read whose running revision is no longer the
		// tag the registry lists has no revision record to link to, but the
		// ref is still exact proof of what runs, so it is judged, never unread.
		return pinnedTargetRef(t), ""
	case rev == nil:
		return "", string(t.Key) + " runs a revision Pacto could not link"
	case rev.ResolvedRef == "":
		return "", string(t.Key) + " runs a revision with no registry ref"
	}
	ref = "oci://" + contract.TrimOCIScheme(rev.ResolvedRef)
	// Pinned so the pull, the answer and the new-digest exclusion all name
	// the revision the target runs: its own digest first, since a tag link's
	// revision digest is only what the tag pointed at when it was indexed.
	if _, d := contract.SplitOCIDigest(ref); d == "" && cmp.Or(t.Digest, rev.Digest) != "" {
		ref += "@" + cmp.Or(t.Digest, rev.Digest)
	}
	if t.RevisionMatch != "exact" {
		why = string(t.Key) + " is linked to " + ref + " by tag, not digest"
	}
	return ref, why
}

// pinnedTargetRef is the oci:// ref t reports running when it is pinned by a
// digest that agrees with t's own, "" otherwise: a tag alone, or a digest
// that contradicts the target's, proves nothing.
func pinnedTargetRef(t *fleet.TargetRecord) string {
	_, d := contract.SplitOCIDigest(t.ResolvedRef)
	if d == "" || d != t.Digest {
		return ""
	}
	return "oci://" + contract.TrimOCIScheme(t.ResolvedRef)
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

// unreadWorkloads names each workload source Pacto could not fully read:
// an unread workload set may hide an undeclared revision of name.
func unreadWorkloads(snap *fleet.FleetSnapshot, name string) []string {
	var out []string
	for _, st := range snap.Sources {
		if st.Kind == fleet.SourceKindKubernetesWorkloads && st.Status != fleet.SourceAvailable && !st.Advisory {
			out = append(out, "the workloads "+st.ID+" reads could not all be read, so an undeclared revision of "+name+" may run there")
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
