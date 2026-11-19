package app

import (
	"bytes"
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// buildOptionalFrom is the first pactoVersion whose publish.artifacts[].build
// is optional: from it on, publish detects a build the contract leaves out.
const buildOptionalFrom = "2.5"

// withNotes is row with what the drops did: notes join its message, and a
// file whose bytes changed (changed) is rewritten even when its version was
// already current.
func withNotes(row MigratedFile, notes []string, changed bool) MigratedFile {
	if len(notes) == 0 {
		return row
	}
	msgs := append([]string{row.Message}, notes...)
	if changed && row.Status == MigrateCurrent {
		row.Status, msgs = MigrateRewritten, notes
	}
	row.Message = strings.Join(msgs, "; ")
	return row
}

// rootMapping is the root mapping of raw's first document, nil when raw does
// not parse or holds no mapping. It never decodes into contract.Contract,
// which refuses the keys the drops remove.
func rootMapping(raw []byte) *yaml.Node {
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return doc.Content[0]
}

// mapKey is the key node of key in the mapping n, nil when n is not a
// mapping or lacks it.
func mapKey(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i]
		}
	}
	return nil
}

// dropRemoved deletes the top-level delivery section, which left pactoVersion
// 2.5 before its release and which Parse refuses at every version, and says
// so. A section it cannot cut by lines stays, and Parse then names it.
func dropRemoved(raw []byte) ([]byte, []string) {
	root := rootMapping(raw)
	if out, ok := deleteKeyLines(raw, root, mapKey(root, "delivery")); ok {
		return out, []string{"removed delivery (it left pactoVersion 2.5 before its release; the Stage's spec.applyRef names the applier)"}
	}
	return raw, nil
}

// dropDetectedBuilds deletes each publish.artifacts[].build of raw that
// publish detection in dir reproduces, and says what it removed and what it
// kept. Below buildOptionalFrom (pactoVersion absent reads as current) a
// build is required and stays without a word.
//
// ponytail: scans dir on every call (migrate calls it up to three times per
// file, twice inside the raise check); cache the plan per folder if a scan
// ever shows in a profile.
func dropDetectedBuilds(dir string, raw []byte) ([]byte, []string) {
	root := rootMapping(raw)
	version := contract.Current()
	if v := contract.MapValue(root, "pactoVersion"); v != nil {
		version = v.Value
	}
	if contract.Older(version, buildOptionalFrom) {
		return raw, nil
	}
	arts := contract.MapValue(contract.MapValue(root, "publish"), "artifacts")
	if arts == nil || arts.Kind != yaml.SequenceNode {
		return raw, nil
	}
	var (
		notes   []string
		bp      discover.BuildPlan
		finds   []contract.InferredFinding
		scanned bool
	)
	for i, art := range slices.Backward(arts.Content) { // last first, so earlier line numbers hold
		key := mapKey(art, "build")
		if key == nil {
			continue
		}
		if !scanned {
			scanned = true
			if sc, err := discover.Dir(dir); err == nil {
				bp, finds = discover.Build(dir, sc)
			}
		}
		var a contract.PublishArtifact
		at := fmt.Sprintf("publish.artifacts[%d].build", i)
		if art.Decode(&a) != nil || !detectedSame(a, bp, finds) {
			notes = append(notes, fmt.Sprintf("kept %s: %s; remove it if that is what you want", at, detectionGives(bp, finds)))
			continue
		}
		out, ok := deleteKeyLines(raw, art, key)
		if !ok {
			notes = append(notes, fmt.Sprintf("kept %s: pacto ci --publish detects the same build; delete it by hand", at))
			continue
		}
		raw = out
		notes = append(notes, fmt.Sprintf("removed %s (pacto ci --publish detects the same build)", at))
	}
	slices.Reverse(notes)
	return raw, notes
}

// detectedSame reports whether the image artifact a declares the Dockerfile
// build that detection (bp, with findings) gives, for the same image: the
// one case where leaving build out publishes the same thing.
func detectedSame(a contract.PublishArtifact, bp discover.BuildPlan, findings []contract.InferredFinding) bool {
	df := a.Build.Dockerfile
	if a.Type != contract.ArtifactImage || df == nil || len(a.Build.Command) > 0 || len(df.Platforms) > 0 ||
		len(findings) > 0 || bp.Tool != discover.ToolDockerfile || bp.Image == "" {
		return false
	}
	file := strings.TrimSuffix(bp.ToolSource, ":1")
	got, _ := discover.ImageRepo(bp.Image)
	want, _ := discover.ImageRepo(strings.TrimPrefix(a.Ref, "oci://"))
	return path.Clean(df.Context) == path.Dir(file) && path.Clean(cmp.Or(df.File, path.Join(df.Context, "Dockerfile"))) == file && got == want
}

// detectionGives says what publish detection found: its first finding, no
// build, or the tool and image.
func detectionGives(bp discover.BuildPlan, findings []contract.InferredFinding) string {
	switch {
	case len(findings) > 0:
		return "detection reports " + findings[0].Message
	case bp.Tool == "":
		return "detection finds no build"
	}
	return strings.TrimSpace(fmt.Sprintf("detection gives %s %s", bp.Tool, bp.Image))
}

// deleteKeyLines deletes key of the block mapping parent from raw by lines:
// the key's line and every following line indented deeper than the key,
// with blank and comment lines between them. It refuses (false) a key of a
// flow mapping, a missing key and one that shares its line with other text.
//
// ponytail: line-range deletion of a block-style key; a head comment above
// the key and above the next key is kept, a comment inside the deleted block
// goes with it.
func deleteKeyLines(raw []byte, parent, key *yaml.Node) ([]byte, bool) {
	if key == nil || parent.Style&yaml.FlowStyle != 0 {
		return raw, false
	}
	lines := bytes.SplitAfter(raw, []byte("\n"))
	indent := key.Column - 1
	if strings.TrimLeft(string(bytes.TrimPrefix(lines[key.Line-1], bom))[:indent], " ") != "" {
		return raw, false
	}
	end := key.Line // lines[key.Line-1 : end] go
	for j := key.Line; j < len(lines); j++ {
		text := bytes.TrimSpace(lines[j])
		if len(text) == 0 || text[0] == '#' {
			continue // part of the block only when a deeper line follows
		}
		if len(lines[j])-len(bytes.TrimLeft(lines[j], " ")) <= indent {
			break
		}
		end = j + 1
	}
	start := len(bytes.Join(lines[:key.Line-1], nil))
	if start == 0 && bytes.HasPrefix(raw, bom) {
		start = len(bom)
	}
	return append(raw[:start:start], bytes.Join(lines[end:], nil)...), true
}

var bom = []byte("\xef\xbb\xbf")
