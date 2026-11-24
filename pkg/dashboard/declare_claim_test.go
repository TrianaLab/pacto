package dashboard

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// claimServer serves inferredSnap with stripe (used by orders) and the
// other-egress node through view with d as the declarer.
func claimServer(t *testing.T, view func(context.Context, *fleet.Query) (*fleet.Query, error), d Declarer) string {
	t.Helper()
	snap := inferredSnap(t)
	orders := fleet.NewServiceKey("orders")
	snap.Externals = map[fleet.ExternalKey]*fleet.ExternalRecord{
		"stripe":          {Key: "stripe", Class: "vendor-api", Consumers: []fleet.ServiceKey{orders}, ConsumersTotal: 1, Sources: []fleet.ExternalSource{{Kind: "contract", Service: orders, Provenance: "declared"}}},
		fleet.OtherEgress: {Key: fleet.OtherEgress, Class: fleet.ClassOtherEgress, Consumers: []fleet.ServiceKey{orders}, ConsumersTotal: 1, Sources: []fleet.ExternalSource{{Kind: "contract", Service: orders, Provenance: "declared"}}},
	}
	q := fleet.NewQuery(snap)
	return serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, View: view, Auth: viewerAuth(false, AuthState{SignedIn: true}), Declare: d})
}

func TestDashboardDeclareResolvesExternal(t *testing.T) {
	d := &fakeDeclarer{write: true}
	base := claimServer(t, nil, d)
	var out DeclareAnswer
	postJSON(t, base+"/api/fleet/declare", map[string]any{"serviceKey": "external:stripe", "owner": "team-platform", "name": "payments-stripe"}, http.StatusOK, &out)
	if len(d.plans) != 1 || d.plans[0].Key != "external:stripe" || d.plans[0].Name != "stripe" || d.plans[0].View == nil ||
		d.plans[0].Namespace != "" || d.plans[0].Kind != "" || d.plans[0].Cluster != "" {
		t.Fatalf("planned %+v", d.plans)
	}
	d.accept = func(in DeclareAcceptInput) (*DeclareAcceptAnswer, error) {
		if in.Owner != "team-platform" || in.Name != "payments-stripe" {
			t.Errorf("accept got %+v", in)
		}
		return &DeclareAcceptAnswer{Tag: "0.1.0"}, nil
	}
	var acc DeclareAcceptAnswer
	postJSON(t, base+"/api/fleet/declare/accept", map[string]any{"serviceKey": "external:stripe", "owner": "team-platform", "name": "payments-stripe",
		"intentDigest": "sha256:i", "mode": "sign"}, http.StatusOK, &acc)
	if acc.Tag != "0.1.0" {
		t.Errorf("accept %+v", acc)
	}
	// A name that is no service name never reaches the declarer.
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]any{"serviceKey": "external:stripe", "name": "Not A Name"}); code != http.StatusUnprocessableEntity {
		t.Errorf("a bad name answered %d", code)
	}
}

func TestDashboardClaimHiddenExternal404(t *testing.T) {
	d := &fakeDeclarer{write: true}
	hideAll := func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, _ string) bool { return false }, nil), nil
	}
	base := claimServer(t, hideAll, d)
	code, hidden := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": "external:stripe"})
	_, missing := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": "external:twilio"})
	if code != http.StatusNotFound || hidden["detail"] != missing["detail"] || hidden["detail"] != "no external system with this key" || len(d.plans) != 0 {
		t.Errorf("hidden %d %v and missing %v must answer the same 404", code, hidden, missing)
	}
}

func TestDashboardClaimOtherEgress404(t *testing.T) {
	d := &fakeDeclarer{write: true}
	base := claimServer(t, nil, d)
	for _, key := range []string{"external:" + string(fleet.OtherEgress), "external:"} {
		if code, _ := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": key}); code != http.StatusNotFound || len(d.plans) != 0 {
			t.Errorf("%s answered %d", key, code)
		}
	}
}

// TestDeclareRefusalEnumCarriesClaimCodes: DeclareRefusal.code lists every
// refusal code internal/app's declare planner defines, read from its source
// so a new code cannot be missed, plus the dashboard's own. The --pr codes
// are left out: the dashboard never opens a pull request (513 D7).
func TestDeclareRefusalEnumCarriesClaimCodes(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "../../internal/app/declare.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var app []string
	ast.Inspect(f, func(n ast.Node) bool {
		if v, ok := n.(*ast.ValueSpec); ok && strings.HasPrefix(v.Names[0].Name, "code") && len(v.Values) == 1 {
			if lit, ok := v.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, _ := strconv.Unquote(lit.Value)
				app = append(app, s)
			}
		}
		return true
	})
	if !slices.Contains(app, "ClaimRepositoryInferred") {
		t.Fatalf("read no claim code from internal/app: %v", app)
	}
	own := []string{"IdentityRequired", "SignInLinkReadsFleet", "ReviewFailed", "ClusterNotReviewable", "NotAllowed", "DraftNotOffered", "TemplateSourceUnverified"}
	f2, _ := reflect.TypeFor[DeclareRefusal]().FieldByName("Code")
	enum := strings.Split(f2.Tag.Get("enum"), ",")
	for _, c := range slices.Concat(app, own, delivery.OwnerRightCodes) {
		if c != "RepositoryUnknown" && c != "RepositoryUnconfirmed" && !slices.Contains(enum, c) {
			t.Errorf("DeclareRefusal.code has no %s", c)
		}
	}
}

// TestDeclareInputKeepsClaimOfferingApartFromAcceptance: a claim's offering
// (a string) and an offering acceptance (an object) reach the declarer under
// their own keys on the plan and accept routes: neither shadows the other in
// the decoded input or in the route's body schema.
func TestDeclareInputKeepsClaimOfferingApartFromAcceptance(t *testing.T) {
	body := map[string]any{"serviceKey": "external:stripe", "owner": "team-pay", "offering": "postgresql", "offeringAcceptance": map[string]any{"version": "1.0.0"}}
	var plan DeclareInput
	raw, _ := json.Marshal(body)
	if err := json.Unmarshal(raw, &plan); err != nil || plan.ClaimInput.Offering != "postgresql" || plan.OfferingAcceptance == nil || plan.OfferingAcceptance.Version != "1.0.0" {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	d := &fakeDeclarer{write: true}
	base := claimServer(t, nil, d)
	var out DeclareAnswer
	postJSON(t, base+"/api/fleet/declare", body, http.StatusOK, &out)
	var got DeclareAcceptInput
	d.accept = func(in DeclareAcceptInput) (*DeclareAcceptAnswer, error) { got = in; return &DeclareAcceptAnswer{Tag: "0.1.0"}, nil }
	body["intentDigest"], body["mode"] = "sha256:i", "sign"
	var acc DeclareAcceptAnswer
	postJSON(t, base+"/api/fleet/declare/accept", body, http.StatusOK, &acc)
	if got.ClaimInput.Offering != "postgresql" || got.OfferingAcceptance == nil || got.OfferingAcceptance.Version != "1.0.0" {
		t.Fatalf("accept got %+v", got)
	}
}
