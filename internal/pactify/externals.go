package pactify

import (
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// External is a system outside the cluster that the folder names.
type External = discover.External

// catalog is the catalog externals are classified with and its digest, with
// " (embedded)" when it is the copy compiled into this binary.
func (p *planner) catalog() (*externalcatalog.Catalog, string) {
	if c := p.opts.Catalog; c != nil {
		return c, c.Refs()[0].Digest
	}
	return externalcatalog.Default(), externalcatalog.DefaultDigest() + " (embedded)"
}

// externals reads the externals the folder names and says what it found:
// a likely one in Found, a possible one (a key name with nothing to confirm
// it) and a host outside the cluster that nothing names in Unknown.
func (p *planner) externals(dir string) {
	cat, digest := p.catalog()
	p.r.Catalog = digest
	p.r.Externals = p.sc.Externals(dir, cat)
	likely := map[string]bool{}
	for _, e := range p.r.Externals {
		if e.Confidence == externalcatalog.ConfidenceLikely {
			likely[e.Identity] = true
			p.found("%s calls %s (%s, %s)", p.r.Service, e.Identity, e.Host, e.Where)
		}
	}
	for _, e := range p.r.Externals {
		if !likely[e.Identity] {
			p.unknown("%s suggests %s: nothing else in the folder confirms it", e.Where, e.Identity)
		}
	}
	// A value under a sensitive key is never parsed, so it never gets here.
	unidentified := func(where, value, key string) {
		if !schemax.SensitiveKey(key) && discover.NamesHost(externalcatalog.NormalizeHost(value)) && !slices.ContainsFunc(p.r.Externals, func(x External) bool { return x.Host != "" && x.Where == where }) {
			p.unknown("%s names a host outside the cluster that no catalog entry identifies: declare it in pacto.yaml if %s calls it", where, p.r.Service)
		}
	}
	for _, e := range p.sc.Env {
		if !e.Secret {
			unidentified(discover.EnvWhere(e), e.Value, e.Name)
		}
	}
	for _, c := range p.sc.Charts {
		p.sc.ChartValues(dir, c, unidentified)
	}
}

// externalEntries writes the likely externals as a commented block under
// dependencies, so nothing inferred becomes declared until a person
// uncomments it. Every interpolated string has its control characters
// replaced, so no value can end a comment line.
func (p *planner) externalEntries(b *strings.Builder) {
	var likely []External
	for _, e := range p.r.Externals {
		if e.Confidence == externalcatalog.ConfidenceLikely && !slices.ContainsFunc(likely, func(x External) bool { return x.Identity == e.Identity }) {
			likely = append(likely, e)
		}
	}
	if len(likely) == 0 {
		return
	}
	if !strings.Contains(b.String(), "\ndependencies:\n") {
		b.WriteString("# dependencies:\n")
	}
	b.WriteString("  # inferred externals: uncomment the ones this service calls and set the version\n")
	for _, e := range likely {
		name, id := dependencyName(e.Identity), discover.CommentSafe(e.Identity)
		detail := "likely, catalog " + discover.CommentSafe(p.r.Catalog)
		if e.Entry != "" {
			detail += ", entry " + discover.CommentSafe(e.Entry)
		} else {
			detail += ", " + discover.CommentSafe(e.Class)
		}
		if name != id {
			detail += ", identity " + id
		}
		fmt.Fprintf(b, "  # - name: %s  # %s (%s)\n  #   external: true\n  #   required: true\n  #   version: <the API version you call>\n",
			name, discover.CommentSafe(e.Where), detail)
	}
	p.next("pacto.yaml lists the externals the folder names as comments under dependencies: uncomment the ones %s calls and set the version it calls", p.r.Service)
}

// dependencyName is the identity when it is a valid dependency name, else its
// last component made one.
func dependencyName(id string) string {
	if nameRE.MatchString(id) {
		return id
	}
	return contract.SanitizeName(id[strings.LastIndex(id, "/")+1:])
}
