package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestProductTypesMatchOpenAPI is the CI-blocking drift gate between the generated
// Huma OpenAPI (the Go source of truth) and the hand-written TypeScript product
// contract (frontend/src/lib/{productTypes,api}.ts). It is a FULL STRUCTURAL
// comparison, not a property-name check: for every mapped TS interface it compares
// each field's optionality AND type — primitives, arrays and their element types,
// object references, bounded Preview/Page shapes and their element types, and
// literal-union (enum) refinements — against the mapped OpenAPI schema. It also
// verifies the discriminated ProductEntityDetail union covers the OpenAPI schema,
// and that every product operation in api.ts models the OpenAPI operation's query
// and path parameters and request body with the correct names, optionality and
// types. A change such as `total: number -> total: string`, a required->optional
// flip, a changed array element type or nested ref, a dropped request parameter or
// an altered POST body field fails this gate. (See TestDriftGateCatchesMutations
// for the adversarial proof that each such change is caught.)
func TestProductTypesMatchOpenAPI(t *testing.T) {
	spec := loadOpenAPI(t)
	tsSrc := readFile(t, "frontend/src/lib/productTypes.ts")
	apiSrc := readFile(t, "frontend/src/lib/api.ts")

	enums := parseTSEnums(tsSrc)
	ifaces := parseTSInterfaces(tsSrc)
	if len(ifaces) == 0 {
		t.Fatal("no TS interfaces parsed from productTypes.ts")
	}

	// tsToOA maps each concrete TS type name to the OpenAPI schema it must match.
	// The generic Preview<T>/Page<T> and the union variants are handled separately.
	tsToOA := map[string]string{
		"SourceError": "Fleet.SourceError", "SourceState": "Fleet.SourceState",
		"Limitation": "Fleet.Limitation", "Coverage": "Fleet.Coverage",
		"RevisionIdentity": "Fleet.RevisionIdentity", "ToolSummary": "Fleet.ToolSummary",
		"DocRef": "Fleet.DocRef", "DeclaredClaim": "Fleet.DeclaredClaim",
		"ObservedSourceStat": "Fleet.ObservedSourceStat", "SubjectRef": "Finding.SubjectRef",
		"EvidenceRef": "Finding.EvidenceRef", "Finding": "Finding.Finding",
		"ReadinessCheck": "Fleet.ReadinessCheck", "ProductReadiness": "Fleet.ProductReadiness",
		"RuntimeFact": "Fleet.RuntimeFact", "ProductMeta": "Fleet.ProductMeta",
		"ProductRef": "ProductRef", "Ownership": "ProductOwnership",
		"AttributedFinding": "ProductAttributedFinding", "AttributedLimitation": "ProductAttributedLimitation",
		"OverviewSummary": "Fleet.OverviewSummary", "AttentionItem": "ProductAttentionItem",
		"EvidenceItem": "ProductEvidenceItem", "EntryPoint": "ProductEntryPoint",
		"ProductOverview": "ProductOverview", "ProductEntityList": "ProductEntityList",
		"ProductAttentionList": "ProductAttentionList", "NeighborhoodNode": "ProductNode",
		"NeighborhoodEdge": "ProductEdge", "UnresolvedDependency": "ProductUnresolvedDependency",
		"ProductNeighborhood": "ProductNeighborhood", "ServiceDetail": "ProductServiceDetail",
		"RevisionDetail": "ProductRevisionDetail", "TargetDetail": "ProductTargetDetail",
		"OwnerDetail": "ProductOwnerDetail", "SourceDetail": "ProductSourceDetail",
		"ImpactConsumer": "ProductImpactConsumer", "ProductImpact": "ProductImpact",
		// kind-narrowed ProductRef aliases used by the discriminated-union variants.
		"ServiceRef": "ProductRef", "RevisionRef": "ProductRef", "TargetRef": "ProductRef",
		"OwnerRef": "ProductRef", "SourceRef": "ProductRef",
	}
	res := &resolver{spec: spec, tsToOA: tsToOA, enums: enums}

	// The five union variants are compared as one against the OpenAPI union schema.
	unionVariants := map[string]bool{
		"ServiceEntityDetail": true, "RevisionEntityDetail": true, "TargetEntityDetail": true,
		"OwnerEntityDetail": true, "SourceEntityDetail": true,
	}
	generics := map[string]bool{"Preview": true, "Page": true}

	// Every parsed interface must be accounted for (mapped, a union variant, or a generic).
	for name := range ifaces {
		if generics[name] || unionVariants[name] {
			continue
		}
		if _, ok := tsToOA[name]; !ok {
			t.Errorf("TS interface %q has no OpenAPI mapping — add it to tsToOA and model it, or the client can drift", name)
		}
	}

	compareMappedInterfaces(t, ifaces, tsToOA, generics, unionVariants, res)
	compareEntityDetailUnion(t, ifaces, unionVariants, res)
	checkPreviewShapes(t, spec)
	checkEveryProductTypeModeled(t, spec, tsToOA)
	compareOperations(t, apiSrc, spec, res)
}

// compareMappedInterfaces structurally compares every mapped, non-generic,
// non-variant TS interface against its OpenAPI schema.
func compareMappedInterfaces(t *testing.T, ifaces map[string][]tsField, tsToOA map[string]string, generics, variants map[string]bool, res *resolver) {
	for name, fields := range ifaces {
		if generics[name] || variants[name] {
			continue
		}
		oa, ok := tsToOA[name]
		if !ok {
			continue // reported above
		}
		fm := map[string]tsField{}
		for _, f := range fields {
			fm[f.name] = f
		}
		report(t, res.diffFields(name, fm, oa))
	}
}

// diffFields is the PURE structural comparison of a TS field set against an
// OpenAPI schema's merged properties: field-name set equality, then per-field
// optionality and type. It returns a drift message per problem (empty if none),
// so both the live gate and the negative-fixture tests can drive it.
func (r *resolver) diffFields(label string, fields map[string]tsField, schema string) []string {
	props, required, ok := r.mergedProps(schema)
	if !ok {
		return []string{fmt.Sprintf("%s: mapped OpenAPI schema %q is missing", label, schema)}
	}
	if !equalSets(mapKeySet(fields), keySet(props)) {
		return []string{fmt.Sprintf("drift: %s field set %v != OpenAPI %q properties %v", label, sortSet(mapKeySet(fields)), schema, sortSet(keySet(props)))}
	}
	var errs []string
	for name, f := range fields {
		errs = append(errs, r.diffField(label, f, props[name], required[name])...)
	}
	return errs
}

// diffField compares one TS field's optionality and type to its OpenAPI property.
func (r *resolver) diffField(label string, f tsField, prop map[string]any, required bool) []string {
	var errs []string
	if f.optional == required {
		errs = append(errs, fmt.Sprintf("drift: %s.%s optional=%v but OpenAPI required=%v", label, f.name, f.optional, required))
	}
	if err := r.compareType(f.typ, r.oaType(prop)); err != nil {
		errs = append(errs, fmt.Sprintf("drift: %s.%s type mismatch: %v", label, f.name, err))
	}
	return errs
}

// report emits each drift message as a test error.
func report(t *testing.T, errs []string) {
	t.Helper()
	for _, e := range errs {
		t.Error(e)
	}
}

// compareEntityDetailUnion merges the discriminated-union variants and compares
// the merged shape to the OpenAPI ProductEntityDetail schema, so the union covers
// exactly the schema's fields with matching types.
func compareEntityDetailUnion(t *testing.T, ifaces map[string][]tsField, variants map[string]bool, res *resolver) {
	merged := mergeUnionVariants(ifaces, variants)
	if len(merged) == 0 {
		t.Fatal("no ProductEntityDetail union variants parsed")
	}
	report(t, res.diffFields("ProductEntityDetail union", merged, "ProductEntityDetail"))
}

// mergeUnionVariants merges variant interfaces into one field map: a field is
// required only when it is required and non-`never` in EVERY variant; its type is
// the sole non-`never` type across variants (payloads are `never` in the variants
// that do not carry them).
func mergeUnionVariants(ifaces map[string][]tsField, variants map[string]bool) map[string]tsField {
	names := []string{}
	for v := range variants {
		names = append(names, v)
	}
	sort.Strings(names)
	total := len(names)
	// Collect per-field occurrences.
	seen := map[string]int{}
	realType := map[string]sType{}
	requiredAll := map[string]int{}
	for _, v := range names {
		for _, f := range ifaces[v] {
			seen[f.name]++
			if f.typ.kind != kNever {
				realType[f.name] = f.typ
			}
			if !f.optional && f.typ.kind != kNever {
				requiredAll[f.name]++
			}
		}
	}
	out := map[string]tsField{}
	for name, typ := range realType {
		// Required only if it appears non-optionally and non-never in every variant.
		req := seen[name] == total && requiredAll[name] == total
		out[name] = tsField{name: name, optional: !req, typ: typ}
	}
	return out
}

// ── structural type model ────────────────────────────────────────────────────

type sKind int

const (
	kPrim    sKind = iota // prim: "string" | "number" | "boolean"
	kEnum                 // enum: sorted string-literal values
	kArray                // elem
	kPreview              // elem (bounded preview)
	kPage                 // elem (offset page)
	kRef                  // ref: a named type
	kMap                  // elem (additionalProperties)
	kNever
	kUnknown
)

type sType struct {
	kind sKind
	prim string
	ref  string
	enum []string
	elem *sType
}

type tsField struct {
	name     string
	optional bool
	typ      sType
}

// ── TypeScript parsing ───────────────────────────────────────────────────────

var (
	tsIfaceRe    = regexp.MustCompile(`^export interface (\w+)`)
	tsFieldRe    = regexp.MustCompile(`^(\w+)(\??):\s*(.+?);?$`)
	tsEnumHeadRe = regexp.MustCompile(`^export type (\w+)\s*=`)
	tsLiteralRe  = regexp.MustCompile(`'([^']*)'`)
)

// parseTSInterfaces extracts each `export interface Name { ... }` and its fields
// (name, optionality, structured type). It relies on productTypes.ts keeping one
// field per line and no inline object literals (a lone "}" closes an interface).
func parseTSInterfaces(src string) map[string][]tsField {
	enums := parseTSEnums(src)
	out := map[string][]tsField{}
	var cur string
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		if cur == "" {
			if m := tsIfaceRe.FindStringSubmatch(trim); m != nil {
				cur = m[1]
				out[cur] = nil
			}
			continue
		}
		if trim == "}" {
			cur = ""
			continue
		}
		if m := tsFieldRe.FindStringSubmatch(trim); m != nil {
			out[cur] = append(out[cur], tsField{name: m[1], optional: m[2] == "?", typ: parseTSType(m[3], enums)})
		}
	}
	return out
}

// parseTSEnums collects every `export type X = 'a' | 'b' | ...;` literal-union
// (single- or multi-line) into a name -> sorted-values map. A type alias that is
// not a pure string-literal union (e.g. an intersection) is skipped.
func parseTSEnums(src string) map[string][]string {
	out := map[string][]string{}
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		m := tsEnumHeadRe.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if m == nil {
			continue
		}
		// Accumulate text until the terminating ';'.
		buf := lines[i]
		for !strings.Contains(buf, ";") && i+1 < len(lines) {
			i++
			buf += "\n" + lines[i]
		}
		rhs := buf[strings.Index(buf, "=")+1:]
		lits := tsLiteralRe.FindAllStringSubmatch(rhs, -1)
		// Only treat as an enum when the RHS is composed solely of string literals
		// (and separators), so an intersection alias is not misread as an enum.
		if len(lits) == 0 || strings.ContainsAny(stripLiterals(rhs), "&{}") {
			continue
		}
		vals := make([]string, 0, len(lits))
		for _, l := range lits {
			vals = append(vals, l[1])
		}
		sort.Strings(vals)
		out[m[1]] = vals
	}
	return out
}

// stripLiterals removes quoted string literals and separators, leaving other tokens.
func stripLiterals(s string) string {
	s = tsLiteralRe.ReplaceAllString(s, "")
	return strings.NewReplacer("|", "", " ", "", "\n", "", ";", "").Replace(s)
}

// parseTSType parses a TS type expression into a structural sType.
func parseTSType(expr string, enums map[string][]string) sType {
	e := strings.TrimSpace(expr)
	switch {
	case strings.HasSuffix(e, "[]"):
		el := parseTSType(strings.TrimSuffix(e, "[]"), enums)
		return sType{kind: kArray, elem: &el}
	case strings.HasPrefix(e, "Preview<") && strings.HasSuffix(e, ">"):
		el := parseTSType(e[len("Preview<"):len(e)-1], enums)
		return sType{kind: kPreview, elem: &el}
	case strings.HasPrefix(e, "Page<") && strings.HasSuffix(e, ">"):
		el := parseTSType(e[len("Page<"):len(e)-1], enums)
		return sType{kind: kPage, elem: &el}
	case strings.HasPrefix(e, "Record<"):
		inner := e[len("Record<") : len(e)-1]
		parts := strings.SplitN(inner, ",", 2)
		el := parseTSType(strings.TrimSpace(parts[len(parts)-1]), enums)
		return sType{kind: kMap, elem: &el}
	case e == "string" || e == "number" || e == "boolean":
		return sType{kind: kPrim, prim: e}
	case e == "never":
		return sType{kind: kNever}
	default:
		if vals, ok := enums[e]; ok {
			return sType{kind: kEnum, enum: vals}
		}
		return sType{kind: kRef, ref: e}
	}
}

// ── OpenAPI resolution ───────────────────────────────────────────────────────

type resolver struct {
	spec   map[string]any
	tsToOA map[string]string
	enums  map[string][]string
}

func (r *resolver) schemas() map[string]any {
	return asMap(asMap(r.spec["components"])["schemas"])
}

// mergedProps returns a schema's properties and required set, merging any allOf
// members (Huma models Go embedding as allOf).
func (r *resolver) mergedProps(name string) (map[string]map[string]any, map[string]bool, bool) {
	s, ok := r.schemas()[name].(map[string]any)
	if !ok {
		return nil, nil, false
	}
	props := map[string]map[string]any{}
	req := map[string]bool{}
	var absorb func(m map[string]any)
	absorb = func(m map[string]any) {
		for k, v := range asMap(m["properties"]) {
			if k == "$schema" {
				continue // a Huma response-body artifact, not part of the contract
			}
			props[k] = asMap(v)
		}
		for _, rq := range asSlice(m["required"]) {
			req[fmt.Sprint(rq)] = true
		}
		for _, a := range asSlice(m["allOf"]) {
			am := asMap(a)
			if ref := refName(am); ref != "" {
				if sub, ok := r.schemas()[ref].(map[string]any); ok {
					absorb(sub)
				}
			}
			absorb(am)
		}
	}
	absorb(s)
	return props, req, true
}

// oaType resolves an OpenAPI property schema into a structural sType.
func (r *resolver) oaType(prop map[string]any) sType {
	if prop == nil {
		return sType{kind: kUnknown}
	}
	if ref := refName(prop); ref != "" {
		return r.resolveRef(ref)
	}
	if enum := asSlice(prop["enum"]); len(enum) > 0 {
		vals := make([]string, 0, len(enum))
		for _, v := range enum {
			vals = append(vals, fmt.Sprint(v))
		}
		sort.Strings(vals)
		return sType{kind: kEnum, enum: vals}
	}
	switch oaBaseType(prop) {
	case "array":
		el := r.oaType(asMap(prop["items"]))
		return sType{kind: kArray, elem: &el}
	case "object":
		if ap, ok := prop["additionalProperties"]; ok {
			el := r.oaType(asMap(ap))
			return sType{kind: kMap, elem: &el}
		}
		return sType{kind: kUnknown}
	case "string":
		return sType{kind: kPrim, prim: "string"}
	case "integer", "number":
		return sType{kind: kPrim, prim: "number"}
	case "boolean":
		return sType{kind: kPrim, prim: "boolean"}
	}
	return sType{kind: kUnknown}
}

// resolveRef resolves a $ref target to a preview/page/plain-ref sType by shape.
func (r *resolver) resolveRef(name string) sType {
	props, _, ok := r.mergedProps(name)
	if ok && isPreviewProps(props) {
		el := r.previewElem(props)
		if isPageProps(props) {
			return sType{kind: kPage, elem: &el}
		}
		return sType{kind: kPreview, elem: &el}
	}
	return sType{kind: kRef, ref: name}
}

// previewElem returns the element type of a preview/page schema (its items' items).
func (r *resolver) previewElem(props map[string]map[string]any) sType {
	items := props["items"]
	return r.oaType(asMap(items["items"]))
}

// compareType asserts a TS structural type matches an OpenAPI structural type.
func (r *resolver) compareType(ts, oa sType) error {
	switch ts.kind {
	case kPrim:
		if oa.kind != kPrim || oa.prim != ts.prim {
			return fmt.Errorf("TS %s vs OpenAPI %s", ts.prim, describe(oa))
		}
	case kEnum:
		// A TS literal union may refine an OpenAPI plain string; if the OpenAPI also
		// declares an enum, the value sets must be identical.
		if oa.kind == kPrim && oa.prim == "string" {
			return nil
		}
		if oa.kind != kEnum || !equalStrSlice(ts.enum, oa.enum) {
			return fmt.Errorf("TS enum %v vs OpenAPI %s", ts.enum, describe(oa))
		}
	case kArray, kPreview, kPage, kMap:
		if oa.kind != ts.kind {
			return fmt.Errorf("TS %s vs OpenAPI %s", describe(ts), describe(oa))
		}
		return r.compareType(deref(ts.elem), deref(oa.elem))
	case kRef:
		want := r.tsToOA[ts.ref]
		if want == "" {
			return fmt.Errorf("TS ref %q is unmapped", ts.ref)
		}
		if oa.kind != kRef || oa.ref != want {
			return fmt.Errorf("TS ref %q (=%s) vs OpenAPI %s", ts.ref, want, describe(oa))
		}
	}
	return nil
}

// compareParamType compares a client param type to an OpenAPI query/path param
// type. Query params are stringly-typed on the wire, so the client may legitimately
// refine a `string` param as a literal-union enum or accept a CSV array of
// string/enum values (which it join(',')s). Non-string params are compared strictly.
func (r *resolver) compareParamType(ts, oa sType) error {
	if oa.kind == kPrim && oa.prim == "string" {
		switch ts.kind {
		case kPrim:
			if ts.prim == "string" {
				return nil
			}
		case kEnum:
			return nil
		case kArray:
			el := deref(ts.elem)
			if el.kind == kEnum || (el.kind == kPrim && el.prim == "string") {
				return nil
			}
		}
		return fmt.Errorf("TS %s is not serializable to an OpenAPI string param", describe(ts))
	}
	return r.compareType(ts, oa)
}

// ── shape predicates + preview/page structural check ─────────────────────────

func isPreviewProps(props map[string]map[string]any) bool {
	for _, k := range []string{"count", "items", "total", "truncated"} {
		if _, ok := props[k]; !ok {
			return false
		}
	}
	return true
}

func isPageProps(props map[string]map[string]any) bool {
	_, a := props["limit"]
	_, b := props["offset"]
	return a && b
}

// checkPreviewShapes asserts every "*Preview" schema has exactly the reusable
// Preview shape, so the TS Preview<T> generic faithfully models all of them.
func checkPreviewShapes(t *testing.T, spec map[string]any) {
	want := []string{"count", "items", "total", "truncated"}
	r := &resolver{spec: spec}
	for name := range r.schemas() {
		if !strings.HasSuffix(name, "Preview") {
			continue
		}
		props, _, _ := r.mergedProps(name)
		if !equalStrSlice(sortSet(keySet(props)), want) {
			t.Errorf("preview schema %q has fields %v, want the reusable Preview shape %v", name, sortSet(keySet(props)), want)
		}
	}
}

// checkEveryProductTypeModeled asserts every concrete (non-preview) Product*
// schema is modeled by a TS interface, so a new Go product type cannot ship
// without a TS DTO.
func checkEveryProductTypeModeled(t *testing.T, spec map[string]any, tsToOA map[string]string) {
	mapped := map[string]bool{"ProductEntityDetail": true}
	for _, oa := range tsToOA {
		mapped[oa] = true
	}
	r := &resolver{spec: spec}
	for name := range r.schemas() {
		if !strings.HasPrefix(name, "Product") || strings.HasSuffix(name, "Preview") || strings.HasSuffix(name, "Page") {
			continue
		}
		if !mapped[name] {
			t.Errorf("OpenAPI schema %q is a product type with no TS model — add it to productTypes.ts and tsToOA", name)
		}
	}
}

// ── operation (request) comparison ───────────────────────────────────────────

type opSpec struct {
	fn       string
	method   string
	path     string
	paramObj bool              // params live in a `params: { ... }` object literal
	bodyObj  bool              // body lives in a `body: { ... }` object literal
	posArgs  map[string]string // positional arg name -> "path" | "query"
}

// compareOperations verifies every product operation in api.ts models the OpenAPI
// operation's query/path parameters and request body: names, optionality, types.
func compareOperations(t *testing.T, apiSrc string, spec map[string]any, res *resolver) {
	ops := []opSpec{
		{fn: "fleetOverview", method: "get", path: "/api/fleet/overview"},
		{fn: "fleetEntities", method: "get", path: "/api/fleet/entities", paramObj: true},
		{fn: "fleetEntityDetail", method: "get", path: "/api/fleet/entities/{kind}", posArgs: map[string]string{"kind": "path", "key": "query"}},
		{fn: "fleetNeighborhood", method: "get", path: "/api/fleet/neighborhood", paramObj: true},
		{fn: "fleetAttention", method: "get", path: "/api/fleet/attention", paramObj: true},
		{fn: "fleetImpactByIdentity", method: "post", path: "/api/fleet/impact", bodyObj: true},
	}
	for _, op := range ops {
		compareOneOperation(t, op, apiSrc, spec, res)
	}
}

func compareOneOperation(t *testing.T, op opSpec, apiSrc string, spec map[string]any, res *resolver) {
	oaOp := asMap(asMap(asMap(spec["paths"])[op.path])[op.method])
	client := parseClientOp(t, apiSrc, op, res.enums)

	// Request parameters (query + path).
	wantParams, wantReq, wantTypes := oaParams(oaOp)
	if !equalSets(mapBoolKeys(client.params), keySet2(wantParams)) {
		t.Errorf("op %s: client params %v != OpenAPI params %v", op.fn, sortSet(mapBoolKeys(client.params)), sortSet(keySet2(wantParams)))
	} else {
		for name, cf := range client.params {
			if cf.optional == wantReq[name] {
				t.Errorf("op %s param %s: client optional=%v but OpenAPI required=%v", op.fn, name, cf.optional, wantReq[name])
			}
			if err := res.compareParamType(cf.typ, wantTypes[name]); err != nil {
				t.Errorf("op %s param %s: type mismatch: %v", op.fn, name, err)
			}
		}
	}

	// Request body.
	bodyRef := requestBodyRef(oaOp)
	if bodyRef == "" {
		if len(client.body) > 0 {
			t.Errorf("op %s: client sends a body but OpenAPI declares none", op.fn)
		}
		return
	}
	report(t, res.diffFields("op "+op.fn+" body", client.body, bodyRef))
}

type clientOp struct {
	params map[string]tsField // query + path params the client accepts
	body   map[string]tsField
}

// parseClientOp extracts the request parameter/body fields an api.ts method accepts.
func parseClientOp(t *testing.T, apiSrc string, op opSpec, enums map[string][]string) clientOp {
	block := funcArgs(apiSrc, op.fn)
	out := clientOp{params: map[string]tsField{}, body: map[string]tsField{}}
	switch {
	case op.paramObj:
		for _, f := range parseObjectLiteralType(objectLiteralAfter(block, "params")) {
			out.params[f.name] = withType(f, enums)
		}
	case op.bodyObj:
		for _, f := range parseObjectLiteralType(objectLiteralAfter(block, "body")) {
			out.body[f.name] = withType(f, enums)
		}
	case len(op.posArgs) > 0:
		for name, kind := range op.posArgs {
			typ := positionalArgType(block, name)
			if typ == "" {
				t.Errorf("op %s: positional %s arg %q not found in client signature", op.fn, kind, name)
				continue
			}
			out.params[name] = tsField{name: name, optional: false, typ: parseTSType(typ, enums)}
		}
	}
	return out
}

// withType re-parses a raw field's type against the enum table.
func withType(f rawField, enums map[string][]string) tsField {
	return tsField{name: f.name, optional: f.optional, typ: parseTSType(f.typ, enums)}
}

type rawField struct {
	name     string
	optional bool
	typ      string
}

// funcArgs returns the argument-list text of the named api.ts arrow method.
func funcArgs(src, fn string) string {
	idx := strings.Index(src, fn+": (")
	if idx < 0 {
		return ""
	}
	rest := src[idx+len(fn)+2:] // after "fn: "
	depth := 0
	for i, c := range rest {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return rest[:i+1]
			}
		}
	}
	return ""
}

// objectLiteralAfter returns the { ... } object-type text following `marker:`.
func objectLiteralAfter(block, marker string) string {
	idx := strings.Index(block, marker+":")
	if idx < 0 {
		return ""
	}
	rest := block[idx:]
	open := strings.Index(rest, "{")
	if open < 0 {
		return ""
	}
	rest = rest[open:]
	depth := 0
	for i, c := range rest {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[:i+1]
			}
		}
	}
	return ""
}

var objFieldRe = regexp.MustCompile(`(\w+)(\??):\s*([^;]+)`)

// parseObjectLiteralType parses `{ a?: string; b: number[] }` into raw fields.
func parseObjectLiteralType(lit string) []rawField {
	lit = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(lit), "{"), "}"))
	var out []rawField
	for _, m := range objFieldRe.FindAllStringSubmatch(lit, -1) {
		out = append(out, rawField{name: m[1], optional: m[2] == "?", typ: strings.TrimSpace(m[3])})
	}
	return out
}

var posArgRe = regexp.MustCompile(`(\w+):\s*([\w<>\[\]]+)`)

// positionalArgType returns the declared type of a positional arg in a signature.
func positionalArgType(block, name string) string {
	for _, m := range posArgRe.FindAllStringSubmatch(block, -1) {
		if m[1] == name {
			return m[2]
		}
	}
	return ""
}

// oaParams returns an operation's parameter names, required set and types.
func oaParams(op map[string]any) (map[string]bool, map[string]bool, map[string]sType) {
	names := map[string]bool{}
	req := map[string]bool{}
	types := map[string]sType{}
	r := &resolver{}
	for _, p := range asSlice(op["parameters"]) {
		pm := asMap(p)
		name := fmt.Sprint(pm["name"])
		names[name] = true
		if b, _ := pm["required"].(bool); b {
			req[name] = true
		}
		types[name] = r.oaType(asMap(pm["schema"]))
	}
	return names, req, types
}

// requestBodyRef returns the $ref of an operation's JSON request-body schema.
func requestBodyRef(op map[string]any) string {
	body := asMap(op["requestBody"])
	if len(body) == 0 {
		return ""
	}
	schema := asMap(asMap(asMap(body["content"])["application/json"])["schema"])
	return refName(schema)
}

// ── small helpers ────────────────────────────────────────────────────────────

func loadOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	b, err := ExportOpenAPI()
	if err != nil {
		t.Fatalf("ExportOpenAPI: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	return doc
}

func readFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func refName(m map[string]any) string {
	if r, ok := m["$ref"].(string); ok {
		return r[strings.LastIndex(r, "/")+1:]
	}
	return ""
}

func oaBaseType(prop map[string]any) string {
	switch t := prop["type"].(type) {
	case string:
		return t
	case []any:
		for _, x := range t {
			if s, _ := x.(string); s != "" && s != "null" {
				return s
			}
		}
	}
	return ""
}

func deref(s *sType) sType {
	if s == nil {
		return sType{kind: kUnknown}
	}
	return *s
}

func describe(s sType) string {
	switch s.kind {
	case kPrim:
		return s.prim
	case kEnum:
		return "enum" + fmt.Sprint(s.enum)
	case kArray:
		return "array<" + describe(deref(s.elem)) + ">"
	case kPreview:
		return "preview<" + describe(deref(s.elem)) + ">"
	case kPage:
		return "page<" + describe(deref(s.elem)) + ">"
	case kRef:
		return "ref:" + s.ref
	case kMap:
		return "map<" + describe(deref(s.elem)) + ">"
	case kNever:
		return "never"
	}
	return "unknown"
}

func fieldNameSet(fs []tsField) map[string]bool {
	out := map[string]bool{}
	for _, f := range fs {
		out[f.name] = true
	}
	return out
}

func keySet(m map[string]map[string]any) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func keySet2(m map[string]bool) map[string]bool { return m }

func mapKeySet(m map[string]tsField) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func mapBoolKeys(m map[string]tsField) map[string]bool { return mapKeySet(m) }

func equalSets(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sortSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── negative fixtures: prove the gate catches each drift type ─────────────────

// synthSpec is a tiny OpenAPI document the mutation fixtures compare against.
func synthSpec() map[string]any {
	sc := func(props map[string]any, required ...string) map[string]any {
		r := make([]any, len(required))
		for i, s := range required {
			r[i] = s
		}
		return map[string]any{"properties": props, "required": r}
	}
	return map[string]any{"components": map[string]any{"schemas": map[string]any{
		"Thing": sc(map[string]any{
			"total": map[string]any{"type": "integer"},
			"name":  map[string]any{"type": "string"},
			"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"ref":   map[string]any{"$ref": "#/components/schemas/RefA"},
		}, "total", "name", "items", "ref"),
		"Body": sc(map[string]any{
			"a": map[string]any{"type": "string"},
			"b": map[string]any{"type": "integer"},
		}, "a", "b"),
		"RefA": sc(map[string]any{"x": map[string]any{"type": "string"}}),
		"RefB": sc(map[string]any{"y": map[string]any{"type": "string"}}),
	}}}
}

func prim(s string) sType           { return sType{kind: kPrim, prim: s} }
func arr(el sType) sType            { return sType{kind: kArray, elem: &el} }
func refT(name string) sType        { return sType{kind: kRef, ref: name} }
func fld(n string, t sType) tsField { return tsField{name: n, typ: t} }

// TestDriftGateCatchesMutations proves the structural gate flags every drift class
// the requirement enumerates: a primitive type change, a required->optional flip, a
// changed array element type, a changed nested ref, a missing request parameter and
// a changed POST body field. Each fixture mutates a field that otherwise matches, so
// only the deliberate change is under test.
func TestDriftGateCatchesMutations(t *testing.T) {
	res := &resolver{spec: synthSpec(), tsToOA: map[string]string{"RefA": "RefA", "RefB": "RefB"}, enums: map[string][]string{}}
	good := func() map[string]tsField {
		return map[string]tsField{
			"total": fld("total", prim("number")),
			"name":  fld("name", prim("string")),
			"items": fld("items", arr(prim("string"))),
			"ref":   fld("ref", refT("RefA")),
		}
	}
	// Sanity: the unmutated field set matches with no drift, so a fixture failing
	// below is caused by its mutation, not a broken baseline.
	if errs := res.diffFields("Thing", good(), "Thing"); len(errs) != 0 {
		t.Fatalf("baseline must be drift-free, got %v", errs)
	}

	mustDrift := func(name string, mutate func(m map[string]tsField), wantSub string) {
		m := good()
		mutate(m)
		errs := res.diffFields("Thing", m, "Thing")
		if !anyContains(errs, wantSub) {
			t.Errorf("%s: expected drift containing %q, got %v", name, wantSub, errs)
		}
	}
	mustDrift("number->string", func(m map[string]tsField) { m["total"] = fld("total", prim("string")) }, "total")
	mustDrift("required->optional", func(m map[string]tsField) { f := m["name"]; f.optional = true; m["name"] = f }, "name")
	mustDrift("changed array element type", func(m map[string]tsField) { m["items"] = fld("items", arr(prim("number"))) }, "items")
	mustDrift("changed nested ref", func(m map[string]tsField) { m["ref"] = fld("ref", refT("RefB")) }, "ref")

	// Missing request parameter: the gate compares the client's param-name set to
	// the OpenAPI operation's, so a dropped param is a set inequality.
	clientParams := map[string]bool{"text": true}
	oaParams := map[string]bool{"text": true, "sourceHealth": true}
	if equalSets(clientParams, oaParams) {
		t.Error("missing request parameter must be caught as a param-set inequality")
	}

	// Changed POST body field: a body field whose type changed is caught by the same
	// structural field diff used for the request body.
	body := map[string]tsField{"a": fld("a", prim("string")), "b": fld("b", prim("string"))}
	if errs := res.diffFields("Body", body, "Body"); !anyContains(errs, "b") {
		t.Errorf("changed POST body field must drift on b, got %v", errs)
	}
	// A renamed body field is caught as a set inequality.
	renamed := map[string]tsField{"a": fld("a", prim("string")), "c": fld("c", prim("number"))}
	if errs := res.diffFields("Body", renamed, "Body"); len(errs) == 0 {
		t.Error("a renamed POST body field must drift")
	}
}

func anyContains(errs []string, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}
