package delivery

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// A member request is a person's signed ask, pushed by a hub to a member's
// request stream, that the member raise one promote; a request outcome is
// the member's signed answer to it. Neither is a contract format.
const (
	PredicateOperationRequest = "delivery.pacto.trianalab.io/operation-request/v1"
	PredicateRequestOutcome   = "delivery.pacto.trianalab.io/operation-request-outcome/v1"
)

// Member request bounds: the requests a member reads per poll, the outcomes
// read per request, the longest lifetime a request may claim and how far
// the time it claims may be from its verified signing time.
const (
	MaxOperationRequests = 50
	MaxRequestOutcomes   = 150
	MaxRequestLifetime   = 7 * 24 * time.Hour
	MaxRequestClockSkew  = 5 * time.Minute
)

// ClusterRequestStream is the record stream a member's requests and their
// outcomes sit on, beside its join stream.
func ClusterRequestStream(uid string) string {
	return uid + "/" + TargetSnapshotRepository + "/requests"
}

// The placement sources a request may carry.
const (
	RequestPlacedAdvertised = "advertised"
	RequestPlacedPolicy     = "policy"
)

// RequestPlacement is the placement the person confirmed by signing: the
// members it named, where it came from (advertised environments or a
// signed placement policy, by digest) and who confirmed it.
type RequestPlacement struct {
	Clusters    []string `json:"clusters,omitempty"`
	Source      string   `json:"source"`
	Policy      string   `json:"policy,omitempty"`
	ConfirmedBy string   `json:"confirmedBy,omitempty"`
}

// OperationRequest asks member Cluster, through hub Hub, to promote Service
// in Environment to Digest on Requester's behalf. At and Expires are what
// the signer wrote (RFC 3339): a member bounds them by the verified signing
// time (Timely).
type OperationRequest struct {
	Cluster       string           `json:"cluster"`
	Hub           string           `json:"hub"`
	Service       ServiceRef       `json:"service"`
	Environment   string           `json:"environment"`
	Version       string           `json:"version,omitempty"`
	Digest        string           `json:"digest"`
	Reason        string           `json:"reason,omitempty"`
	CorrelationID string           `json:"correlationID,omitempty"`
	Requester     string           `json:"requester"`
	Placement     RequestPlacement `json:"placement"`
	At            string           `json:"at"`
	Expires       string           `json:"expires"`
}

// Check refuses a request a member could not act on: a cluster that cannot
// sit in a tag, no hub, requester or service, an environment that is not a
// DNS label, a digest that is not a sha256 manifest digest, a bad
// correlation id, times that are not RFC 3339, an expiry before At or more
// than MaxRequestLifetime after it, an unknown placement source and an
// advertised placement confirmed by someone other than the requester.
func (r OperationRequest) Check() error {
	if _, err := TargetSnapshotTag(r.Cluster); err != nil {
		return err
	}
	switch {
	case r.Hub == "":
		return errors.New("no hub")
	case r.Requester == "":
		return errors.New("no requester")
	case r.Service.Name == "":
		return errors.New("no service")
	case !dnsLabel(r.Environment):
		return fmt.Errorf("environment %q is not a DNS label", r.Environment)
	case !strings.HasPrefix(r.Digest, "sha256:") || !isDigest(r.Digest):
		return fmt.Errorf("digest %q is not a sha256 manifest digest", r.Digest)
	}
	if err := r.checkPlacement(); err != nil {
		return err
	}
	if err := CheckCorrelationID(r.CorrelationID); err != nil {
		return err
	}
	at, expires, err := r.times()
	if err != nil {
		return err
	}
	if expires.Before(at) || expires.Sub(at) > MaxRequestLifetime {
		return fmt.Errorf("expires %s: want after at %s and at most %s later", r.Expires, r.At, MaxRequestLifetime)
	}
	return nil
}

// checkPlacement refuses an unknown placement source and an advertised
// placement confirmed by someone other than the requester.
func (r OperationRequest) checkPlacement() error {
	switch {
	case r.Placement.Source != RequestPlacedAdvertised && r.Placement.Source != RequestPlacedPolicy:
		return fmt.Errorf("placement source %q: want %s or %s", r.Placement.Source, RequestPlacedAdvertised, RequestPlacedPolicy)
	case r.Placement.Source == RequestPlacedAdvertised && r.Placement.ConfirmedBy != r.Requester:
		return fmt.Errorf("an advertised placement is confirmed by the requester %s, not %q", r.Requester, r.Placement.ConfirmedBy)
	}
	return nil
}

// times parses At and Expires.
func (r OperationRequest) times() (at, expires time.Time, err error) {
	if at, err = time.Parse(time.RFC3339, r.At); err != nil {
		return at, expires, fmt.Errorf("at %q: want RFC 3339", r.At)
	}
	if expires, err = time.Parse(time.RFC3339, r.Expires); err != nil {
		return at, expires, fmt.Errorf("expires %q: want RFC 3339", r.Expires)
	}
	return at, expires, nil
}

// Timely says why a request whose signature verified at signedAt is too
// late or claims too much at now, "" when it is in time: At more than
// MaxRequestClockSkew from signedAt, Expires later than signedAt plus
// MaxRequestLifetime or ttl (the Operation TTL; 0 adds no bound), or Expires
// past. Check has accepted the request.
func (r OperationRequest) Timely(signedAt, now time.Time, ttl time.Duration) (code, why string) {
	at, expires, _ := r.times()
	life := MaxRequestLifetime
	if ttl > 0 {
		life = min(life, ttl)
	}
	switch {
	case at.Sub(signedAt).Abs() > MaxRequestClockSkew:
		return RefusalRequestExpired, fmt.Sprintf("the request says it was signed at %s, but its signature was made at %s", r.At, signedAt.UTC().Format(time.RFC3339))
	case expires.After(signedAt.Add(life)):
		return RefusalRequestExpired, fmt.Sprintf("the request asks to live until %s, longer than the %s this member allows", r.Expires, life)
	case now.After(expires):
		return RefusalRequestExpired, "the request expired at " + r.Expires
	}
	return "", ""
}

// OperationRequestStatement is an in-toto Statement v1 carrying a request.
type OperationRequestStatement struct {
	Type          string           `json:"_type"`
	Subject       []Subject        `json:"subject"`
	PredicateType string           `json:"predicateType"`
	Predicate     OperationRequest `json:"predicate"`
}

// requestSubject is the subject of a request or outcome on cluster's request
// stream: that stream, by the hex of the digest it is about.
func requestSubject(cluster, digest string) []Subject {
	return []Subject{{Name: ClusterRequestStream(cluster), Digest: map[string]string{"sha256": strings.TrimPrefix(digest, "sha256:")}}}
}

// sameSubject reports whether two subjects name the same thing.
func sameSubject(a, b []Subject) bool {
	return slices.EqualFunc(a, b, func(x, y Subject) bool { return x.Name == y.Name && maps.Equal(x.Digest, y.Digest) })
}

// OpenOperationRequest verifies a request bundle with verify and returns its
// statement, once Check accepts the request and its subject is the request
// stream of its cluster keyed by its digest.
func OpenOperationRequest(bundle []byte, verify func(bundle []byte) error) (OperationRequestStatement, error) {
	var st OperationRequestStatement
	if _, err := openStatement(bundle, PredicateOperationRequest, &st); err != nil {
		return OperationRequestStatement{}, err
	}
	if err := verify(bundle); err != nil {
		return OperationRequestStatement{}, fmt.Errorf("signature: %w", err)
	}
	if err := st.Predicate.Check(); err != nil {
		return OperationRequestStatement{}, err
	}
	if !sameSubject(st.Subject, requestSubject(st.Predicate.Cluster, st.Predicate.Digest)) {
		return OperationRequestStatement{}, errors.New("its subject is not the request stream of its cluster keyed by its digest")
	}
	return st, nil
}

// RequestState is what an outcome says happened to a request.
type RequestState string

const (
	// RequestRefused: the member refused the request at intake.
	RequestRefused RequestState = "refused"
	// RequestRaised: the member raised the Operation.
	RequestRaised RequestState = "raised"
	// RequestConcluded: that Operation concluded.
	RequestConcluded RequestState = "concluded"
)

// RequestedOperation is the Operation a member raised for a request, and
// the intent record a hub person approves it through (empty when the member
// keeps none).
type RequestedOperation struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	UID          string `json:"uid,omitempty"`
	Stage        string `json:"stage,omitempty"`
	Stream       string `json:"stream,omitempty"`
	IntentRecord string `json:"intentRecord,omitempty"`
}

// RequestOutcome is a member's signed answer to one request: Request is the
// request's manifest digest. A refusal carries a code, a reason and the next
// command; a conclusion the Operation's outcome and the pacto.published
// verdict.
type RequestOutcome struct {
	Request   string              `json:"request"`
	Cluster   string              `json:"cluster"`
	State     RequestState        `json:"state"`
	Code      string              `json:"code,omitempty"`
	Reason    string              `json:"reason,omitempty"`
	Next      string              `json:"next,omitempty"`
	Operation *RequestedOperation `json:"operation,omitempty"`
	Outcome   Outcome             `json:"outcome,omitempty"`
	Digest    string              `json:"digest,omitempty"`
	Published string              `json:"published,omitempty"`
	At        string              `json:"at"`
}

// Check refuses an outcome a hub could not read: a cluster that cannot sit
// in a tag, a request that is not a sha256 digest, a time that is not RFC
// 3339, a refusal with no code or next command, a raise with no Operation
// and a conclusion with no Operation or outcome.
func (o RequestOutcome) Check() error {
	if _, err := TargetSnapshotTag(o.Cluster); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, o.At); err != nil {
		return fmt.Errorf("at %q: want RFC 3339", o.At)
	}
	if !strings.HasPrefix(o.Request, "sha256:") || !isDigest(o.Request) {
		return fmt.Errorf("request %q is not a sha256 manifest digest", o.Request)
	}
	return o.checkState()
}

// checkState refuses a refusal with no code or next command, a raise with no
// Operation, a conclusion with no Operation or outcome and an unknown state.
func (o RequestOutcome) checkState() error {
	switch {
	case o.State == RequestRefused && (o.Code == "" || o.Next == ""):
		return errors.New("a refusal needs a code and the next command")
	case o.State == RequestRaised && o.Operation == nil:
		return errors.New("a raise needs its operation")
	case o.State == RequestConcluded && (o.Operation == nil || o.Outcome == ""):
		return errors.New("a conclusion needs its operation and outcome")
	case o.State != RequestRefused && o.State != RequestRaised && o.State != RequestConcluded:
		return fmt.Errorf("state %q: want refused, raised or concluded", o.State)
	}
	return nil
}

// RequestOutcomeStatement is an in-toto Statement v1 carrying an outcome.
type RequestOutcomeStatement struct {
	Type          string         `json:"_type"`
	Subject       []Subject      `json:"subject"`
	PredicateType string         `json:"predicateType"`
	Predicate     RequestOutcome `json:"predicate"`
}

// NewRequestOutcomeStatement builds the statement of o: its subject is its
// cluster's request stream, by the request it answers.
func NewRequestOutcomeStatement(o RequestOutcome) RequestOutcomeStatement {
	return RequestOutcomeStatement{Type: StatementType, Subject: requestSubject(o.Cluster, o.Request),
		PredicateType: PredicateRequestOutcome, Predicate: o}
}

// SignRequestOutcome signs st and returns the bundle.
func SignRequestOutcome(ctx context.Context, s Signer, st RequestOutcomeStatement) ([]byte, error) {
	bundle, _, err := signStatement(ctx, s, st)
	return bundle, err
}

// OpenRequestOutcome decodes an outcome bundle read on member uid's request
// stream and verifies it with a record key of trust scoped to uid; open
// turns one key into the check of a bundle (pkg/signing does). A nil trust
// reads it unverified. An outcome Check refuses, naming another cluster than
// uid or with another subject than its own is an error.
func OpenRequestOutcome(bundle []byte, trust *TrustConfig, uid string, open func(TrustedKey) (func([]byte) error, error)) (RequestOutcomeStatement, error) {
	var st RequestOutcomeStatement
	if _, err := openStatement(bundle, PredicateRequestOutcome, &st); err != nil {
		return RequestOutcomeStatement{}, err
	}
	if err := st.Predicate.Check(); err != nil {
		return RequestOutcomeStatement{}, err
	}
	switch {
	case trust != nil && st.Predicate.Cluster != uid:
		return RequestOutcomeStatement{}, fmt.Errorf("the outcome names cluster %s, not %s", st.Predicate.Cluster, uid)
	case !sameSubject(st.Subject, NewRequestOutcomeStatement(st.Predicate).Subject):
		return RequestOutcomeStatement{}, errors.New("its subject is not the request stream of its cluster keyed by its request")
	case trust == nil:
		return st, nil
	}
	stream := ClusterRequestStream(uid)
	err := fmt.Errorf("no trusted key of cluster %q may sign its request outcomes", uid)
	for _, k := range trust.Keys {
		if k.Role != "" || !scopedTo(k.Streams, uid, stream) {
			continue
		}
		verify, verr := TrustConfig{Keys: []TrustedKey{k}}.Verifier(open)
		if verr == nil {
			verr = verify(stream, 0, bundle)
		}
		if verr == nil {
			return st, nil
		}
		err = verr
	}
	return RequestOutcomeStatement{}, fmt.Errorf("signature: %w", err)
}

// OwnHub is the hub that approved cluster uid's own key keyID, among the
// join records of uid that verified under this cluster's approver signers:
// per hub the newest by verified signing time decides (a removal on a tie),
// and it counts when it approves keyID. No such hub, or more than one, is
// why no hub is named.
func OwnHub(uid, keyID string, verified []VerifiedJoin) (hub, why string) {
	newest := map[string]VerifiedJoin{}
	for _, v := range verified {
		n, seen := newest[v.Record.Hub]
		switch {
		case !v.Verified || v.Record.Cluster != uid:
		case !seen || v.SignedAt.After(n.SignedAt), v.SignedAt.Equal(n.SignedAt) && v.Record.Decision == JoinRemove:
			newest[v.Record.Hub] = v
		}
	}
	var hubs []string
	for h, v := range newest {
		if v.Record.Decision == JoinApprove && v.Record.KeyID == keyID {
			hubs = append(hubs, h)
		}
	}
	slices.Sort(hubs)
	switch len(hubs) {
	case 0:
		return "", "no hub approved this cluster's key by a record its approver signers verify"
	case 1:
		return hubs[0], ""
	}
	return "", "more than one hub approved this cluster (" + strings.Join(hubs, ", ") + ")"
}

// The codes a member refuses a request with at intake, each a signed
// refused outcome with its reason and next command (RefusalNext).
const (
	RefusalNotForThisCluster = "NotForThisCluster"
	RefusalRequestExpired    = "RequestExpired"
	RefusalNoStage           = "NoStage"
	RefusalAmbiguousStage    = "AmbiguousStage"
	RefusalOutsideAllowlist  = "OutsideAllowlist"
	RefusalNotAllowedHere    = "NotAllowedHere"
	RefusalNotAPerson        = "NotAPerson"
	RefusalPointerMoved      = "PointerMoved"
)

// RefusalNext is the next command for a request r refused with code; the
// namespace is the Stage's, for NotAllowedHere.
func RefusalNext(code string, r OperationRequest, namespace string) string {
	asked := fmt.Sprintf("--service %s --environment %s --to %s", r.Service.Name, r.Environment, r.Digest)
	propose := "pacto delivery propose " + asked + " --member " + r.Cluster + " --confirm"
	switch code {
	case RefusalNotForThisCluster:
		return "plan again from the hub, which writes each member's request on its own stream: pacto delivery plan " + asked
	case RefusalNoStage:
		return "create the Stage of " + r.Service.Name + " in " + r.Environment + " on this member, or let its ladder place it, then: " + propose
	case RefusalAmbiguousStage:
		return "keep one Stage of " + r.Service.Name + " in " + r.Environment + " in the namespaces this member takes requests for, then: " + propose
	case RefusalOutsideAllowlist:
		return "a cluster admin of this member widens delivery.remote.allow, or plan for a member that takes it: pacto delivery plan " + asked
	case RefusalNotAllowedHere:
		return "a cluster admin of this member runs: kubectl create rolebinding pacto-propose --clusterrole=edit --user=" + r.Requester + " -n " + namespace
	case RefusalPointerMoved:
		return "the Stage changed after " + r.Requester + " asked; plan again: pacto delivery plan " + asked
	}
	return "a person signs the request again: " + propose // RequestExpired, NotAPerson
}

// RequestAllowlist narrows what a member takes requests for (the chart's
// delivery.remote.allow): service name globs (path.Match), namespaces and
// environments. Empty lists narrow nothing.
type RequestAllowlist struct {
	Services     []string
	Namespaces   []string
	Environments []string
}

// Hosted are the environments a member takes requests for: those its name
// advertises (advertised is AdvertisedEnvironments over the name alone, as
// for its managed Stages), canonicalized, kept to Environments when set.
// nil means every environment: the name advertises none and Environments is
// empty.
func (a RequestAllowlist) Hosted(advertised []AdvertisedEnvironment) []string {
	var named []string
	for _, e := range advertised {
		named = append(named, canonicalEnvironment(e.Name))
	}
	if len(a.Environments) == 0 {
		slices.Sort(named)
		return slices.Compact(named)
	}
	out := []string{} // non-nil: nothing hosted is not every environment
	for _, e := range a.Environments {
		if c := canonicalEnvironment(e); len(named) == 0 || slices.Contains(named, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Refusal says why a request for service in namespace and environment is
// outside the allowlist, "" when it is inside: a namespace the member does
// not observe (observed empty observes every one) or Namespaces leaves out,
// an environment Hosted leaves out, or a service no Services glob matches.
// The narrowing values only narrow.
func (a RequestAllowlist) Refusal(service ServiceRef, namespace, environment string, observed []string, advertised []AdvertisedEnvironment) (code, why string) {
	hosted := a.Hosted(advertised)
	switch {
	case len(observed) > 0 && !slices.Contains(observed, namespace):
		why = "namespace " + namespace + " is not one this member observes"
	case len(a.Namespaces) > 0 && !slices.Contains(a.Namespaces, namespace):
		why = "namespace " + namespace + " is not in delivery.remote.allow.namespaces (" + strings.Join(a.Namespaces, ", ") + ")"
	case hosted != nil && !slices.Contains(hosted, canonicalEnvironment(environment)):
		why = "environment " + environment + " is not one this member takes requests for (" + strings.Join(hosted, ", ") + ")"
	case len(a.Services) > 0 && !slices.ContainsFunc(a.Services, func(p string) bool { ok, _ := path.Match(p, service.Name); return ok }):
		why = "service " + service.Name + " matches no delivery.remote.allow.services pattern (" + strings.Join(a.Services, ", ") + ")"
	default:
		return "", ""
	}
	return RefusalOutsideAllowlist, why
}

// DefaultRequestLifetime is how long a request the hub signs lives.
const DefaultRequestLifetime = 24 * time.Hour

// NewOperationRequestStatement builds the statement of r: its subject is its
// member's request stream, by the digest it asks for.
func NewOperationRequestStatement(r OperationRequest) OperationRequestStatement {
	return OperationRequestStatement{Type: StatementType, Subject: requestSubject(r.Cluster, r.Digest),
		PredicateType: PredicateOperationRequest, Predicate: r}
}

// SignOperationRequest signs st and returns the bundle.
func SignOperationRequest(ctx context.Context, s Signer, st OperationRequestStatement) ([]byte, error) {
	bundle, _, err := signStatement(ctx, s, st)
	return bundle, err
}

// VerifiedRequest is a request a hub read and verified: signed by a person
// under its approver signers, naming that person as requester and the
// stream's member as cluster. Manifest is its manifest digest, the key its
// outcomes carry; SignedAt its verified signing time.
type VerifiedRequest struct {
	Request  OperationRequest
	Manifest string
	SignedAt time.Time
}

// The states a member is in for a planned promote, as the hub reads them.
const (
	MemberNoRequest = "no request"
	MemberWaiting   = "waiting"
	MemberUnknown   = "unknown"
	MemberRefused   = "refused"
	MemberRaised    = "raised"
	MemberHeld      = "held"
	MemberApplied   = "applied"
	MemberRejected  = "rejected"
	MemberFailed    = "failed"
)

// MemberState is one placed member and what happened to the newest request
// a person signed for it: its state, the digest asked for, the reason and
// the next command, the Operation it raised (namespace/name), when the
// member last said anything (its snapshot's asOf) and whether that is stale.
type MemberState struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	State     string `json:"state" enum:"no request,waiting,unknown,refused,raised,held,applied,rejected,failed"`
	Digest    string `json:"digest,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Next      string `json:"next,omitempty"`
	Operation string `json:"operation,omitempty"`
	AsOf      string `json:"asOf,omitempty"`
	Stale     bool   `json:"stale,omitempty"`
}

// MemberPlan is a promote of Service in Environment planned over the members
// of a hub: the placement, each placed member's state, the members a
// restricted reader may not see (counted, with why), what was not read
// (Notes; Completeness partial) and the command a person runs to confirm.
type MemberPlan struct {
	Service      ServiceRef          `json:"service"`
	Environment  string              `json:"environment"`
	Version      string              `json:"version,omitempty"`
	Digest       string              `json:"digest,omitempty"`
	Placement    MemberPlacementPlan `json:"placement"`
	Members      []MemberState       `json:"members"`
	Hidden       int                 `json:"hidden"`
	HiddenReason string              `json:"hiddenReason,omitempty"`
	Completeness fleet.Completeness  `json:"completeness"`
	Notes        []string            `json:"notes,omitempty"`
	AsOf         string              `json:"asOf"`
	Next         string              `json:"next,omitempty"`
}

// The codes the hub refuses a member plan or request with, each with its
// next command (HubRefusal).
const (
	RefusalMemberRequestNeedsPerson = "MemberRequestNeedsPerson"
	RefusalNotAMember               = "NotAMember"
	RefusalStageOrMembers           = "StageOrMembers"
	RefusalMemberRecorderUnknown    = "MemberRecorderUnknown"
	RefusalDigestRequired           = "DigestRequired"
	RefusalMemberTakesNoRequests    = "MemberTakesNoRequests"
	RefusalIssuerNotAccepted        = "IssuerNotAccepted"
)

// ConfirmCommand is the command a person runs on the hub to sign the
// requests of req, reading the members from registry.
func ConfirmCommand(req Request, registry string) string {
	cmd := fmt.Sprintf("pacto delivery propose --service %s --environment %s --to %s", req.Service, req.Environment, cmp.Or(req.To, "<sha256 digest>"))
	for _, m := range req.Members {
		cmd += " --member " + m
	}
	return cmd + " --registry " + cmp.Or(registry, "<registry>") + " --confirm"
}

// HubRefusalError is a hub refusal: its stable code, why and the next
// command a person runs.
type HubRefusalError struct{ Code, Why, Next string }

func (e *HubRefusalError) Error() string {
	return fmt.Sprintf("%s: %s; next: %s", e.Code, e.Why, e.Next)
}

// CheckRequestMode refuses a request that names both a Stage and members,
// or neither, and a member request that is not a promote (an empty type is
// one) to a sha256 digest, for req read from registry.
func CheckRequestMode(req Request, registry string) error {
	switch {
	case req.Stage != "" && (req.Service != "" || req.Environment != "" || len(req.Members) > 0):
		return HubRefusal(RefusalStageOrMembers, "pass --stage, or --service with --environment, not both", req, registry)
	case req.Stage == "" && !req.MemberMode():
		return HubRefusal(RefusalStageOrMembers, "name the Stage with --stage, or the service and environment with --service and --environment", req, registry)
	case req.Stage == "" && cmp.Or(req.Type, TypePromote) != TypePromote:
		return HubRefusal(RefusalStageOrMembers, "a member request is a promote only; change "+req.Type+" with --stage on the member itself", req, registry)
	case req.Stage == "" && !strings.HasPrefix(req.To, "sha256:"):
		return HubRefusal(RefusalDigestRequired, "member mode takes the digest of what to promote, never a version or tag", req, registry)
	}
	return nil
}

// HubRefusal is the refusal code with why and its next command, for req
// read from registry (a *HubRefusalError).
func HubRefusal(code, why string, req Request, registry string) error {
	next := map[string]string{
		RefusalMemberRequestNeedsPerson: "a person runs: " + ConfirmCommand(req, registry),
		RefusalNotAMember:               "pick an approved member: pacto cluster list --registry " + cmp.Or(registry, "<registry>"),
		RefusalStageOrMembers:           "pass --stage for a Stage in this cluster, or --service and --environment to plan over the members",
		RefusalMemberRecorderUnknown:    "a cluster admin of that member sets delivery.recorder.signing.publicKey there, then approve again",
		RefusalDigestRequired:           "pass the sha256: digest pacto publish printed for " + req.To + ", or the to of the offer on the Environments board",
		RefusalMemberTakesNoRequests:    "a cluster admin of that member sets delivery.remote.enabled, a registry and delivery there",
		RefusalIssuerNotAccepted:        "log in with an approver signer the member accepts: pass --oidc-issuer-url",
	}[code]
	return &HubRefusalError{Code: code, Why: why, Next: next}
}
