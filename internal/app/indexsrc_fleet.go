package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
)

// limInferredContractUnread marks an inferred contract an indexed bundle
// names that the whole-index source did not read. It never makes the source
// partial: the external system stays, only its inferred contract is missing.
const limInferredContractUnread = "INFERRED_CONTRACT_UNREAD"

// maxIndexInferredRefs bounds the inferred contracts one whole-index read
// pulls.
//
// ponytail: a fixed bound on the second pass; raise it, or read pacto-inferred by listing (515-S4), if an organization names more inferred contracts.
var maxIndexInferredRefs = 200

// indexFleetSource is the whole organization index as a fleet source: the
// newest entry of every service it lists, pulled by digest with the same
// checks as the registry source, and the inferred contracts those bundles
// name under the installation's own pacto-inferred. Its read problems are its
// state and limitations, filed under its own id.
type indexFleetSource struct {
	svc         *Service
	repo        string
	longRunning bool
	configure   func(*fleetsrc.OCISource)
	// unread is what the second pass could not read, reported by its
	// advisory companion once done closes.
	unread []fleet.Limitation
	done   chan struct{}
	once   sync.Once
}

// newIndexFleetSource is the whole-index source over repo and its advisory
// companion.
func newIndexFleetSource(svc *Service, repo string, longRunning bool, configure func(*fleetsrc.OCISource)) (*indexFleetSource, *inferredUnreadSource) {
	s := &indexFleetSource{svc: svc, repo: repo, longRunning: longRunning, configure: configure, done: make(chan struct{})}
	return s, &inferredUnreadSource{of: s}
}

// ID implements [fleet.Source].
func (s *indexFleetSource) ID() string { return "registry-index" }

// Kind implements [fleet.Source].
func (s *indexFleetSource) Kind() string { return "index" }

// Collect implements [fleet.Source]. The read timeout bounds the listing
// only; the pulls run under the caller's context, as the registry source's do.
func (s *indexFleetSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	defer s.once.Do(func() { close(s.done) })
	candidates := indexCandidates(s.repo, "")
	lctx, cancel := context.WithTimeout(ctx, indexReadTimeout)
	rd := s.svc.IndexEntries(lctx, candidates)
	cancel()
	byRef := map[string][]orgindex.Entry{}
	var refs []string
	for _, e := range rd.entries {
		ref := contract.OCIScheme + e.Bundle
		if byRef[ref] == nil {
			refs = append(refs, ref)
		}
		byRef[ref] = append(byRef[ref], e)
	}
	coll, err := s.pull(ctx, refs)
	if err != nil {
		at := rd.at
		return &fleet.Collection{State: &fleet.SourceState{Status: fleet.SourceUnavailable, ObservedAt: &at,
			Error: &fleet.SourceError{Code: "UNAVAILABLE", Message: redactDigests(err.Error())}}}, nil
	}
	s.keepNamed(coll, byRef)
	coll.State = s.readState(rd, candidates[0], coll)
	s.unread = s.readInferred(ctx, coll, strings.TrimSuffix(candidates[0], "/"+orgindex.RepositoryName))
	return coll, nil
}

// inferredUnreadSource reports the inferred contracts its whole-index source
// could not read, filed under that source. It is advisory: a dangling ref
// one publisher writes loses only an annotation, and never holds the
// organization's catalog back.
//
// ponytail: a companion source because a collection limitation always makes
// its own source partial (fleet.Build); fold it back if pkg/fleet grows a
// non-degrading collection note.
type inferredUnreadSource struct{ of *indexFleetSource }

// ID implements [fleet.Source].
func (s *inferredUnreadSource) ID() string { return "registry-index-inferred" }

// Kind implements [fleet.Source].
func (s *inferredUnreadSource) Kind() string { return "index" }

// Collect implements [fleet.Source]: what the whole-index source's second
// pass left unread, once it is done.
func (s *inferredUnreadSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	select {
	case <-s.of.done:
		return &fleet.Collection{Limitations: s.of.unread, Advisory: true}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// pull reads refs through a registry source checked like the --oci one.
func (s *indexFleetSource) pull(ctx context.Context, refs []string) (*fleet.Collection, error) {
	src := fleetsrc.NewOCISource(s.ID(), s.svc.BundleStore, refs)
	s.configure(src)
	return src.Collect(ctx)
}

// keepNamed drops every revision no entry pulling it names, with one
// limitation per entry that names another service than its bundle, naming
// the entry's tag, never its content. Each bundle is pulled once, however
// many entries point at it.
func (s *indexFleetSource) keepNamed(coll *fleet.Collection, byRef map[string][]orgindex.Entry) {
	coll.Revisions = slices.DeleteFunc(coll.Revisions, func(r fleet.RawRevision) bool {
		named := false
		for _, e := range byRef[r.RequestedRef] {
			if entryNamesRevision(e, r) {
				named = true
				continue
			}
			tag, _ := orgindex.Tag(fleet.ServiceKey(e.Service), e.Version) // a read entry validated its tag
			coll.Limitations = append(coll.Limitations, fleet.Limitation{Code: orgindex.LimitationEntryInvalid, Source: s.ID(),
				Message: "index entry " + tag + " not used: its bundle is another service"})
		}
		return !named
	})
}

// readState is the state of the read rd of repo, its limitations filed
// under this source. A missing index keeps the impact meaning (partial) in
// a one-shot command; a long-running reader that never listed entries there
// reads it empty, since a fresh installation has published nothing yet.
func (s *indexFleetSource) readState(rd *indexRead, repo string, coll *fleet.Collection) *fleet.SourceState {
	lims := withSource(s.ID(), rd.lims)
	if rd.status == impact.IndexNotFound {
		if s.longRunning && !s.svc.indexSeen(repo) {
			lims = nil
		} else {
			lims[0].Message = rd.note + ": the services it would list are unknown"
		}
	}
	return indexReadState(rd, lims, coll)
}

// readInferred pulls the inferred contracts the read revisions name under
// root's pacto-inferred, at most maxIndexInferredRefs, and adds them to
// coll. What it could not read comes back as INFERRED_CONTRACT_UNREAD
// limitations, outside the source state.
func (s *indexFleetSource) readInferred(ctx context.Context, coll *fleet.Collection, root string) []fleet.Limitation {
	refs, named := inferredRefs(coll, root, maxIndexInferredRefs)
	var lims []fleet.Limitation
	if len(refs) < named {
		lims = append(lims, fleet.Limitation{Source: s.ID(),
			Message: fmt.Sprintf("the indexed contracts name %d inferred contracts; only the first %d were read", named, len(refs))})
	}
	if len(refs) > 0 {
		more, err := s.pull(ctx, refs)
		if err != nil {
			more = &fleet.Collection{Limitations: []fleet.Limitation{{Source: s.ID(), Message: "the inferred contracts were not read: " + redactDigests(err.Error())}}}
		}
		coll.Revisions = append(coll.Revisions, more.Revisions...)
		coll.Providers = mergeRows(coll.Providers, more.Providers)
		lims = append(more.Limitations, lims...)
	}
	for i := range lims {
		lims[i].Code = limInferredContractUnread
	}
	return lims
}

// inferredRefs are the dependency refs of coll's revisions under
// root/pacto-inferred that name an external identity, deduplicated, minus the
// refs already read, sorted and cut at limit; named counts them all. A ref on
// another host or root is never pulled: credentials go only where the index is.
func inferredRefs(coll *fleet.Collection, root string, limit int) (refs []string, named int) {
	prefix := oci.InstallationRepository(root, contract.InferredRepository) + "/"
	seen := map[string]bool{}
	for _, r := range coll.Revisions {
		seen[contract.TrimOCIScheme(r.RequestedRef)] = true
	}
	for _, r := range coll.Revisions {
		for _, d := range r.Bundle.Contract.Dependencies {
			ref := contract.TrimOCIScheme(d.Ref)
			if _, ok := contract.InferredRef(ref); !ok || !strings.HasPrefix(ref, prefix) || seen[ref] {
				continue
			}
			seen[ref] = true
			refs = append(refs, contract.OCIScheme+ref)
		}
	}
	slices.Sort(refs)
	return refs[:min(len(refs), limit)], len(refs)
}

// mergeRows adds more's registry rows to rows, one row per id: a host both
// passes read keeps one row naming every service read there.
func mergeRows(rows, more []fleet.Provider) []fleet.Provider {
	for _, p := range more {
		i := slices.IndexFunc(rows, func(r fleet.Provider) bool { return r.ID == p.ID })
		if i < 0 {
			rows = append(rows, p)
			continue
		}
		for _, k := range p.Services {
			if !slices.Contains(rows[i].Services, k) {
				rows[i].Services = append(rows[i].Services, k)
			}
		}
	}
	return rows
}

// indexFleetSources is the whole-index source opts.ReadIndex names, none
// for "" or IndexNone.
func (s *Service) indexFleetSources(opts FleetOptions) []fleet.Source {
	switch {
	case opts.ReadIndex == "" || opts.ReadIndex == IndexNone:
		return nil
	case s.BundleStore == nil:
		return []fleet.Source{fleet.NewFailingSource("registry-index", "index", errNoBundleStore)}
	}
	src, unread := newIndexFleetSource(s, opts.ReadIndex, opts.LongRunning, s.registryChecks(opts))
	return []fleet.Source{src, unread}
}
