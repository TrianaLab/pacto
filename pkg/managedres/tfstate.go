package managedres

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// SourceTerraform is the [fleet.RawManagedResource] source of a resource
// read from a Terraform or OpenTofu state.
const SourceTerraform = "terraform"

// MaxStateBytes bounds one state document.
//
// ponytail: the whole document is read into memory; stream the resources
// array with json.Decoder.Token if real states outgrow it.
const MaxStateBytes = 64 << 20

// stateVersion is the only state format ParseState reads.
const stateVersion = 4

// state is the part of a state document ParseState reads.
type state struct {
	Version   int `json:"version"`
	Resources []struct {
		Mode      string     `json:"mode"`
		Type      string     `json:"type"`
		Name      string     `json:"name"`
		Instances []instance `json:"instances"`
	} `json:"resources"`
}

// instance is one resource instance: its index key and its attributes, held
// raw only until the allowed keys are copied out.
type instance struct {
	IndexKey   any                        `json:"index_key"`
	Attributes map[string]json.RawMessage `json:"attributes"`
}

// ParseState reads the managed resources of the state document r, read from
// location (a file name or a state URL, never its contents), classified with
// cat and observed at now. Only managed resources a catalog row names are
// returned; data sources and unclassified types are skipped. Of every
// instance only the catalog's id attribute (never a sensitive key), arn, id
// (an azurerm subscription), region, location, project and the engine keys
// are read, the engine kept as an object's is; the identity is
// the full managed identity when account, region and id were read, else the
// catalog entry's identity with the missing parts listed. State records what
// Terraform last applied, so the engine is observed.
func ParseState(r io.Reader, location string, cat *externalcatalog.Catalog, now time.Time) ([]fleet.RawManagedResource, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxStateBytes+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("terraform state %s: %w", location, err)
	case len(data) > MaxStateBytes:
		return nil, fmt.Errorf("terraform state %s is over 64 MiB, so it is not read", location)
	}
	var doc state
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("terraform state %s is not valid JSON", location)
	}
	if doc.Version != stateVersion {
		return nil, fmt.Errorf("terraform state %s is version %d; only version %d is read", location, doc.Version, stateVersion)
	}
	var out []fleet.RawManagedResource
	for _, res := range doc.Resources {
		if res.Mode != "managed" {
			continue
		}
		m, idAttr, ok := cat.MatchTerraform(res.Type)
		if !ok {
			continue
		}
		_, ref, _ := cat.Entry(m.Identity) // ponytail: a match always names an entry of cat
		for _, in := range res.Instances {
			rr := resource(m, idAttr, res.Type, address(res.Type, res.Name, in.IndexKey), in.Attributes)
			rr.Location, rr.Catalog, rr.ObservedAt = location, ref.Digest, &now
			out = append(out, rr)
		}
	}
	return out, nil
}

// address is the Terraform address of an instance: <type>.<name>, with
// [<index_key>] when it has one.
func address(typ, name string, key any) string {
	addr := typ + "." + name
	switch k := key.(type) {
	case string:
		addr += fmt.Sprintf("[%q]", k)
	case float64:
		addr += fmt.Sprintf("[%v]", k)
	}
	return addr
}

// resource is the managed resource of the match m read from attrs: only the
// keys it names are looked at.
func resource(m externalcatalog.Match, idAttr, typ, addr string, attrs map[string]json.RawMessage) fleet.RawManagedResource {
	str := func(key string) string {
		var s string
		_ = json.Unmarshal(attrs[key], &s) // ponytail: a key that is not a string reads as unread
		return s
	}
	var id string
	if idAttr != "" && !schemax.SensitiveKey(idAttr) { // whatever the catalog says
		id = strings.ToLower(str(idAttr))
	}
	arnRegion, arnAccount := arnParts(str("arn"))
	// An azurerm instance names its subscription in its resource id.
	azAccount, _ := azureParts(str("id"))
	account := strings.ToLower(cmp.Or(arnAccount, azAccount, str("project")))
	region := strings.ToLower(cmp.Or(str("region"), str("location"), arnRegion))
	engine := strings.TrimSpace(str("engine") + " " + cmp.Or(str("engine_version"), str("version")))
	r := fleet.RawManagedResource{Source: SourceTerraform, Entry: m.Entry, Type: typ, Name: addr,
		Engine: keepEngine(cmp.Or(engine, str("database_version")))}
	identify(&r, m, account, region, id)
	return r
}

// identify sets the identity of r, classified as m: the full managed
// identity when account, region and id are each one segment, else m's
// entry identity with the parts that were not read listed in Missing.
func identify(r *fleet.RawManagedResource, m externalcatalog.Match, account, region, id string) {
	r.Identity = string(m.Identity)
	for _, part := range []struct{ name, value string }{{"id", id}, {"account", account}, {"region", region}} {
		if !segment(part.value) {
			r.Missing = append(r.Missing, part.name)
		}
	}
	if provider, service, ok := strings.Cut(r.Identity, "/"); ok && len(r.Missing) == 0 {
		full, _ := externalid.Managed(provider, service, account, region, id) // ponytail: every part is a segment
		r.Identity = string(full)
	}
}

// arnParts are the region and account of an AWS ARN, empty when arn is not
// one.
func arnParts(arn string) (region, account string) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" {
		return "", ""
	}
	return parts[3], parts[4]
}

// segment reports whether s is one identity segment.
func segment(s string) bool {
	if s == "" || strings.Contains(s, "/") {
		return false
	}
	_, err := externalid.Parse(s)
	return err == nil
}
