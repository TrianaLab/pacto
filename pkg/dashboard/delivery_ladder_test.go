package dashboard

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

const ladderDigest = "sha256:5be1"

// ladderCluster holds a managed payments Stage in payments-staging, a
// hand-written ledger Stage in books, and an installation ladder with one
// proposed rung.
func ladderCluster() *fakeDelivery {
	managed := stage("payments-staging", "payments-staging", "payments", delivery.StageStatus{})
	managed.Spec.Environment = "staging"
	p := delivery.Placement{Namespace: "payments-staging", Provenance: delivery.PlacementInferred, Source: delivery.PlacementFromGitOps,
		Evidence: "Argo CD source github.com/acme/payments"}
	managed.Metadata.Labels = delivery.ManagedLabels(delivery.ServiceRef{Name: "payments"}, "staging")
	managed.Metadata.Annotations = delivery.PlacementAnnotations(delivery.DesiredFromLadder, p)
	managed.Metadata.Annotations[delivery.PlacementConfirmedByAnnotation] = "bob@acme.com"
	return &fakeDelivery{caller: person("acme:team-payments"), stages: []delivery.StageObject{managed, stage("books", "ledger", "ledger", delivery.StageStatus{})},
		ladder: &delivery.EnvironmentsView{Declared: []delivery.LadderRung{{Name: "staging", Source: "chart", Provenance: delivery.RungDeclared},
			{Name: "production", Upstream: "staging", Source: "chart", Provenance: delivery.RungDeclared}},
			Proposal:       delivery.LadderProposal{{Name: "preview", Source: "from namespaces *-preview, 4 found", Provenance: delivery.RungInferred}},
			ProposalDigest: ladderDigest, UnplacedCount: 2},
		placements: []delivery.PlacementRow{
			{Service: "payments", Environment: "staging", Source: delivery.DesiredFromLadder, Placement: &p, Stage: "payments-staging/payments-staging"},
			{Service: "payments", Environment: "production", Source: delivery.DesiredFromLadder,
				Placement: &delivery.Placement{Namespace: "production", Provenance: delivery.PlacementDefault, Source: delivery.PlacementFromRungName},
				Reason:    "namespace production does not exist: create it or set delivery.environments.createNamespaces=true"},
			{Service: "orders", Environment: "staging", Source: delivery.DesiredFromLadder,
				Placement: &delivery.Placement{Namespace: "orders", Provenance: delivery.PlacementUnknown, Refused: "refused: namespace orders belongs to orders (team-orders)"}},
		}, placementsReadable: true}
}

func environmentsOf(t *testing.T, f *fakeDelivery, query string) map[string]any {
	t.Helper()
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	code, envs := call(t, http.MethodGet, base+"/api/delivery/environments"+query, nil)
	expect(t, code == http.StatusOK, "environments: %d %v", code, envs)
	return envs
}

func TestEnvironmentRowSourceAndPlacement(t *testing.T) {
	row := rowOf(t, environmentsOf(t, ladderCluster(), ""), "payments")
	pl, _ := row["placement"].(map[string]any)
	expect(t, row["source"] == "ladder" && pl["namespace"] == "payments-staging" && pl["provenance"] == "inferred" && pl["source"] == "gitops-source" &&
		pl["evidence"] == "Argo CD source github.com/acme/payments" && pl["confirmedBy"] == "bob@acme.com", "managed row: %v", row)
}

func TestHandWrittenRowSource(t *testing.T) {
	row := rowOf(t, environmentsOf(t, ladderCluster(), ""), "ledger")
	pl, _ := row["placement"].(map[string]any)
	expect(t, row["source"] == "hand-written" && pl["namespace"] == "books" && pl["provenance"] == "declared" && pl["source"] == "hand-written", "hand-written row: %v", row)
}

func TestEnvironmentsNoDeliveryUnchanged(t *testing.T) {
	f := ladderCluster()
	f.ladder, f.placements = nil, nil
	envs := environmentsOf(t, f, "")
	for _, svc := range []string{"payments", "ledger"} {
		row := rowOf(t, envs, svc)
		_, hasSource := row["source"]
		_, hasPlacement := row["placement"]
		expect(t, !hasSource && !hasPlacement, "no installation ladder, no source: %v", row)
	}
	_, unplaced := envs["unplaced"]
	_, hidden := envs["unplacedHidden"]
	expect(t, !unplaced && !hidden && envs["completeness"] == "complete", "nothing new: %v", envs)
}

func TestEnvironmentsLadderUnreadableIsPartial(t *testing.T) {
	f := ladderCluster()
	f.ladder, f.ladderErr = nil, errors.New("forbidden")
	envs := environmentsOf(t, f, "")
	_, hasSource := rowOf(t, envs, "payments")["source"]
	expect(t, !hasSource && envs["completeness"] == "partial" && slices.Contains(toStrings(envs["unreadable"]), "the installation ladder: forbidden"),
		"an unread ladder is unknown: %v", envs)
}

func TestUnplacedListedWithReason(t *testing.T) {
	envs := environmentsOf(t, ladderCluster(), "")
	un := envs["unplaced"].([]any)
	expect(t, len(un) == 2, "two environments have no Stage: %v", un)
	prod, orders := un[0].(map[string]any), un[1].(map[string]any)
	expect(t, prod["service"] == "payments" && prod["environment"] == "production" && prod["source"] == "ladder" &&
		prod["reason"] == "namespace production does not exist: create it or set delivery.environments.createNamespaces=true" &&
		prod["placement"].(map[string]any)["provenance"] == "default", "production: %v", prod)
	expect(t, orders["reason"] == "refused: namespace orders belongs to orders (team-orders)", "a refusal is the reason: %v", orders)
	envs = environmentsOf(t, ladderCluster(), "?service=orders")
	expect(t, len(envs["unplaced"].([]any)) == 1, "service narrows: %v", envs["unplaced"])
	envs = environmentsOf(t, ladderCluster(), "?state=failed")
	_, listed := envs["unplaced"]
	expect(t, !listed, "a state keeps no row with no Stage: %v", envs["unplaced"])
}

func TestUnplacedHiddenAndCounted(t *testing.T) {
	f := ladderCluster()
	f.placements, f.placementsReadable = nil, false
	envs := environmentsOf(t, f, "")
	_, rows := envs["unplaced"]
	expect(t, !rows && envs["unplacedHidden"] == float64(2), "hidden and counted: %v", envs)
}

func TestUnplacedNarrowedByNamespace(t *testing.T) {
	f := ladderCluster()
	f.forbidAll, f.stageNS = true, []string{"payments-staging", "books", "production"}
	f.placements = append(f.placements, delivery.PlacementRow{Service: "payments", Environment: "preview", Reason: "waiting: publisher not trusted yet"})
	envs := environmentsOf(t, f, "")
	un := envs["unplaced"].([]any)
	expect(t, len(un) == 1 && un[0].(map[string]any)["environment"] == "production" && envs["unplacedHidden"] == float64(2),
		"orders and a row with no namespace are hidden from a caller who lists three namespaces: %v %v", un, envs["unplacedHidden"])
}

func inboxLadderOf(t *testing.T, f *fakeDelivery) map[string]any {
	t.Helper()
	base := startDelivery(t, f, (&authed{state: AuthState{SignedIn: true}}).auth(false, true), true, nil)
	code, inbox := call(t, http.MethodGet, base+"/api/delivery/inbox", nil)
	expect(t, code == http.StatusOK, "inbox: %d %v", code, inbox)
	l, _ := inbox["ladder"].(map[string]any)
	return l
}

func TestInboxLadderCanConfirmBySAR(t *testing.T) {
	f := ladderCluster()
	l := inboxLadderOf(t, f)
	expect(t, l["canConfirm"] == true && f.cans.Load() == 1, "a person allowed to approve environmentladders: %v", l)
	f.deny = map[string]bool{"approve environmentladders": true}
	expect(t, inboxLadderOf(t, f)["canConfirm"] == false, "denied")
	f.deny, f.canErr = nil, errors.New("timeout")
	expect(t, inboxLadderOf(t, f)["canConfirm"] == false, "a review that fails says no")
	f.canErr, f.caller = nil, delivery.Caller{Identity: delivery.Identity{User: "system:serviceaccount:agents:bot"}, Class: delivery.ClassAgent}
	before := f.cans.Load()
	expect(t, inboxLadderOf(t, f)["canConfirm"] == false && f.cans.Load() == before, "an agent never confirms, and is not asked about")
}

func TestInboxLadderCommandExact(t *testing.T) {
	l := inboxLadderOf(t, ladderCluster())
	expect(t, l["command"] == "pacto delivery ladder confirm --proposal "+ladderDigest && l["proposalDigest"] == ladderDigest &&
		len(l["declared"].([]any)) == 2 && len(l["confirmed"].([]any)) == 0 && l["proposal"].([]any)[0].(map[string]any)["name"] == "preview", "ladder: %v", l)
}

func TestInboxLadderAbsentWithoutProposal(t *testing.T) {
	f := ladderCluster()
	f.ladder.Proposal, f.ladder.ProposalDigest = nil, ""
	expect(t, inboxLadderOf(t, f) == nil, "no proposal, no block")
	f.ladder = nil
	expect(t, inboxLadderOf(t, f) == nil, "no installation ladder, no block")
}
