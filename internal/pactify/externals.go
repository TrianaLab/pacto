package pactify

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
)

// External is one place the folder names a system outside the cluster.
type External = discover.External

// catalog is the catalog externals are classified with and every catalog it
// was built from as "<origin> <digest>", with " (embedded)" when it is the
// copy compiled into this binary.
func (p *planner) catalog() (*externalcatalog.Catalog, string) {
	if c := p.opts.Catalog; c != nil {
		return c, strings.Join(discover.CatalogInputs(c), ", ")
	}
	return externalcatalog.Default(), "default " + externalcatalog.DefaultDigest() + " (embedded)"
}

// externals reads the external dependencies of the record rec and says what
// it found: a likely one in Found, a possible one (a key name with nothing to
// confirm it) in Unknown. A host no catalog entry identifies is a finding of
// rec, which discoveryLines names.
func (p *planner) externals(rec *contract.InferredRecord) {
	cat, digest := p.catalog()
	p.r.Catalog, p.r.Externals = digest, []External{}
	rec = cmp.Or(rec, &contract.InferredRecord{})
	unread := discover.ExternalUnread(rec.Findings, rec.FindingsDropped)
	if p.opts.CatalogUnavailable != "" {
		p.unknown("%s", p.opts.CatalogUnavailable)
		unread++
	}
	p.r.ExternalSummary = ExternalSummary(rec.Fields, unread)
	for _, f := range rec.Fields {
		x := f.External
		if x == nil {
			continue
		}
		class := externalcatalog.ClassUnclassified
		if e, _, ok := cat.Entry(externalid.ID(x.Identity)); ok {
			class = e.Class
		}
		for _, s := range x.Sources {
			where := s.Path
			if s.Line > 0 {
				where = fmt.Sprintf("%s:%d", s.Path, s.Line)
			}
			if s.Key != "" {
				where += " " + s.Key
			}
			p.r.Externals = append(p.r.Externals, External{Identity: x.Identity, Class: class, Confidence: s.Confidence(f.Confidence), Where: where,
				Host: s.Host, Kind: s.Kind, Path: s.Path, Line: s.Line, Key: s.Key, Origin: x.Catalog})
		}
		if f.Confidence == externalcatalog.ConfidenceLikely {
			p.found("%s calls %s (%s)", p.r.Service, x.Identity, f.Source)
		} else {
			p.unknown("%s suggests %s: nothing else in the folder confirms it", f.Source, x.Identity)
		}
	}
}
