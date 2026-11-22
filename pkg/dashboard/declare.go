package dashboard

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Declarer plans and carries out declaring an inferred service for the
// person behind a request: the host reads the cluster as them, signs with
// their fresh login or saves a draft, and writes only into the service's
// bundle repository. The route has already resolved the workload in the
// viewer's snapshot and refused callers with no identity.
type Declarer interface {
	// Plan is what declaring w would publish, or accepting in.FromDraft.
	Plan(r *http.Request, w DeclareWorkload, in DeclareInput) (*DeclareAnswer, error)
	// Accept signs the planned declaration or saves it as a draft.
	Accept(r *http.Request, w DeclareWorkload, in DeclareAcceptInput) (*DeclareAcceptAnswer, error)
	// CanWrite reports whether the host can write at all: a local host, or
	// a shared one with a push credential of its own.
	CanWrite() bool
	// AcceptOffer is what the person behind r can do here with the accept
	// plan whose intent digest is intent: sign, save a draft, or neither.
	AcceptOffer(r *http.Request, intent string) AcceptExecuteOffer
	// AcceptRanges signs the accept plan the person reviewed, saves it as an
	// acceptance draft or accepts a draft, with the same signer, review and
	// write rules as a declaration.
	AcceptRanges(r *http.Request, t AcceptTarget, in AcceptExecuteInput) (*AcceptExecuteAnswer, error)
}

// DeclareWorkload is the inferred service a declaration is for, as the
// viewer's snapshot names it.
type DeclareWorkload struct {
	Key       string
	Name      string
	Cluster   string
	Namespace string
	Kind      string
	// Repositories and Image are the workload as the viewer's restricted
	// snapshot shows it: the plan names its repository from them alone.
	Repositories []fleet.InferredRepository
	Image        string
}

// DraftRef names one draft by its tag and the digest the person was shown.
type DraftRef struct {
	Tag    string `json:"tag" pattern:"^draft-[0-9]+$" maxLength:"32" doc:"The draft tag, draft-<n>"`
	Digest string `json:"digest" pattern:"^sha256:[a-f0-9]{64}$" doc:"The digest of the draft the plan showed"`
}

// DeclareInput asks for a declaration plan.
type DeclareInput struct {
	ServiceKey string    `json:"serviceKey" minLength:"1" doc:"The inferred service to declare"`
	Accept     []string  `json:"accept,omitempty" maxItems:"64" doc:"The dependencies whose proposed range the declaration accepts"`
	FromDraft  *DraftRef `json:"fromDraft,omitempty" doc:"Plan accepting this draft instead: its own texts, what nothing backs and an intent digest that covers the draft digest"`
	// IntentDigest names the plan a signing login was started for.
	IntentDigest string `json:"intentDigest,omitempty" doc:"The intent a signing login was started for: back from that login, the accepted ranges are the ones it was started with, whatever accept says"`
}

// DeclareAcceptInput carries out a plan the person reviewed.
type DeclareAcceptInput struct {
	ServiceKey   string    `json:"serviceKey" minLength:"1" doc:"The inferred service to declare"`
	Accept       []string  `json:"accept,omitempty" maxItems:"64" doc:"The dependencies whose proposed range the declaration accepts"`
	IntentDigest string    `json:"intentDigest" minLength:"1" doc:"The intent digest of the plan the person reviewed: a plan that moved since is refused with PlanStale"`
	Mode         string    `json:"mode" enum:"sign,draft" doc:"sign: publish the declared version signed by the person's fresh login; draft: save a draft-<n> a person accepts later"`
	FromDraft    *DraftRef `json:"fromDraft,omitempty" doc:"Accept this draft: its tag must still name the digest the plan showed"`
}

// DeclareText is one file of a declaration, at most MaxDeclareText bytes.
type DeclareText struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty" doc:"The file is longer than 64 KiB and only its start is shown"`
}

// MaxDeclareText bounds each file a declaration plan shows.
const MaxDeclareText = 64 << 10

// NewDeclareText is data as a shown text, cut at MaxDeclareText.
func NewDeclareText(data []byte) DeclareText {
	if len(data) > MaxDeclareText {
		return DeclareText{Text: string(data[:MaxDeclareText]), Truncated: true}
	}
	return DeclareText{Text: string(data)}
}

// DeclareRange is one dependency of a declaration.
type DeclareRange struct {
	Dependency    string `json:"dependency"`
	Compatibility string `json:"compatibility" doc:"The declared range; unknown unless a person accepts the proposal"`
	Proposed      string `json:"proposed,omitempty" doc:"The range the cluster read proposes"`
	Observed      string `json:"observed,omitempty" doc:"The versions observed"`
	Accepted      bool   `json:"accepted,omitempty"`
}

// DeclareSigner is who signs a declaration.
type DeclareSigner struct {
	Subject string `json:"subject,omitempty"`
	Issuer  string `json:"issuer,omitempty"`
	Class   string `json:"class"`
	Source  string `json:"source" doc:"key, ambient, login or none"`
	Why     string `json:"why,omitempty"`
}

// DeclareBaseline is the published version a declaration starts from.
type DeclareBaseline struct {
	Version string `json:"version"`
	Bundle  string `json:"bundle"`
	Signer  string `json:"signer,omitempty"`
}

// DeclarePlan is what a declaration would publish, signed by whom, as
// pacto declare plans it.
type DeclarePlan struct {
	Service           string                             `json:"service"`
	Version           string                             `json:"version"`
	BundleRepo        string                             `json:"bundleRepo"`
	State             string                             `json:"state" doc:"declared for a person's login, else draft"`
	InferredFrom      string                             `json:"inferredFrom"`
	Cluster           string                             `json:"cluster,omitempty"`
	Baseline          *DeclareBaseline                   `json:"baseline,omitempty"`
	Deploy            string                             `json:"deploy" doc:"The baseline deploy artifact the version reuses, or none"`
	Ranges            []DeclareRange                     `json:"ranges"`
	Owner             string                             `json:"owner,omitempty"`
	MayAccept         []string                           `json:"mayAccept,omitempty" doc:"Who may set the owner this plan changes"`
	Signer            DeclareSigner                      `json:"signer"`
	PolicyNote        string                             `json:"policyNote,omitempty"`
	Coverage          []contract.InferredCoverage        `json:"coverage,omitempty"`
	Hidden            *contract.InferredHidden           `json:"hidden,omitempty" doc:"The called Services and callers the person's own credentials could not read: left out of the ranges"`
	ClusterAsOf       string                             `json:"clusterAsOf,omitempty" doc:"When the cluster read the plan comes from was taken"`
	Skipped           []DeclareSkipped                   `json:"skipped,omitempty" doc:"The published versions never taken as the baseline, and why"`
	IntentDigest      string                             `json:"intentDigest"`
	Unchanged         string                             `json:"unchanged,omitempty" doc:"The baseline version when nothing changed since it: nothing is written"`
	Notes             []string                           `json:"notes,omitempty"`
	Unbacked          []string                           `json:"unbacked,omitempty" doc:"Declared changes of an accepted draft that neither the baseline, the cluster read nor an accepted range backs"`
	UnbackedUnchecked string                             `json:"unbackedUnchecked,omitempty" doc:"Why what backs the draft's changes could not be checked"`
	Contract          DeclareText                        `json:"contract" doc:"pacto.yaml as it would be published"`
	Record            DeclareText                        `json:"record" doc:"pacto.inferred.yaml as it would be published"`
	Repository        *contract.InferredSourceRepository `json:"repository,omitempty" doc:"Where the service's source lives and how Pacto found it; absent when no source named it"`
	RepositoryTried   []DeclareRungNote                  `json:"repositoryTried,omitempty" doc:"Why each source Pacto tried for the repository gave nothing"`
}

// DeclareRungNote is why one source of the repository gave nothing.
type DeclareRungNote struct {
	Rung   string `json:"rung" doc:"signed-provenance, gitops, image-label, repository-map or given"`
	Reason string `json:"reason"`
}

// DeclareSkipped is a published version the plan did not take as its
// baseline.
type DeclareSkipped struct {
	Tag    string `json:"tag"`
	Signer string `json:"signer,omitempty"`
	Why    string `json:"why"`
}

// DeclareDraft is one draft-<n> of the service's bundle repository.
type DeclareDraft struct {
	Tag        string                  `json:"tag"`
	Digest     string                  `json:"digest,omitempty"`
	State      string                  `json:"state" doc:"draft, or unreadable with the reason"`
	Asker      contract.InferredSigner `json:"asker" doc:"Who wrote it; an unsigned draft names nobody and its on-behalf-of person is asserted"`
	OnBehalfOf string                  `json:"onBehalfOf,omitempty"`
	Requested  []string                `json:"requested,omitempty" doc:"The ranges it requests, as <dependency> <range>"`
	Signed     bool                    `json:"signed"`
	Reason     string                  `json:"reason,omitempty"`
}

// DeclareSigning says whether this person can sign here.
type DeclareSigning struct {
	Mode     string `json:"mode" enum:"fresh-login,none" doc:"fresh-login: the person signs with a fresh login at the identity provider; none: the person saves a draft or runs the command"`
	Identity string `json:"identity,omitempty" doc:"Who the fresh login signs as"`
	Reason   string `json:"reason,omitempty" doc:"Why the person cannot sign here"`
}

// DeclareWrite says whether this host can write a draft or a version.
type DeclareWrite struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// DeclareRefusal is a declaration that was refused, with the command a person
// runs about it.
type DeclareRefusal struct {
	Code    string `json:"code" enum:"PlanStale,IntentRequired,WorkloadNotFound,WorkloadAmbiguous,AlreadyDeclared,RangeNotProposed,NoRegistry,RegistryUnreadable,VersionConflict,SignerNotAllowed,SignerUnverified,DraftNotFound,ExecuteNeedsStdio,BaselineNeedsMigration,BundleRepoMismatch,AcceptNothingProposed,FieldNotAcceptable,BreakNotAnnounced,NotPublished,PersonSigningUnavailable,OWNER_SIGNER_NOT_PERSON,OWNER_SIGNER_NOT_ALLOWED,OWNER_POLICY_UNVERIFIED,OWNER_SIGNER_UNVERIFIED,IdentityRequired,SignInLinkReadsFleet,ReviewFailed,ClusterNotReviewable,NotAllowed" doc:"The pacto declare refusal codes, plus the dashboard's own: IdentityRequired, SignInLinkReadsFleet, ReviewFailed, ClusterNotReviewable and NotAllowed"`
	Message string `json:"message"`
	Command string `json:"command,omitempty" doc:"The command a person runs about it"`
}

// DeclareAnswer is a declaration plan with the drafts waiting and what this
// person can do with it here.
type DeclareAnswer struct {
	Plan        *DeclarePlan    `json:"plan,omitempty" doc:"Absent when the plan was refused before it was built"`
	Drafts      []DeclareDraft  `json:"drafts,omitempty" doc:"The service's drafts, newest first, at most 20"`
	DraftsTotal int             `json:"draftsTotal,omitempty"`
	Signing     DeclareSigning  `json:"signing"`
	Write       DeclareWrite    `json:"write"`
	Command     string          `json:"command,omitempty" doc:"The exact pacto declare command a person runs in a terminal"`
	PRCLI       string          `json:"prCli,omitempty" doc:"The pacto declare command that opens the repository pull request with the person's own Git token; the dashboard never opens it"`
	SignTicket  string          `json:"signTicket,omitempty" doc:"Starts the signing login once: /auth/sign?ticket=<signTicket>&return=<page>; present only with signing mode fresh-login"`
	SignedIn    bool            `json:"signedIn,omitempty" doc:"Back from a signing login: the login for the intentDigest asked about is still live, unused and unexpired"`
	Refusal     *DeclareRefusal `json:"refusal,omitempty"`
}

// DeclareAcceptAnswer is what accepting a plan wrote, or why nothing was.
type DeclareAcceptAnswer struct {
	Published string          `json:"published,omitempty" doc:"The bundle written, as <repository>@sha256:<hex>"`
	Tag       string          `json:"tag,omitempty" doc:"The version or draft-<n> tag written"`
	Draft     bool            `json:"draft,omitempty"`
	Signed    bool            `json:"signed,omitempty"`
	Command   string          `json:"command,omitempty" doc:"The command a person accepts a draft with"`
	Refusal   *DeclareRefusal `json:"refusal,omitempty"`
	Plan      *DeclareAnswer  `json:"plan,omitempty" doc:"The fresh plan of a PlanStale refusal"`
}

type declarePlanInput struct{ Body DeclareInput }
type declarePlanOutput struct{ Body DeclareAnswer }
type declareAcceptInput struct{ Body DeclareAcceptInput }
type declareAcceptOutput struct{ Body DeclareAcceptAnswer }

// registerDeclareOperations registers the plan route with a declarer, and
// the accept route only when it can also write.
func (s *Server) registerDeclareOperations(api huma.API) {
	d := s.opts.Declare
	if d == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "fleet-declare",
		Method:      http.MethodPost,
		Path:        "/api/fleet/declare",
		Summary:     "Plan declaring an inferred service",
		Description: "Plans what pacto declare would publish for an inferred service, read with the person's own cluster " +
			"credentials: pacto.yaml, pacto.inferred.yaml, the version, the proposed ranges, the drafts waiting and whether " +
			"the person can sign here. It writes nothing. Signed-in and local viewers only.",
		Tags: []string{"Fleet"},
	}, func(ctx context.Context, in *declarePlanInput) (*declarePlanOutput, error) {
		w, err := s.declareWorkload(ctx, in.Body.ServiceKey)
		if err != nil {
			return nil, err
		}
		ans, err := d.Plan(requestFrom(ctx), w, in.Body)
		if err != nil {
			return nil, err
		}
		return &declarePlanOutput{Body: *ans}, nil
	})
	if !d.CanWrite() {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "fleet-declare-accept",
		Method:      http.MethodPost,
		Path:        "/api/fleet/declare/accept",
		Summary:     "Sign a declaration or save it as a draft",
		Description: "Carries out a declaration plan the person reviewed, by its intent digest: publishes the declared version " +
			"signed by the person's fresh login, saves a draft-<n> a person accepts later, or accepts a draft. It needs update " +
			"on the workload and writes only into the service's bundle repository.",
		Tags: []string{"Fleet"},
	}, func(ctx context.Context, in *declareAcceptInput) (*declareAcceptOutput, error) {
		w, err := s.declareWorkload(ctx, in.Body.ServiceKey)
		if err != nil {
			return nil, err
		}
		ans, err := d.Accept(requestFrom(ctx), w, in.Body)
		if err != nil {
			return nil, err
		}
		return &declareAcceptOutput{Body: *ans}, nil
	})
}

// declares reports whether the caller can be identified for a declaration:
// a local viewer or a signed-in person.
func (s *Server) declares(ctx context.Context) bool {
	a := s.opts.Auth
	return a != nil && (a.Local || s.authState(ctx).SignedIn)
}

// declareIdentity refuses a caller who cannot be identified or whose
// session came from a sign-in link: a write names its actor.
func (s *Server) declareIdentity(ctx context.Context) error {
	switch {
	case !s.declares(ctx):
		return Refuse(http.StatusForbidden, "IdentityRequired",
			"sign in to declare a service: Pacto reads the cluster and writes as you")
	case s.authState(ctx).FleetOnly:
		return Refuse(http.StatusForbidden, "SignInLinkReadsFleet",
			"a sign-in link reads the fleet only: sign in with your identity provider or paste your own token to declare a service")
	}
	return nil
}

// declareWorkload is the inferred service key names in the viewer's
// snapshot, after the caller's identity is checked: a hidden service and a
// declared one answer as a missing one.
func (s *Server) declareWorkload(ctx context.Context, key string) (DeclareWorkload, error) {
	if err := s.declareIdentity(ctx); err != nil {
		return DeclareWorkload{}, err
	}
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return DeclareWorkload{}, err
	}
	rec := q.SnapshotForSerialization().Services[fleet.ServiceKey(key)]
	if rec == nil || !rec.Inferred() || rec.Workload == nil {
		return DeclareWorkload{}, huma.Error404NotFound("no inferred service with this serviceKey")
	}
	wl := rec.Workload
	out := DeclareWorkload{Key: key, Name: wl.Name, Cluster: wl.Cluster, Namespace: wl.Namespace, Kind: wl.Kind, Repositories: wl.Repositories}
	if wl.Image != nil {
		out.Image = wl.Image.Value
	}
	return out, nil
}
