package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// memberRemotes are the hub's approved members, each a read-only remote
// over the Stage records it signs, as p may see them; none on a host that
// reads no members. A member already read through a context (live, in
// skip) is not read again. Behind a viewer a member with no live channel is
// hidden, and a namespace there shows only when that member's review lets p
// read it (Viewer.Sees, as the Members block reads it).
func (id *dashboardIdentity) memberRemotes(ctx context.Context, p authn.Person, skip map[string]bool) []dashboard.RemoteDelivery {
	if id.members == nil {
		return nil
	}
	o, err := id.members.options(ctx)
	if err != nil {
		return nil // the Members block of each service says why
	}
	trust, states := id.members.svc.MemberTrust(ctx, o)
	verify, _ := signing.TrustVerifier(*trust) // MemberRecordTrust builds only keys a snapshot's own key signed
	// ponytail: one Reader per request; keep one per trust digest on the
	// identity if member histories load the registry.
	reader := &deliveryrecord.Reader{Verify: verify}
	var out []dashboard.RemoteDelivery
	for _, m := range states {
		if m.State != delivery.JoinApproved || skip[m.UID] {
			continue
		}
		c := fleet.Cluster{ID: m.UID, Name: m.Name}
		r := dashboard.RemoteDelivery{Cluster: c, Act: memberAct(o.Repository)}
		switch {
		case id.viewer != nil && !id.viewer.Reviewable(ctx, p, m.UID):
			r.Hidden = true
		case m.RecorderKey == nil:
			r.Err = errors.New(m.Name + " advertises no recorder key, so its Stage records verify under no key (" + delivery.RefusalMemberRecorderUnknown +
				"): a cluster admin of that member sets delivery.recorder.signing.publicKey there")
		default:
			md := &memberRecordDelivery{member: m, registry: o.Repository, reader: reader, records: id.records, now: id.now, hidden: map[string]bool{}}
			if id.viewer != nil {
				md.sees = func(ns string) bool { return id.viewer.Sees(ctx, p, m.UID, ns) }
			}
			if at, err := time.Parse(time.RFC3339, m.SnapshotAsOf); err == nil {
				r.AsOf = &at
				if age := id.now().Sub(at); age > delivery.TargetSnapshotFreshness {
					md.unreadable = append(md.unreadable, fmt.Sprintf("the snapshot that lists its Stages is %s old, past %s, so a Stage there may have moved or gone",
						age.Round(time.Minute), delivery.TargetSnapshotFreshness))
				}
			}
			r.API = md
		}
		out = append(out, r)
	}
	return out
}

// memberAct is how a person acts on a member's change: on the hub, through
// the intent record the hub's Members block names.
func memberAct(registry string) string {
	return "decide it on the hub: pacto delivery approve --intent <intent record> --registry " + registry +
		" (each service's Members block names the intent record of a held request)"
}

// memberRecordDelivery reads one approved member's Stages from the records
// its recorder signs: each Stage its join block lists, verified under that
// member's recorder key scoped to exactly those streams
// (delivery.MemberRecordTrust). It only reads: the member's Operations are
// its own, and a person decides a held request on the hub.
type memberRecordDelivery struct {
	member delivery.ClusterJoinState
	// registry is the hub's pacto-targets repository, which the command
	// that decides a held request names.
	registry string
	reader   *deliveryrecord.Reader
	records  app.RecordRepo
	now      func() time.Time
	// sees says whether the reader may read namespace there; nil reads all.
	sees func(namespace string) bool

	mu         sync.Mutex
	unreadable []string
	hidden     map[string]bool
}

// memberStream is one Stage a member records, parsed from its stream id.
type memberStream struct {
	delivery.JoinStream
	namespace, name string
}

// streams are the member's recorded Stages in namespace ns ("" all) the
// reader may see; one it may not see is counted, never named.
func (m *memberRecordDelivery) streams(ns string) []memberStream {
	var out []memberStream
	for _, s := range m.member.Streams {
		parts := strings.SplitN(s.Stream, "/", 3)
		if len(parts) != 3 || parts[0] != m.member.UID || ns != "" && parts[1] != ns {
			continue // a join block names only its own <uid>/<namespace>/<stage> streams
		}
		if m.sees != nil && !m.sees(parts[1]) {
			m.mu.Lock()
			m.hidden[parts[1]] = true
			m.mu.Unlock()
			continue
		}
		out = append(out, memberStream{JoinStream: s, namespace: parts[1], name: parts[2]})
	}
	return out
}

// note counts what could not be read or verified.
func (m *memberRecordDelivery) note(why string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unreadable = append(m.unreadable, why)
}

// HiddenNamespaces counts the namespaces the reader may not read there.
func (m *memberRecordDelivery) HiddenNamespaces() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.hidden)
}

// Unreadable says what of the member could not be read or verified.
func (m *memberRecordDelivery) Unreadable() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.unreadable...)
}

// stage is s as its verified records say it stands, or nil when they say
// nothing it can check (counted in Unreadable).
func (m *memberRecordDelivery) stage(ctx context.Context, s memberStream) *delivery.StageObject {
	repo, err := m.records(m.member.StreamsRepository)
	if err != nil {
		m.note(s.namespace + "/" + s.name + ": " + err.Error())
		return nil
	}
	ctx, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	where := s.namespace + "/" + s.name
	l, err := m.reader.ListStream(ctx, repo, s.Stream)
	switch {
	case err != nil:
		m.note(where + ": reading the records: " + err.Error())
		return nil
	case len(l.Unreadable) > 0:
		m.note(fmt.Sprintf("%s: %d records did not verify under member-recorder:%s", where, len(l.Unreadable), m.member.Name))
	}
	if len(l.Records) == 0 {
		return nil
	}
	status, why := delivery.UpstreamFromRecords(l.Records, delivery.RecordMark{}, nil)
	if status == nil {
		m.note(where + ": " + why)
		return nil
	}
	// The version it runs is the newest recorded pointer move.
	for _, r := range delivery.FoldStream(l.Records).Records {
		if o := r.Statement.Predicate.Outcome; o != nil && o.Pointer.After != "" {
			status.Pointer = o.Pointer.After
		}
	}
	status.Conditions = []delivery.Condition{{Type: "Ready", Status: "Unknown", Reason: "RecordedByMember",
		Message: "recordedBy: member " + m.member.Name + ": read from the Stage records it signs, so only what it recorded is known here"}}
	return &delivery.StageObject{Metadata: delivery.ObjectMeta{Name: s.name, Namespace: s.namespace},
		Spec: delivery.StageSpec{Service: s.Service, Environment: s.Environment}, Status: *status}
}

func (m *memberRecordDelivery) Stages(ctx context.Context, ns string) ([]delivery.StageObject, error) {
	out := []delivery.StageObject{}
	for _, s := range m.streams(ns) {
		if st := m.stage(ctx, s); st != nil {
			out = append(out, *st)
		}
	}
	return out, nil
}

// find is the recorded Stage ns/name, or the same 404 as an absent one.
func (m *memberRecordDelivery) find(ns, name string) (memberStream, error) {
	for _, s := range m.streams(ns) {
		if s.name == name {
			return s, nil
		}
	}
	return memberStream{}, dashboard.Refuse(http.StatusNotFound, "NotFound", "member "+m.member.Name+" records no Stage "+ns+"/"+name+" you may read")
}

func (m *memberRecordDelivery) Stage(ctx context.Context, ns, name string) (*delivery.StageObject, error) {
	s, err := m.find(ns, name)
	if err != nil {
		return nil, err
	}
	if st := m.stage(ctx, s); st != nil {
		return st, nil
	}
	return nil, dashboard.Refuse(http.StatusServiceUnavailable, "RecordsUnreadable", "the records of "+ns+"/"+name+" on member "+m.member.Name+
		" could not be read or verified: "+strings.Join(m.Unreadable(), "; "))
}

// history is ns/name's history from its verified records, and the records.
func (m *memberRecordDelivery) history(ctx context.Context, ns, name string) (*delivery.History, []delivery.StoredRecord, error) {
	st, err := m.Stage(ctx, ns, name)
	if err != nil {
		return nil, nil, err
	}
	repo, _ := m.records(m.member.StreamsRepository) // Stage opened it
	bounded, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	l, err := m.reader.ListStream(bounded, repo, st.Status.Stream.ID)
	read := &delivery.RecordsRead{Fold: delivery.FoldStream(l.Records), Unreadable: l.Unreadable, Trust: "member-recorder:" + m.member.Name}
	h, _ := delivery.AssembleHistory(st, nil, read, err, m.now().UTC().Format(time.RFC3339))
	return h, read.Fold.Records, nil
}

func (m *memberRecordDelivery) History(ctx context.Context, ns, stage string, _ *delivery.StageObject) (*delivery.History, error) {
	h, _, err := m.history(ctx, ns, stage)
	return h, err
}

func (m *memberRecordDelivery) Blame(ctx context.Context, ns, stage, digest string) (*delivery.Blame, error) {
	h, recs, err := m.history(ctx, ns, stage)
	if err != nil {
		return nil, err
	}
	return delivery.BlameOf(h, recs, nil, digest), nil
}

func (m *memberRecordDelivery) Record(ctx context.Context, ns, stage, id string) (*delivery.RecordView, error) {
	h, recs, err := m.history(ctx, ns, stage)
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		if r.ID == id {
			v := delivery.RecordViewOf(ns, stage, r, "member-recorder:"+m.member.Name, h.AsOf)
			return &v, nil
		}
	}
	return nil, dashboard.Refuse(http.StatusNotFound, "NotFound", "no verified record "+id+" is in the stream of "+ns+"/"+stage+" on member "+m.member.Name)
}

// Operations are the member's own: a hub reads only what its records say.
func (m *memberRecordDelivery) Operations(context.Context, string) ([]delivery.OperationObject, error) {
	return []delivery.OperationObject{}, nil
}

func (m *memberRecordDelivery) Describe(context.Context, *delivery.StageObject, string) delivery.Revision {
	return delivery.Revision{}
}

// errMemberReadOnly refuses what only the member, or a person on the hub,
// does.
func (m *memberRecordDelivery) errMemberReadOnly() error {
	return dashboard.Refuse(http.StatusConflict, "RemoteClusterReadOnly", "this dashboard only reads member "+m.member.Name+" from its records: "+
		memberAct(m.registry))
}

// errNoOperation is the 404 of an Operation: a hub reads none of a member's.
func (m *memberRecordDelivery) errNoOperation(ns, name string) error {
	return dashboard.Refuse(http.StatusNotFound, "NotFound", "this dashboard reads no Operation of member "+m.member.Name+", so not "+ns+"/"+name)
}

func (m *memberRecordDelivery) Get(_ context.Context, ns, name string) (*delivery.OperationObject, error) {
	return nil, m.errNoOperation(ns, name)
}

func (m *memberRecordDelivery) Explain(_ context.Context, ns, name string) (*delivery.Explanation, error) {
	return nil, m.errNoOperation(ns, name)
}

func (m *memberRecordDelivery) Undo(context.Context, string, string, string) (*delivery.Undo, error) {
	return nil, m.errMemberReadOnly()
}

func (m *memberRecordDelivery) WhoAmI(context.Context) (delivery.Caller, error) {
	return delivery.Caller{}, m.errMemberReadOnly()
}

func (m *memberRecordDelivery) Plan(context.Context, delivery.Request) (delivery.PlanAnswer, error) {
	return delivery.PlanAnswer{}, m.errMemberReadOnly()
}

func (m *memberRecordDelivery) Propose(context.Context, delivery.Request) (*delivery.OperationObject, delivery.PlanAnswer, error) {
	return nil, delivery.PlanAnswer{}, m.errMemberReadOnly()
}

func (m *memberRecordDelivery) Approve(context.Context, string, string, delivery.Decision, string) (string, error) {
	return "", m.errMemberReadOnly()
}

func (m *memberRecordDelivery) Cancel(context.Context, string, string) error {
	return m.errMemberReadOnly()
}

func (m *memberRecordDelivery) Trust(context.Context, []delivery.StageObject, []delivery.OperationObject) (*delivery.TrustView, error) {
	return nil, m.errMemberReadOnly()
}

func (m *memberRecordDelivery) PublishRefusals(context.Context, string) (*dashboard.PublishRefusals, error) {
	return nil, nil
}

func (m *memberRecordDelivery) Can(context.Context, string, string, string) (bool, error) {
	return false, nil
}

func (m *memberRecordDelivery) WorkloadAccess(_ context.Context, ns string, w delivery.WorkloadRef) delivery.WorkloadAccess {
	return delivery.WorkloadAccess{Name: w.Name, Namespace: ns, Unverified: "this dashboard only reads member " + m.member.Name + " from its records"}
}

func (m *memberRecordDelivery) StageNamespaces(context.Context) ([]string, error) { return nil, nil }

func (m *memberRecordDelivery) Environments(context.Context) (*delivery.EnvironmentsView, []delivery.PlacementRow, bool, error) {
	return nil, nil, false, nil
}

func (m *memberRecordDelivery) Members(context.Context, string, []string) []delivery.MemberPlan {
	return nil
}
