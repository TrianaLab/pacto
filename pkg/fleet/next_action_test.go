package fleet

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/finding"
)

// editCodes are the pre-adoption attention codes whose next step is a
// contract edit. SUCCESSOR_OUT_OF_RANGE and a past-sunset item carry it only
// when their next step widens a range; an unowned service whose contracts
// cannot be read gets none (unknown is never positive).
var editCodes = map[string]bool{
	"DEPENDENCY_UNDECLARED": true, "UNOWNED_SERVICE": true, "INVALID_CONTRACT": true,
	"MISSING_READINESS": true, LimitationRangeUnsatisfied: true, "GOLDEN_PATH_DEPLOYED": true,
	CodeSuccessorOutOfRange: true,
}

// actionRow is one item of an adoption attention code (#508 to #517) in one
// fixture, and the action it carries. Later stories append rows.
type actionRow struct {
	query       func(*testing.T) *Query
	code, label string
	want        *NextAction
}

// adoptionRows pins the action of every adoption attention code, one row per
// branch. A row's want always has a cli.
func adoptionRows() []actionRow {
	declare := func(cli string) *NextAction { return &NextAction{Verb: "declare", Tool: "pacto_declare", CLI: cli} }
	golden := func(t *testing.T) *Query { return goldenWith(t, true, kernel(call(cartName, "orders"))) }
	handed := func(t *testing.T) *Query { return NewQuery(handSnap(t, []RawRevision{anaDecl(t)}, nil, true)) }
	stripe := func(t *testing.T) *Query {
		return infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")))
	}
	ledger := func(t *testing.T) *Query {
		return ledgerFleet(t, "^9.0.0", []string{"9.0.0"}, map[string]bool{"9.0.0": true})
	}
	return []actionRow{
		// An uncontracted caller whose workload Pacto sees, one it does not,
		// one matching two services and one that names no workload.
		{golden, "UNCONTRACTED_CALLER", cartName + " calls orders", declare("pacto declare cart --namespace shop")},
		{undeclaredFleet, "UNCONTRACTED_CALLER", "k8s:secret/Deployment/x calls b", declare("pacto declare x --namespace secret")},
		{undeclaredFleet, "UNCONTRACTED_CALLER", "pay calls b", editContract()},
		{func(t *testing.T) *Query { return observedFleet(t) }, "UNCONTRACTED_CALLER", "cron-x calls pg", editContract()},
		// A replaced inferred service: check the contract describes it.
		{handed, attentionInferredReplaced, invoicesName + " was replaced by invoices", editContract()},
		{func(t *testing.T) *Query { return replacedQuery(t, 1) }, attentionInferredReplaced, cartName + " was replaced by cart-svc", editContract()},
		// A range on an inferred contract: declare a service, write an external.
		{stripe, RangeReasonInferredProvider, "1 service checks a range against stripe's inferred contract", editContract()},
		{ledger, RangeReasonInferredProvider, "1 service checks a range against ledger's inferred contract", declare("pacto declare ledger")},
	}
}

func allAttention(t *testing.T) []AttentionItem {
	t.Helper()
	var out []AttentionItem
	for _, q := range []*Query{productFleet(t), rangeFleet(t), undeclaredFleet(t), domainFleet(t), deployedTemplateFleet(t), lifecycleRangeFleet(t)} {
		out = append(out, attentionOf(t, q, AttentionFilter{}).Items...)
	}
	return out
}

// wantAction is the action it must carry: its adoption row's, else
// edit-contract for an edit code, else none. ok is false for an adoption
// code with no row.
func wantAction(it AttentionItem) (want *NextAction, ok bool) {
	rows := adoptionRows()
	if slices.ContainsFunc(rows, func(r actionRow) bool { return r.code == it.Code }) {
		i := slices.IndexFunc(rows, func(r actionRow) bool { return r.code == it.Code && r.label == it.Label })
		if i < 0 {
			return nil, false
		}
		return rows[i].want, true
	}
	switch {
	case it.Code == "UNOWNED_SERVICE" && strings.Contains(it.Summary, "Owner unknown"),
		it.Code == "DEPENDENCY_SERVICE_SUNSET" && !strings.HasPrefix(it.NextStep, "Widen the range"),
		!editCodes[it.Code] && it.Code != "DEPENDENCY_SERVICE_SUNSET":
		return nil, true
	}
	return editContract(), true
}

func TestAttentionNextActionEditContract(t *testing.T) {
	seen := map[string]bool{}
	for _, it := range allAttention(t) {
		want, ok := wantAction(it)
		if !ok {
			t.Errorf("%s %q: no adoption row", it.Code, it.Label)
			continue
		}
		if !reflect.DeepEqual(it.NextAction, want) {
			t.Errorf("%s %q: nextAction %+v, want %+v", it.Code, it.Label, it.NextAction, want)
			continue
		}
		if want != nil && *want == *editContract() {
			seen[it.Code] = true
		}
	}
	for code := range editCodes {
		if !seen[code] {
			t.Errorf("no fixture produces %s with an edit-contract action", code)
		}
	}
}

func TestEveryAdoptionAttentionCodeSetsNextAction(t *testing.T) {
	for _, r := range adoptionRows() {
		var got []AttentionItem
		for _, it := range attentionOf(t, r.query(t), AttentionFilter{}).Items {
			if it.Code == r.code && it.Label == r.label {
				got = append(got, it)
			}
		}
		if len(got) != 1 || !reflect.DeepEqual(got[0].NextAction, r.want) || r.want.CLI == "" {
			t.Errorf("set nextAction for %s %q: got %+v, want %+v", r.code, r.label, got, r.want)
		}
	}
}

// preAdoptionCodes are the attention codes on main before #508 that carry no
// action, each with the reason. Edit codes are in editCodes.
var preAdoptionCodes = map[string]string{
	"NON_COMPLIANT":                      "a person reads the findings",
	"STALE_EVIDENCE":                     "the evidence source is refreshed, never the contract",
	"UNRESOLVED_DEPENDENCY":              "the dependency is published, which no contract edit does",
	string(finding.CodeHealthFailing):    "the workload is fixed, never the contract",
	"DEPENDENCY_SERVICE_DEPRECATED":      "the move to a supported version is a code change",
	"DEPENDENCY_SERVICE_SUNSET":          "the move to a supported version is a code change, unless the step widens a range (editCodes rule)",
	codeProvisioningFailed:               "the provider fixes the resource",
	codeProvisioningStale:                "the provider reports again",
	codeCrossEnvironmentCall:             "a person finds out why the workload calls across environments; no contract edit stops the call",
	codeCrossEnvironmentPreviewCall:      "a person finds out why the workload calls a preview; no contract edit stops the call",
	CodeClusterIssuerDiffers:             "a person approves a group mapping with pacto cluster map-groups; no contract edit answers it",
	string(finding.CodeCapabilityAbsent): "the producer that reports the extension absent is asked, or the capability fixed; no contract edit answers it",
	codeExtensionUnreported:              "the producer reports again; no contract edit answers it",
}

// dynamicCodeProducers are the functions that set a computed code, each with
// the reason none of its codes carries an action.
var dynamicCodeProducers = map[string]string{
	"targetAttention":    "an unknown compliance cause is checked at its evidence source",
	"knowledgeAttention": "a knowledge gap names what is unknown; unknown is never positive",
}

// selectorCodes resolves the string(pkg.X) codes the walk meets.
var selectorCodes = map[string]string{
	"finding.CodeHealthFailing":        string(finding.CodeHealthFailing),
	"finding.CodeDependencyUndeclared": string(finding.CodeDependencyUndeclared),
	"finding.CodeCapabilityAbsent":     string(finding.CodeCapabilityAbsent),
}

// attentionCodes walks the production files of dir and returns every code
// an AttentionItem literal or a .Code assignment sets: string literals,
// package constants by name, string(pkg.X) as "pkg.X" and any other value as
// "func:" and the enclosing function's name.
func attentionCodes(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	var files []*ast.File
	paths, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	consts := stringConsts(files)
	var out []string
	for _, f := range files {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok {
				out = append(out, codesIn(fn, consts)...)
			}
		}
	}
	return out
}

// stringConsts maps each package constant with a string literal value to it.
func stringConsts(files []*ast.File) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if vs, ok := n.(*ast.ValueSpec); ok {
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							out[name.Name], _ = strconv.Unquote(lit.Value)
						}
					}
				}
			}
			return true
		})
	}
	return out
}

// codesIn is the codes fn sets (see [attentionCodes]).
func codesIn(fn *ast.FuncDecl, consts map[string]string) []string {
	var out []string
	resolve := func(e ast.Expr) {
		out = append(out, codeOf(e, consts, fn.Name.Name))
	}
	ast.Inspect(fn, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			for _, e := range attentionLits(n) {
				if kv, ok := e.(*ast.KeyValueExpr); ok && isIdent(kv.Key, "Code") {
					resolve(kv.Value)
				}
			}
		case *ast.AssignStmt:
			for i, l := range n.Lhs {
				if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "Code" && len(n.Rhs) == len(n.Lhs) {
					resolve(n.Rhs[i])
				}
			}
		}
		return true
	})
	return out
}

// attentionLits is the fields of n when n is an AttentionItem literal, and
// of each element whose type n's []AttentionItem type leaves out.
func attentionLits(n *ast.CompositeLit) []ast.Expr {
	if isIdent(n.Type, "AttentionItem") {
		return n.Elts
	}
	var out []ast.Expr
	if at, ok := n.Type.(*ast.ArrayType); ok && isIdent(at.Elt, "AttentionItem") {
		for _, e := range n.Elts {
			if cl, ok := e.(*ast.CompositeLit); ok && cl.Type == nil {
				out = append(out, cl.Elts...)
			}
		}
	}
	return out
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

// codeOf resolves one code expression inside function fn.
func codeOf(e ast.Expr, consts map[string]string, fn string) string {
	switch e := e.(type) {
	case *ast.BasicLit:
		v, _ := strconv.Unquote(e.Value)
		return v
	case *ast.Ident:
		if v, ok := consts[e.Name]; ok {
			return v
		}
	case *ast.CallExpr:
		if !isIdent(e.Fun, "string") || len(e.Args) != 1 {
			break
		}
		if sel, ok := e.Args[0].(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok {
				return pkg.Name + "." + sel.Sel.Name
			}
		}
	}
	return "func:" + fn
}

// unclassified is the codes neither an adoption row, editCodes,
// preAdoptionCodes nor dynamicCodeProducers account for.
func unclassified(codes []string) []string {
	var out []string
	for _, c := range codes {
		if v, ok := selectorCodes[c]; ok {
			c = v
		}
		fn, dynamic := strings.CutPrefix(c, "func:")
		_, pre := preAdoptionCodes[c]
		adoption := slices.ContainsFunc(adoptionRows(), func(r actionRow) bool { return r.code == c })
		if dynamic && dynamicCodeProducers[fn] == "" || !dynamic && !pre && !adoption && !editCodes[c] {
			out = append(out, c)
		}
	}
	return out
}

func TestAttentionCodesAreClassified(t *testing.T) {
	codes := attentionCodes(t, ".")
	if len(codes) < 15 {
		t.Fatalf("the walk found only %v", codes)
	}
	for _, c := range unclassified(codes) {
		t.Errorf("set nextAction for %s: add an adoption row, or a preAdoptionCodes or dynamicCodeProducers entry with the reason", c)
	}
	// A new code in a new file fails the walk.
	dir := t.TempDir()
	src := "package fleet\n\nconst codeNew = \"NEW_CODE\"\n\nfunc newAttention() []AttentionItem {\n\tit := AttentionItem{Code: \"NON_COMPLIANT\"}\n\tit.Code = codeNew\n\treturn append([]AttentionItem{it, AttentionItem{Code: pick()}}, []AttentionItem{{Code: \"ELIDED_CODE\"}}...)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := unclassified(attentionCodes(t, dir)); !slices.Equal(got, []string{"NEW_CODE", "func:newAttention", "ELIDED_CODE"}) {
		t.Errorf("unclassified = %v, want the new codes and the new producer", got)
	}
}

func TestUncontractedCallerDeclares(t *testing.T) {
	for _, r := range adoptionRows()[:4] {
		it := attentionOf(t, r.query(t), AttentionFilter{}).Items
		i := slices.IndexFunc(it, func(it AttentionItem) bool { return it.Label == r.label })
		if i < 0 || !reflect.DeepEqual(it[i].NextAction, r.want) {
			t.Errorf("%q: %+v", r.label, it)
		}
	}
}

func TestDeclareActionRefusesUnsafeNames(t *testing.T) {
	long := strings.Repeat("a", 64)
	for _, caller := range []string{
		"k8s:shop/Deployment/a;rm -rf /", "k8s:shop/Deployment/a|sh", "k8s:shop/Deployment/$(id)", "k8s:shop/Deployment/a b",
		"k8s:shop/Deployment/a'b", `k8s:shop/Deployment/a"b`, "k8s:shop/Deployment/a\nb", "k8s:sh;op/Deployment/a",
		"k8s:" + long + "/Deployment/a", "k8s:shop/Deployment/" + strings.Repeat("a.", 127) + "a", "k8s:shop/Deployment/A",
	} {
		_, ns, _, name, _ := ParseK8sCaller(caller)
		if a := declareAction(name, ns); a != nil {
			t.Errorf("%q: declare action %+v", caller, a)
		}
		if a := uncontractedAction(UncontractedCaller{Caller: caller}); !reflect.DeepEqual(a, editContract()) {
			t.Errorf("%q: action %+v", caller, a)
		}
	}
	if a := declareAction("a.b-c", long[:63]); a == nil || a.CLI != "pacto declare a.b-c --namespace "+long[:63] {
		t.Errorf("valid names refused: %+v", a)
	}
}

func TestEditContractHasCLI(t *testing.T) {
	if *editContract() != (NextAction{Verb: "edit-contract", Tool: "pacto_edit", CLI: "pacto ci"}) {
		t.Errorf("editContract = %+v", editContract())
	}
}

func TestNextActionJSONKeepsVerbAndTool(t *testing.T) {
	b, _ := json.Marshal(editContract())
	if string(b) != `{"verb":"edit-contract","tool":"pacto_edit","cli":"pacto ci"}` {
		t.Errorf("editContract = %s", b)
	}
	b, _ = json.Marshal(NextAction{Verb: "declare", CLI: "pacto declare x"})
	if string(b) != `{"verb":"declare","cli":"pacto declare x"}` {
		t.Errorf("a served-nowhere tool is omitted: %s", b)
	}
}

func TestInferredServiceDetailDeclares(t *testing.T) {
	d, err := goldenWith(t, true).EntityDetail(KindService, string(NewServiceKey(cartName)))
	if err != nil {
		t.Fatal(err)
	}
	if want := (NextAction{Verb: "declare", Tool: "pacto_declare", CLI: "pacto declare cart --namespace shop"}); d.Service.NextAction == nil || *d.Service.NextAction != want {
		t.Errorf("nextAction = %+v", d.Service.NextAction)
	}
}

func TestReplacedInferredServiceHasNoDeclare(t *testing.T) {
	q := NewQuery(handSnap(t, []RawRevision{anaDecl(t)}, nil, true))
	for _, key := range []string{"invoices", string(NewServiceKey(invoicesName))} {
		if d, err := q.EntityDetail(KindService, key); err == nil && d.Service.NextAction != nil {
			t.Errorf("%s: nextAction %+v", key, d.Service.NextAction)
		}
	}
	if d, _ := goldenWith(t, false).EntityDetail(KindService, "orders"); d == nil || d.Service.NextAction != nil {
		t.Errorf("a declared service carries %+v", d)
	}
}

func TestObservedUncontractedRowDeclares(t *testing.T) {
	want := map[string]*NextAction{
		"k8s:shop/Deployment/legacy": {Verb: "declare", Tool: "pacto_declare", CLI: "pacto declare legacy --namespace shop"},
		"cron-x":                     editContract(),
	}
	for _, c := range observedOf(t, observedFleet(t)).ObservedConsumers.Items {
		if !reflect.DeepEqual(c.NextAction, want[c.Caller]) {
			t.Errorf("row %q %+v: nextAction %+v", c.Caller, c.Consumer, c.NextAction)
		}
	}
}

// TestEntityActionsUnchanged: declare is a next action, never an entity
// action; Actions stays navigation only.
func TestEntityActionsUnchanged(t *testing.T) {
	q := goldenWith(t, true)
	for key, want := range map[string][]string{string(NewServiceKey(cartName)): nil, "orders": {"open-graph", "compare", "impact"}} {
		d, err := q.EntityDetail(KindService, key)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(d.Actions, want) {
			t.Errorf("%s actions = %#v, want %#v", key, d.Actions, want)
		}
	}
}

func TestAttentionNoActionOnNonCompliant(t *testing.T) {
	n := 0
	for _, it := range allAttention(t) {
		if it.Code == "NON_COMPLIANT" {
			n++
			if it.NextAction != nil {
				t.Errorf("NON_COMPLIANT %q carries %+v", it.Label, it.NextAction)
			}
		}
	}
	if n == 0 {
		t.Fatal("no NON_COMPLIANT item in the fixtures")
	}
}

func TestAttentionNoActionOnUnknownOrStale(t *testing.T) {
	seen := map[string]bool{}
	for _, it := range allAttention(t) {
		switch it.Category {
		case categoryUnknown, categoryStale, categoryUnresolved:
			seen[it.Category] = true
			if it.NextAction != nil {
				t.Errorf("%s %q carries %+v", it.Code, it.Label, it.NextAction)
			}
		}
	}
	for _, c := range []string{categoryUnknown, categoryStale, categoryUnresolved} {
		if !seen[c] {
			t.Errorf("no %s item in the fixtures", c)
		}
	}
	// A service with no readable contract raises a knowledge item, never an
	// unowned one, and neither carries an action.
	q := NewQuery(&FleetSnapshot{
		Services:  map[ServiceKey]*ServiceRecord{"unread": {Key: "unread", Name: "unread", Revisions: []RevisionKey{"u@1"}}},
		Revisions: map[RevisionKey]*ContractRevision{"u@1": {Key: "u@1", Service: "unread", ServiceKey: "unread"}},
	})
	if l := attentionOf(t, q, AttentionFilter{Category: categoryUnowned}); len(l.Items) != 0 {
		t.Errorf("unreadable service raises unowned items %+v", l.Items)
	}
	k := attentionOf(t, q, AttentionFilter{Category: categoryKnowledge}).Items
	if len(k) == 0 {
		t.Error("unreadable service raises no knowledge item")
	}
	for _, it := range k {
		if it.NextAction != nil {
			t.Errorf("unreadable service's knowledge item carries %+v", it.NextAction)
		}
	}
}

func TestNextActionMatchesNextStep(t *testing.T) {
	for _, it := range allAttention(t) {
		if it.NextAction == nil {
			continue
		}
		if !strings.Contains(it.NextStep, "contract") && !strings.Contains(it.NextStep, "readiness assessment") {
			t.Errorf("%s: edit-contract with next step %q", it.Code, it.NextStep)
		}
	}
}

func TestNextStepNamesNoDeliveryAction(t *testing.T) {
	for _, it := range allAttention(t) {
		s := strings.ToLower(it.NextStep)
		if strings.Contains(s, "rollback") || strings.Contains(s, "roll back") || strings.Contains(s, "plan a") {
			t.Errorf("%s: next step %q names a delivery action", it.Code, it.NextStep)
		}
	}
}

// TestAttentionActionNarrowedWithItem: a restricted view hides the item and
// with it its action; the visible items keep theirs.
func TestAttentionActionNarrowedWithItem(t *testing.T) {
	q := undeclaredFleet(t)
	all := attentionOf(t, q, AttentionFilter{})
	hiddenLabel := "a calls c"
	if !slices.ContainsFunc(all.Items, func(it AttentionItem) bool { return it.Label == hiddenLabel && it.NextAction != nil }) {
		t.Fatalf("fixture lacks %q with an action: %+v", hiddenLabel, all.Items)
	}
	r := attentionOf(t, q.Restrict(anyCluster(func(ns string) bool { return ns != "secret" }), anyClusterKind(nil)), AttentionFilter{})
	if r.Total != len(r.Items) {
		t.Errorf("restricted total %d, items %d", r.Total, len(r.Items))
	}
	for _, it := range r.Items {
		if it.Label == hiddenLabel || it.Service == "c" {
			t.Errorf("restricted view shows %+v", it)
		}
		if want, _ := wantAction(it); !reflect.DeepEqual(it.NextAction, want) {
			t.Errorf("restricted %s carries %+v", it.Code, it.NextAction)
		}
	}
}

// TestAttentionPreviewCarriesNextAction: the overview and owner detail
// previews carry the same action as the list.
func TestAttentionPreviewCarriesNextAction(t *testing.T) {
	q := undeclaredFleet(t)
	list := attentionOf(t, q, AttentionFilter{})
	byLabel := map[string]*NextAction{}
	for _, it := range list.Items {
		byLabel[it.Label] = it.NextAction
	}
	od, err := q.EntityDetail(KindOwner, "team:t")
	if err != nil {
		t.Fatal(err)
	}
	ov := q.Overview()
	for name, items := range map[string][]AttentionItem{"owner detail": od.Owner.Attention.Items, "overview": ov.Attention.Items} {
		n := 0
		for _, it := range items {
			want, ok := byLabel[it.Label]
			if !ok {
				continue
			}
			if want != nil {
				n++
			}
			if !reflect.DeepEqual(it.NextAction, want) {
				t.Errorf("%s %q: %+v, list %+v", name, it.Label, it.NextAction, want)
			}
		}
		if n == 0 {
			t.Errorf("%s preview shares no item with an action with the list", name)
		}
	}
}

// TestAttentionItemJSONKeepsFields: every existing key keeps its name and the
// new one is additive and omitted when empty.
func TestAttentionItemJSONKeepsFields(t *testing.T) {
	it := AttentionItem{Entity: EntityRef{Kind: KindService, Key: "s"}, Service: "s", Label: "l", Severity: severityInfo,
		Code: "c", Category: categoryUnowned, Summary: "x", Reason: "y", Source: "z", NextStep: "n"}
	b, _ := json.Marshal(it)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"entity", "service", "label", "severity", "code", "category", "summary", "reason", "source", "nextStep"} {
		if _, ok := m[k]; !ok {
			t.Errorf("key %q missing from %s", k, b)
		}
	}
	if _, ok := m["nextAction"]; ok {
		t.Errorf("empty nextAction is emitted: %s", b)
	}
	it.NextAction = editContract()
	b, _ = json.Marshal(it)
	if !strings.Contains(string(b), `"nextAction":{"verb":"edit-contract","tool":"pacto_edit","cli":"pacto ci"}`) {
		t.Errorf("nextAction = %s", b)
	}
}

// TestInferredLinesNameDeclare: the text the CLI and the TUI both render for
// an inferred service ends with the command that declares it.
func TestInferredLinesNameDeclare(t *testing.T) {
	d, err := goldenWith(t, true).EntityDetail(KindService, string(NewServiceKey(cartName)))
	if err != nil {
		t.Fatal(err)
	}
	lines := d.Service.InferredLines(d.Meta.AsOf)
	if got := lines[len(lines)-1]; got != "Next: pacto declare cart --namespace shop" {
		t.Errorf("last line = %q", got)
	}
}

func TestKeepServedToolsClearsUnserved(t *testing.T) {
	edit := editContract()
	items := []AttentionItem{{NextAction: edit}, {NextAction: declareAction("cart", "shop")}, {}, {NextAction: &NextAction{Verb: "approve-cluster", CLI: "x"}}}
	KeepServedTools(items, func(tool string) bool { return tool == "pacto_declare" })
	if items[0].NextAction.Tool != "" || items[0].NextAction.CLI != "pacto ci" || items[0].NextAction.Verb != "edit-contract" {
		t.Errorf("unserved: %+v", items[0].NextAction)
	}
	if edit.Tool != "pacto_edit" {
		t.Error("KeepServedTools wrote through to the caller's action")
	}
	if items[1].NextAction.Tool != "pacto_declare" || items[2].NextAction != nil || items[3].NextAction.CLI != "x" {
		t.Errorf("served or empty changed: %+v", items)
	}

	d := &EntityDetail{
		Service:  &ServiceDetailData{NextAction: editContract()},
		Offering: &OfferingDetailData{ObservedConsumers: ObservedConsumersPreview{Items: []ObservedConsumer{{NextAction: editContract()}, {}}}},
		Owner:    &OwnerDetailData{Attention: AttentionPreview{Items: []AttentionItem{{NextAction: editContract()}}}},
		Domain:   &DomainDetailData{Attention: AttentionPreview{Items: []AttentionItem{{NextAction: editContract()}}}},
	}
	d.KeepServedTools(NoTools)
	for _, a := range []*NextAction{d.Service.NextAction, d.Offering.ObservedConsumers.Items[0].NextAction, d.Owner.Attention.Items[0].NextAction, d.Domain.Attention.Items[0].NextAction} {
		if a.Tool != "" || a.CLI == "" {
			t.Errorf("detail action %+v", a)
		}
	}
	(&EntityDetail{}).KeepServedTools(NoTools) // no payload: nothing to clear
}
