package diff

import (
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"path"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// specRefs resolves the $refs of one interface spec diff, so a schema defined
// under components is judged where an operation or a message uses it.
//
// Each usage is judged in its own position, but a pair (the old and new
// file#pointer a ref resolves to) is walked once per position: a second usage
// in the same position finds it in walked and only adds itself to uses. That
// keeps the walk linear in the spec, and a recursive schema, which meets its
// own pair again, stops there. merge then folds the per-position results of
// one edit into a single change, so a component used in fifty places is one
// change, graded for the worst place, whose reason names them.
type specRefs struct {
	side   [2]*refSide
	walked map[string]bool     // pair, position: already diffed
	uses   map[string][]string // pair: every usage, in walk order
	label  map[string]string   // pair: the component name a reason shows
}

// refSide is one revision of the spec: the bundle it lives in and the files
// its refs reach, each decoded once.
type refSide struct {
	fsys  fs.FS
	root  string
	files map[string]any
	used  map[string]bool // section/name of every component reached from the root sections
}

func newSpecRefs(oldFS, newFS fs.FS, oldPath, newPath string) *specRefs {
	side := func(fsys fs.FS, root string) *refSide {
		return &refSide{fsys: fsys, root: root, files: map[string]any{}, used: map[string]bool{}}
	}
	return &specRefs{
		side:   [2]*refSide{side(oldFS, oldPath), side(newFS, newPath)},
		walked: map[string]bool{}, uses: map[string][]string{}, label: map[string]string{},
	}
}

// at places a walk at the given JSON pointer of the spec file in each revision.
func (r *specRefs) at(ptr string) *refAt {
	return &refAt{r, [2]string{r.side[0].root + "#" + ptr, r.side[1].root + "#" + ptr}}
}

// refAt is where a JSON walk stands in each revision, as file#pointer, so a
// $ref met there resolves against the right file. A nil *refAt walks without
// resolving anything, which is how a configuration schema is compared.
type refAt struct {
	refs *specRefs
	loc  [2]string
}

var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

func (a *refAt) child(seg string) *refAt {
	if a == nil {
		return nil
	}
	seg = pointerEscaper.Replace(seg)
	return &refAt{a.refs, [2]string{a.loc[0] + "/" + seg, a.loc[1] + "/" + seg}}
}

// fresh is a walk at the given locations that has diffed nothing yet.
func (a *refAt) fresh(oldLoc, newLoc string) *refAt {
	r := *a.refs
	r.walked = map[string]bool{}
	return &refAt{&r, [2]string{oldLoc, newLoc}}
}

// hasRef reports whether either node carries a $ref this walk follows.
func (a *refAt) hasRef(old, new any) bool {
	return a != nil && (a.refs.side[0].follows(a.loc[0], refOf(old)) || a.refs.side[1].follows(a.loc[1], refOf(new)))
}

var positionNames = map[schemaDirection]string{dirRequest: "request", dirResponse: "response", dirRecord: "record"}

// diffRef compares two nodes at least one of which is a $ref, by comparing
// what each resolves to. The ref text itself never counts: a component
// renamed with identical content is no change, and a ref pointed at a
// different schema is graded by the structural difference.
func (a *refAt) diffRef(dir schemaDirection, prefix string, old, new any) []Change {
	r := a.refs
	o, oLoc, oErr := r.side[0].deref(a.loc[0], old)
	n, nLoc, nErr := r.side[1].deref(a.loc[1], new)
	if oErr != nil || nErr != nil {
		return unresolvedRef(prefix, old, new, oErr != nil, nErr != nil)
	}
	oRef, nRef := oLoc != a.loc[0], nLoc != a.loc[1]
	// Keywords beside a $ref belong to the usage, not the component.
	changes := diffJSONObjects(dir, a, prefix, siblings(old, oRef), siblings(new, nRef))

	pair := oLoc + "\n" + nLoc
	if _, ok := r.label[pair]; !ok {
		r.label[pair] = r.labelOf(oLoc, nLoc, oRef, nRef)
	}
	r.uses[pair] = append(r.uses[pair], positionNames[dir]+" at "+prefix)
	// A component inside a response header is capped the way the header is,
	// so it is judged apart from the same component in the body.
	walk := fmt.Sprintf("%s\n%d\n%t", pair, dir, inResponseHeaders(prefix))
	if r.walked[walk] {
		return changes
	}
	r.walked[walk] = true
	for _, c := range diffJSON(dir, &refAt{r, [2]string{oLoc, nLoc}}, prefix, o, n) {
		if c.refPair == "" {
			c.refPair, c.refRel = pair, strings.TrimPrefix(c.Path, prefix)
		}
		changes = append(changes, c)
	}
	return changes
}

// unresolvedRef reports a $ref Pacto could not follow. A target it cannot
// read is a missing observation, so the change is POTENTIAL_BREAKING and never
// a confirmed break. Only the same ref failing on both sides shows no change
// Pacto can see: the same ref text that resolved before and no longer does
// (a component renamed, a file dropped from the bundle) is reported.
func unresolvedRef(prefix string, old, new any, oldErr, newErr bool) []Change {
	if oldErr && newErr && refOf(old) == refOf(new) {
		return nil
	}
	ref, rev := refOf(old), "old"
	if newErr {
		ref, rev = refOf(new), "new"
	}
	return []Change{{
		Path: prefix, Type: Modified, OldValue: jsonSafe(old), NewValue: jsonSafe(new),
		Classification: PotentialBreaking,
		Reason: fmt.Sprintf("%s: Pacto could not resolve $ref %s in the %s revision (it reads only files inside the bundle), so it cannot tell whether this change breaks anybody",
			prefix, ref, rev),
	}}
}

func (r *specRefs) labelOf(oLoc, nLoc string, oRef, nRef bool) string {
	o, n := r.side[0].display(oLoc), r.side[1].display(nLoc)
	switch {
	case oRef && nRef && o != n:
		return n + ", was " + o
	case nRef:
		return n
	}
	return o
}

// merge folds the changes one component edit produced in different positions
// into one change carrying the worst grade and the path where it applies, and
// names the component and its usages in the reason.
func (r *specRefs) merge(changes []Change) []Change {
	var out []Change
	first := map[string]int{}
	for _, c := range changes {
		key := c.refPair + "\n" + c.refRel
		i, seen := first[key]
		switch {
		case c.refPair == "":
			out = append(out, c)
		case !seen:
			first[key] = len(out)
			out = append(out, c)
		case c.Classification > out[i].Classification:
			out[i] = c
		}
	}
	for i := range out {
		c := &out[i]
		if c.refPair == "" {
			continue
		}
		c.Reason += " (in " + r.label[c.refPair]
		if uses := r.uses[c.refPair]; len(uses) > 1 {
			// ponytail: three usages named, the rest counted; a reason is read in a terminal.
			if len(uses) > 3 {
				uses = append(uses[:3:3], fmt.Sprintf("%d more", len(uses)-3))
			}
			c.Reason += "; used as " + strings.Join(uses, ", ")
		}
		c.Reason += ")"
		c.refPair, c.refRel = "", ""
	}
	return out
}

func refOf(node any) string {
	m, _ := node.(map[string]any)
	ref, _ := m["$ref"].(string)
	return ref
}

// siblings returns the keywords written beside the $ref of a resolved node.
func siblings(node any, resolved bool) map[string]any {
	if !resolved {
		return nil
	}
	m := maps.Clone(node.(map[string]any))
	delete(m, "$ref")
	return m
}

// links are the sections of a spec file the diff compares at their own place.
// A ref into one (an AsyncAPI 3 operation naming its channel) is compared as
// text: following it would report every channel change a second time.
var links = map[string]bool{"paths": true, "channels": true, "operations": true, "servers": true}

// follows reports whether the walk resolves ref, met at loc.
func (s *refSide) follows(loc, ref string) bool {
	file, ptr, _ := strings.Cut(ref, "#")
	if ref == "" || file != "" || !strings.HasPrefix(loc, s.root+"#") {
		return ref != ""
	}
	toks := pointerTokens(ptr)
	return len(toks) == 0 || !links[toks[0]]
}

// deref returns what node resolves to and where that lives. A node without a
// ref it follows is its own target. A relative file ref resolves against the
// file holding it and never leaves the bundle; a remote URL never resolves.
func (s *refSide) deref(loc string, node any) (any, string, error) {
	ref := refOf(node)
	if !s.follows(loc, ref) {
		return node, loc, nil
	}
	base, _, _ := strings.Cut(loc, "#")
	file, ptr, _ := strings.Cut(ref, "#")
	if file != "" {
		file = path.Join(path.Dir(base), file)
	} else {
		file = base
	}
	doc, ok := s.doc(file)
	if !ok {
		return nil, "", fmt.Errorf("cannot read %s", file)
	}
	target, err := lookup(doc, ptr)
	return target, file + "#" + ptr, err
}

// doc decodes a bundle file once; nil records one that cannot be read.
func (s *refSide) doc(file string) (any, bool) {
	if d, ok := s.files[file]; ok {
		return d, d != nil
	}
	var d any
	if data, err := fs.ReadFile(s.fsys, file); err != nil || yaml.Unmarshal(data, &d) != nil {
		d = nil
	}
	s.files[file] = d
	return d, d != nil
}

func (s *refSide) display(loc string) string {
	return strings.TrimSuffix(strings.TrimPrefix(loc, s.root), "#")
}

// pointerTokens splits a JSON pointer into its unescaped tokens. A pointer in
// a URI fragment may also be percent-encoded.
func pointerTokens(ptr string) []string {
	if ptr == "" {
		return nil
	}
	toks := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for i, t := range toks {
		if u, err := url.PathUnescape(t); err == nil {
			t = u
		}
		toks[i] = strings.NewReplacer("~1", "/", "~0", "~").Replace(t)
	}
	return toks
}

// lookup follows a JSON pointer into doc. A fragment that is no pointer, such
// as a plain-name anchor, does not resolve.
func lookup(doc any, ptr string) (any, error) {
	if ptr != "" && !strings.HasPrefix(ptr, "/") {
		return nil, fmt.Errorf("%s is no JSON pointer", ptr)
	}
	node := doc
	for _, tok := range pointerTokens(ptr) {
		if arr, ok := node.([]any); ok {
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(arr) {
				return nil, fmt.Errorf("no index %s", tok)
			}
			node = arr[i]
			continue
		}
		v, ok := toStringMap(node)[tok]
		if !ok {
			return nil, fmt.Errorf("no key %s", tok)
		}
		node = v
	}
	return node, nil
}

// unused reports, as one NON_BREAKING change each, the components no
// root section reaches through a $ref in either revision. A consumer only
// reads a component through an operation or a message that uses it, so an
// edit to one nothing uses breaks nobody. Security schemes are left out: a
// security requirement names one without a $ref, so the scan cannot see it
// used.
func (r *specRefs) unused(prefix, noun string, roots ...string) []Change {
	comps := [2]map[string]any{}
	for i, s := range r.side {
		doc, _ := s.doc(s.root)
		top := toStringMap(doc)
		seen := map[string]bool{}
		for _, root := range roots {
			s.reach(s.root+"#/"+root, top[root], seen)
		}
		comps[i] = map[string]any{}
		for sec, entries := range toStringMap(top["components"]) {
			for name, v := range toStringMap(entries) {
				if sec != "securitySchemes" {
					comps[i][sec+"/"+name] = v
				}
			}
		}
	}
	var changes []Change
	for _, key := range mergedKeys(comps[0], comps[1]) {
		o, inOld := comps[0][key]
		n, inNew := comps[1][key]
		if r.side[0].used[key] || r.side[1].used[key] || (inOld && inNew && yamlEqual(o, n)) {
			continue
		}
		ct := Modified
		switch {
		case !inOld:
			ct = Added
		case !inNew:
			ct = Removed
		}
		sec, name, _ := strings.Cut(key, "/")
		changes = append(changes, Change{
			Path: fmt.Sprintf("%s.components.%s[%s]", prefix, sec, name), Type: ct,
			OldValue: jsonSafe(o), NewValue: jsonSafe(n), Classification: NonBreaking,
			Reason: fmt.Sprintf("component %s %s: no %s in either revision references it, so no consumer reads it", key, ct, noun),
		})
	}
	return changes
}

// reach marks every component node reaches through $refs it follows,
// visiting each target once, so cyclic refs terminate.
func (s *refSide) reach(loc string, node any, seen map[string]bool) {
	if arr, ok := node.([]any); ok {
		for i, e := range arr {
			s.reach(loc+"/"+strconv.Itoa(i), e, seen)
		}
		return
	}
	m := toStringMap(node)
	if target, tLoc, err := s.deref(loc, m); err == nil && tLoc != loc && !seen[tLoc] {
		seen[tLoc] = true
		if toks := pointerTokens(strings.TrimPrefix(tLoc, s.root+"#")); strings.HasPrefix(tLoc, s.root+"#/components/") && len(toks) > 2 {
			s.used[toks[1]+"/"+toks[2]] = true
		}
		s.reach(tLoc, target, seen)
	}
	for k, v := range m {
		s.reach(loc+"/"+pointerEscaper.Replace(k), v, seen)
	}
}
