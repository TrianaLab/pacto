package inferredcontract

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// pactoVersion is the contract version an inferred contract is written at:
// the first that has service.external.
const pactoVersion = "2.5"

// notName matches every character a service name may not hold.
var notName = regexp.MustCompile(`[^a-z0-9-]`)

// ServiceName is the service.name of key's inferred contract: the last
// identity segment, every character outside [a-z0-9-] replaced by "-",
// trimmed of dashes and capped at 63 characters.
func ServiceName(key fleet.ExternalKey) string {
	seg := string(key)[strings.LastIndexByte(string(key), '/')+1:]
	name := strings.Trim(notName.ReplaceAllString(seg, "-"), "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// Bundle is key's inferred contract at version: pacto.yaml names the
// service, its version, the external it describes and, when openapi is set,
// the vendor OpenAPI document under metadata; pacto.inferred.yaml holds s and
// the inputs in. Both are written deterministically, and the bundle is
// validated before it is returned.
func Bundle(key fleet.ExternalKey, s contract.InferredSurface, version string, in contract.InferredInputs, openapi string) (*contract.Bundle, error) {
	raw := fmt.Sprintf("pactoVersion: %q\nservice:\n  name: %s\n  version: %s\n  external: %s\n", pactoVersion, ServiceName(key), version, key)
	if openapi != "" {
		raw += fmt.Sprintf("metadata:\n  %s: %q\n", fleet.ExternalOpenAPIMetadataKey, openapi)
	}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: in, Fields: []contract.Inferred{}, Surface: &s}
	fsys := fstest.MapFS{"pacto.yaml": {Data: []byte(raw)}, contract.InferredRecordFile: {Data: rec.Marshal()}}
	c, err := contract.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("the inferred contract of %s: %w", key, err)
	}
	if res := validation.Validate(c, []byte(raw), fsys); !res.IsValid() {
		e := res.Errors[0]
		return nil, fmt.Errorf("the inferred contract of %s is invalid: %s: %s", key, e.Path, e.Message)
	}
	return &contract.Bundle{Contract: c, RawYAML: []byte(raw), FS: fsys}, nil
}

// Changed reports whether cur differs from old by anything diff.Compare
// sees, at old's version, or by the digest of its linked vendor OpenAPI
// document: a version bump alone is no change, and the surface's sources,
// times, asOf and completeness emit nothing, so only a value added or
// removed is. A document re-hosted under the same digest is no change.
func Changed(ctx context.Context, old, cur *contract.Bundle) bool {
	if fleet.ExternalOpenAPIDigest(openAPILink(old)) != fleet.ExternalOpenAPIDigest(openAPILink(cur)) {
		return true
	}
	c := *cur.Contract
	c.Service.Version = old.Contract.Service.Version
	return len(diff.Compare(ctx, old.Contract, &c, old.FS, cur.FS).Changes) > 0
}

// openAPILink is the vendor OpenAPI document b records, or "".
func openAPILink(b *contract.Bundle) string {
	v, _ := b.Contract.Metadata[fleet.ExternalOpenAPIMetadataKey].(string)
	return v
}

// Repository is the repository of key's inferred contract under root, the
// installation's <registry>/pacto-inferred.
func Repository(root string, key fleet.ExternalKey) string {
	return strings.TrimSuffix(root, "/") + "/" + string(key)
}

// versionTag matches the tags an inferred contract is published under.
var versionTag = regexp.MustCompile(`^0\.0\.(0|[1-9][0-9]{0,8})$`)

// versions is tags' 0.0.<n> tags, newest first.
func versions(tags []string) []string {
	var out []string
	for _, t := range tags {
		if versionTag.MatchString(t) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return patch(b) - patch(a) })
	return out
}

func patch(tag string) int {
	n, _ := strconv.Atoi(tag[len("0.0."):]) // versionTag bounds it to nine digits
	return n
}

// NextVersion is the version after every 0.0.<n> tag of tags: 0.0.1 when
// there is none. It counts surface changes and claims no vendor version.
func NextVersion(tags []string) string {
	n := 0
	if v := versions(tags); len(v) > 0 {
		n = patch(v[0])
	}
	return "0.0." + strconv.Itoa(n+1)
}
