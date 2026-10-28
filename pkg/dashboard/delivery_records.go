package dashboard

import (
	"context"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// ── blame, undo and refused publishes ──────────────────────────────────

type blameInput struct {
	Namespace string `query:"namespace" required:"true" doc:"The Stage's namespace"`
	Stage     string `query:"stage" required:"true" doc:"The Stage's name"`
	Digest    string `query:"digest" doc:"The digest to blame; empty means the one the Stage runs"`
}

type blameOutput struct {
	Body struct {
		delivery.Blame
		Labels
	}
}

func (s *Server) deliveryBlame(ctx context.Context, in *blameInput) (*blameOutput, error) {
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	b, err := api.Blame(ctx, in.Namespace, in.Stage, in.Digest)
	if err != nil {
		return nil, err
	}
	out := &blameOutput{}
	out.Body.Blame = *b
	digests, people := []string{b.Digest, b.Stage.Pointer}, []string{b.OnBehalfOf}
	if c := b.Change; c != nil {
		digests, people = append(digests, c.From, c.To), append(people, c.Actor)
	}
	for _, a := range b.Approvals {
		people = append(people, a.By)
	}
	out.Body.Versions = stageVersions(ctx, api, in.Namespace, in.Stage, digests...)
	out.Body.People = s.named(people...)
	return out, nil
}

type undoInput struct {
	Namespace string `query:"namespace" required:"true" doc:"The operation's namespace"`
	Name      string `query:"name" required:"true" doc:"The operation to undo"`
	Stage     string `query:"stage" doc:"Its Stage, to find it in the records when it is gone from the cluster"`
}

type undoOutput struct {
	Body struct {
		delivery.Undo
		Labels
	}
}

func (s *Server) deliveryUndo(ctx context.Context, in *undoInput) (*undoOutput, error) {
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	u, err := api.Undo(ctx, in.Namespace, in.Name, in.Stage)
	if err != nil {
		return nil, err
	}
	out := &undoOutput{}
	out.Body.Undo = *u
	if p := u.Plan; p != nil && in.Stage != "" {
		out.Body.Versions = stageVersions(ctx, api, in.Namespace, in.Stage, p.From, p.To)
	}
	return out, nil
}

// PublishRefusal is one publish Pacto refused.
type PublishRefusal struct {
	Service string `json:"service"`
	Version string `json:"version" doc:"The version the publish asked for"`
	// Check is the check that refused it: version (the version rule), sign,
	// lock, validation or conflict.
	Check  string `json:"check" doc:"The check that refused it: version, sign, lock, validation or conflict"`
	Reason string `json:"reason" doc:"Why, in one sentence"`
	// Baseline and RequiredBump are set when the version rule refused it.
	Baseline     string `json:"baseline,omitempty" doc:"The highest published version below it, the one the change was compared with"`
	RequiredBump string `json:"requiredBump,omitempty" enum:"major,minor,patch" doc:"The smallest bump the change needs"`
	Actor        string `json:"actor,omitempty" doc:"Who ran the publish, as the cluster or the record names them"`
	At           string `json:"at" doc:"When it was refused (RFC 3339)"`
	// Verified: a signed record of the refusal was checked against the trust
	// config.
	Verified bool `json:"verified" doc:"A signed record of the refusal was verified"`
}

// PublishRefusals is what a host knows of refused publishes.
type PublishRefusals struct {
	Completeness string           `json:"completeness" enum:"complete,partial"`
	Note         string           `json:"note,omitempty" doc:"Where the refusals were read and what may be missing"`
	Unreadable   []string         `json:"unreadable,omitempty" doc:"What could not be read, and why"`
	Refusals     []PublishRefusal `json:"refusals"`
}

type refusalsOutput struct {
	Body struct {
		AsOf string `json:"asOf"`
		// Recorded: this host keeps a record of refused publishes.
		Recorded bool `json:"recorded" doc:"This cluster keeps a record of refused publishes; false means an empty list proves nothing"`
		PublishRefusals
		Total int `json:"total" doc:"How many refusals were read; refusals holds the newest 20"`
	}
}

// recentRefusals is how many refusals the dashboard shows.
const recentRefusals = 20

func (s *Server) deliveryRefusals(ctx context.Context, _ *struct{}) (*refusalsOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	r, err := api.PublishRefusals(ctx, s.opts.DeliveryNamespace)
	if err != nil {
		return nil, err
	}
	out := &refusalsOutput{}
	out.Body.AsOf = time.Now().UTC().Format(time.RFC3339)
	if r == nil {
		out.Body.Completeness, out.Body.Refusals = "partial", []PublishRefusal{}
		out.Body.Note = "this cluster keeps no record of refused publishes: a refusal is shown only to whoever ran pacto publish"
		return out, nil
	}
	out.Body.Recorded, out.Body.PublishRefusals = true, *r
	slices.SortFunc(out.Body.Refusals, func(a, b PublishRefusal) int { return refusedAt(b.At).Compare(refusedAt(a.At)) })
	out.Body.Total = len(out.Body.Refusals)
	out.Body.Refusals = out.Body.Refusals[:min(len(out.Body.Refusals), recentRefusals)]
	if out.Body.Refusals == nil {
		out.Body.Refusals = []PublishRefusal{}
	}
	return out, nil
}

// refusedAt reads a refusal time. Strings do not sort as times once offsets
// or fractional seconds differ, and an unreadable time sorts as the oldest.
func refusedAt(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
