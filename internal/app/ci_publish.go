package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// CIPublishOptions configure one pacto ci --publish run.
type CIPublishOptions struct {
	CI CIOptions
	// Images maps a service to the digest-pinned image it deploys; the key
	// "" is an --image with no service, which needs exactly one changed
	// service.
	Images map[string]string
	// SignKey and NoTransparencyLog shape the publish section Pacto derives
	// for a contract that declares none.
	SignKey           string
	NoTransparencyLog bool
	// Revision is stamped on every artifact; empty reads each folder's git
	// commit.
	Revision string
	// Record, when set, gets every service the run decided: its row and,
	// when Publish ran, its result and error.
	Record func(row ci.Row, res *publish.Result, err error)
}

// planModeNote marks a derived VERSION plan mode read without verifying the
// baseline's signature; publish verifies it.
const planModeNote = "baseline unverified in plan mode"

// CIPublish plans every service folder under opts.CI.Root, then publishes
// each changed one through Publish in dependency order. A failure blocks only
// the services of the run that depend on it, a lost tag race re-plans a
// derived version up to ci.MaxAttempts times, and a pull or merge request
// pipeline is refused before any token is asked for or anything is built.
func (s *Service) CIPublish(ctx context.Context, opts CIPublishOptions) (*ci.Plan, error) {
	refused := ""
	if ci.Detect(opts.CI.Getenv).Base.Value != "" {
		refused = RefusedPullRequest
	}
	o := opts.CI
	if o.Bundles == nil {
		o.Bundles = map[string]*contract.Bundle{}
	}
	o.revision, o.migrations = opts.Revision, map[string]CIMigration{}
	p, _, err := s.CIPlan(ctx, o)
	if err != nil {
		return nil, err
	}
	p.Refused = refused
	images, err := rowImages(p.Rows, opts.Images)
	if err != nil {
		return nil, err
	}
	order, note := publishOrder(p.Rows, o.Bundles)
	if note != "" {
		p.Notes = append(p.Notes, note)
	}
	cp := &ciPublisher{s: s, opts: opts, bundles: o.Bundles, migrations: o.migrations, images: images, refused: refused != ""}
	rows := make([]ci.Row, 0, len(order))
	for _, i := range order {
		r := p.Rows[i]
		cp.row(ctx, &r)
		cp.done = append(cp.done, r)
		rows = append(rows, r)
	}
	p.Rows = rows
	p.SettlePublish(o.Root)
	return p, nil
}

// ciPublisher publishes one run's rows, in order.
type ciPublisher struct {
	s       *Service
	opts    CIPublishOptions
	bundles map[string]*contract.Bundle
	// migrations are the plan's in-memory migrations by row Dir.
	migrations map[string]CIMigration
	images     map[string]string
	refused    bool     // a pull or merge request pipeline: nothing publishes
	done       []ci.Row // the rows decided so far, in order
}

// row decides one row, first rule that applies: undecided by the plan,
// refused run, behind with nothing to derive from, changed and blocked by a
// provider of this run, else through Publish. An unchanged row goes through
// Publish too: plan mode read its baseline without verifying it, and only
// Publish says "already published" once the publisher's signature and the
// deploy artifact are there (or resumes what an earlier run left missing).
// A row decided without Publish keeps the plan-mode note: its version and
// classification were never verified.
func (cp *ciPublisher) row(ctx context.Context, r *ci.Row) {
	switch b := cp.bundles[r.Dir]; {
	case r.Changed != "yes" && r.Changed != "no":
		cp.decide(r, ci.StateFailed, "not published: "+r.Impact.Text)
	case cp.refused:
		cp.decide(r, ci.StateRefused, RefusedPullRequest)
	case !behindOK(b):
		cur := contract.Current()
		why := fmt.Sprintf("declares no publish section; Pacto derives one for pactoVersion %s: set pactoVersion: %q", cur, cur)
		if m := cp.migrations[r.Dir]; m.State == migrationKept {
			// Never advise the raise the plan refused: name why it was refused.
			why = fmt.Sprintf("declares no publish section, and pactoVersion stays at %s (%s): fix that or declare publish", m.From, m.Reason)
		}
		cp.decide(r, ci.StateFailed, why)
	default:
		if why := cp.blockedBy(b); r.Changed == "yes" && why != "" {
			cp.decide(r, ci.StateBlocked, why)
			return
		}
		cp.publish(ctx, r, b)
	}
}

// behindOK reports whether b can publish as its pactoVersion: a current
// contract can, and so can a behind one the plan kept, as written, when it
// declares a publish section. A behind contract with no section cannot be
// derived: that needs the current inferred record.
func behindOK(b *contract.Bundle) bool {
	_, behind := contract.Behind(b.Contract.PactoVersion)
	return !behind || b.Contract.Publish != nil
}

// decide sets r's outcome without publishing and hands it to Record.
func (cp *ciPublisher) decide(r *ci.Row, state, reason string) {
	r.State, r.Reason = state, reason
	if cp.opts.Record != nil {
		cp.opts.Record(*r, nil, nil)
	}
}

// publish publishes r's folder through Publish, the one engine pacto publish
// runs too, re-planning a derived version that lost a tag race.
func (cp *ciPublisher) publish(ctx context.Context, r *ci.Row, b *contract.Bundle) {
	opts := PublishOptions{Path: filepath.Join(cp.opts.CI.Root, filepath.FromSlash(r.Dir)), Revision: cp.opts.Revision,
		Image: cp.images[r.Service], Getenv: cp.opts.CI.Getenv}
	if v := b.Contract.Service.Version; r.Changed == "no" && v != "" && r.Baseline == v {
		opts.unchanged = r.BaselineDigest // the declared tag the plan judged
	}
	if m := cp.migrations[r.Dir]; m.State == migrationMigrated && b.Contract.PactoVersion == m.To {
		opts.Bundle = b // the raised bytes the plan judged, never the folder as written
	}
	if b.Contract.Publish == nil {
		opts.Registry, opts.SignKey, opts.NoTransparencyLog = cp.opts.CI.Registry, cp.opts.SignKey, cp.opts.NoTransparencyLog
	}
	derived := b.Contract.Service.Version == ""
	var res *publish.Result
	var err error
	for r.Attempts = 1; ; r.Attempts++ {
		res, err = cp.s.Publish(ctx, opts)
		if !derived || r.Attempts == ci.MaxAttempts || !lostRace(res, err) {
			break
		}
		r.Notes = append(r.Notes, r.Service+" "+res.Plan.Version+" published by another run; re-planned")
	}
	fillRow(r, b, res, err)
	if cp.opts.Record != nil {
		cp.opts.Record(*r, res, err)
	}
}

// lostRace reports whether a publish lost its version to a concurrent run:
// a tag that changed under it, or a bundle tag that appeared since the
// version was derived. A tag held by another identity is never a race.
func lostRace(res *publish.Result, err error) bool {
	if errors.Is(err, publish.ErrTagRace) {
		return true
	}
	if !errors.Is(err, publish.ErrNotReady) {
		return false
	}
	items := res.Plan.Items
	last := items[len(items)-1] // the bundle is always the last item
	return last.State == publish.Conflict && !errors.Is(last.Err, publish.ErrForeignSignature)
}

// fillRow sets r from what Publish did: Publish verified the baseline, so
// the plan-mode note goes, and the baseline and classification are the ones
// Publish measured the version from.
func fillRow(r *ci.Row, b *contract.Bundle, res *publish.Result, err error) {
	if res != nil {
		r.Notes = slices.DeleteFunc(r.Notes, func(n string) bool { return n == planModeNote })
		if err == nil && len(res.Actions) > 0 {
			for i, n := range r.Notes {
				if strings.HasSuffix(n, migratedNote) {
					r.Notes[i] = strings.Replace(n, ", publishes as ", ", published as ", 1)
				}
			}
		}
		r.Notes = append(r.Notes, res.Notes...)
		r.To = res.Plan.Version
		r.Baseline, r.Classification = cmp.Or(res.Plan.Baseline, r.Baseline), res.Plan.Classification
	}
	switch {
	case errors.Is(err, publish.ErrNotReady):
		r.State, r.Reason = ci.StateRefused, err.Error()
	case err != nil:
		r.State, r.Reason = ci.StateFailed, err.Error()
	case len(res.Actions) == 0:
		r.State, r.Reason = ci.StateUnchanged, "already published"
	default:
		r.State, r.Identity, r.Tool = ci.StatePublished, res.Signer, toolOf(b, res.Plan)
		bundle := res.Plan.Items[len(res.Plan.Items)-1]
		r.Published = bundle.Ref + "@" + res.Digests[publish.BundleItem]
		r.Images = publish.ImageRefs(res.Images)
	}
}

// toolOf is the tool that built the image: the detected one a derived
// section names in its build check, else docker buildx or the program of
// the first declared image build; "" when no image was built in this run
// (none was absent: an earlier run pushed it, or it is supplied).
func toolOf(b *contract.Bundle, p publish.Plan) string {
	if !slices.ContainsFunc(p.Items, func(it publish.Item) bool { return it.Type == contract.ArtifactImage && it.State == publish.Absent }) {
		return ""
	}
	for _, c := range p.Checks {
		if tool, _, ok := strings.Cut(c.Message, " ("); c.Name == "build" && ok {
			return tool
		}
	}
	if pub := b.Contract.Publish; pub != nil {
		for _, a := range pub.Artifacts {
			switch {
			case a.Type != contract.ArtifactImage:
			case a.Build.Dockerfile != nil:
				return "docker buildx"
			case len(a.Build.Command) > 0:
				return a.Build.Command[0]
			}
		}
	}
	return ""
}

// blockedBy is why a service is not published in this run, "" when nothing
// blocks it: a provider of this run that failed or was blocked, or one whose
// BREAKING change its range does not accept. An unknown range never accepts.
// Services outside the run are left to impact and delivery gates.
func (cp *ciPublisher) blockedBy(b *contract.Bundle) string {
	for _, d := range b.Contract.Dependencies {
		p, ok := providerOf(cp.done, d)
		if !ok {
			continue
		}
		at := strings.TrimSpace(p.Service + " " + cmp.Or(p.To, p.Next))
		switch {
		case p.State != ci.StatePublished && p.State != ci.StateUnchanged:
			return "depends on " + at + ", which failed to publish"
		case p.State == ci.StateUnchanged || p.Classification != "BREAKING":
		case d.Compatibility == contract.CompatibilityUnknown:
			return "depends on " + at + ", which is BREAKING; its range is unknown"
		case !acceptsVersion(d.Compatibility, p.To):
			return "depends on " + at + ", which is BREAKING for it"
		}
	}
	return ""
}

// acceptsVersion reports whether the declared range accepts v; no range
// accepts every version, a range that does not parse none.
func acceptsVersion(compatibility, v string) bool {
	if compatibility == "" {
		return true
	}
	r, err := contract.ParseRange(compatibility)
	return err == nil && r.Contains(v)
}

// providerOf is the row of rows the dependency d names: by its bundle
// repository when d has a ref, else by service name. A row the plan gave no
// repository (invalid, say) is matched by name, so its dependents are still
// held when it fails.
func providerOf(rows []ci.Row, d contract.Dependency) (ci.Row, bool) {
	repo := refRepo(contract.TrimOCIScheme(d.Ref))
	for _, r := range rows {
		published, _, _ := strings.Cut(r.Published, "@")
		switch {
		case (d.Ref == "" || r.Repository == "") && r.Service == d.Name && r.Service != "":
		case d.Ref != "" && (repo == r.Repository || repo == refRepo(published)):
		default:
			continue
		}
		return r, true
	}
	return ci.Row{}, false
}

// publishOrder is the order rows publish in: every provider before the
// services of the run that depend on it (Kahn's algorithm), ties by service
// name. A cycle publishes in name order, with the note saying so.
func publishOrder(rows []ci.Row, bundles map[string]*contract.Bundle) ([]int, string) {
	n := len(rows)
	indeg, out := make([]int, n), make([][]int, n)
	for i, r := range rows {
		b := bundles[r.Dir]
		if b == nil {
			continue
		}
		for _, d := range b.Contract.Dependencies {
			if j := providerIndex(rows, d); j >= 0 && j != i {
				out[j] = append(out[j], i)
				indeg[i]++
			}
		}
	}
	byName := func(a, b int) int {
		return cmp.Or(cmp.Compare(rows[a].Service, rows[b].Service), cmp.Compare(rows[a].Dir, rows[b].Dir))
	}
	done := make([]bool, n)
	var order []int
	for len(order) < n {
		var ready, rest []int
		for i := range n {
			switch {
			case done[i]:
			case indeg[i] == 0:
				ready = append(ready, i)
			default:
				rest = append(rest, i)
			}
		}
		if len(ready) == 0 {
			slices.SortFunc(rest, byName)
			names := make([]string, len(rest))
			for k, i := range rest {
				names[k] = rows[i].Service
			}
			return append(order, rest...), "dependency cycle: " + strings.Join(names, ", ") + " (published in name order)"
		}
		next := slices.MinFunc(ready, byName)
		done[next] = true
		order = append(order, next)
		for _, i := range out[next] {
			indeg[i]--
		}
	}
	return order, ""
}

// providerIndex is the index of the row d names, -1 for none.
func providerIndex(rows []ci.Row, d contract.Dependency) int {
	p, ok := providerOf(rows, d)
	if !ok {
		return -1
	}
	return slices.IndexFunc(rows, func(r ci.Row) bool { return r.Dir == p.Dir })
}

// rowImages maps each changed service to the image --image names for it. An
// --image with no service needs exactly one changed service; one naming a
// service that is not changed in this run is refused.
func rowImages(rows []ci.Row, images map[string]string) (map[string]string, error) {
	var changed []string
	for _, r := range rows {
		if r.Changed == "yes" {
			changed = append(changed, r.Service)
		}
	}
	out := map[string]string{}
	for _, svc := range slices.Sorted(maps.Keys(images)) { // "" first
		ref := images[svc]
		switch {
		case svc == "" && len(changed) != 1:
			return nil, fmt.Errorf("--image %s names no service, so it needs exactly one changed service; %d changed: pass --image <service>=%s", ref, len(changed), ref)
		case svc == "":
			out[changed[0]] = ref
		case out[svc] != "": // the bare image, sorted first, took the one changed service
			return nil, fmt.Errorf("--image %s=%s and --image %s both name %s: pass one", svc, ref, images[""], svc)
		case !slices.Contains(changed, svc):
			return nil, fmt.Errorf("--image %s=%s names a service that is not changed in this run", svc, ref)
		default:
			out[svc] = ref
		}
	}
	return out, nil
}
