package diff

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"gopkg.in/yaml.v3"
)

// diffAsyncAPI compares two AsyncAPI documents and returns changes for channels
// and (AsyncAPI 3.x) operations.
//
// Only `channels` and `operations` are compared: they are the consumer-facing
// surface. `info`, `servers`, `components` and `defaultContentType` are skipped
// for the same reason `metadata` is — they are documentation and wiring detail
// that churns on every release without changing what a consumer can subscribe to
// or publish.
func diffAsyncAPI(oldPath, newPath string, oldFS, newFS fs.FS) []Change {
	if oldFS == nil || newFS == nil || oldPath == "" || newPath == "" {
		return nil
	}

	oldDoc, oldErr := readAsyncAPIDoc(oldFS, oldPath)
	newDoc, newErr := readAsyncAPIDoc(newFS, newPath)
	if oldErr != nil || newErr != nil {
		return nil
	}

	changes := diffAsyncAPISection("asyncapi.channels", "channel", oldDoc.Channels, newDoc.Channels)
	return append(changes,
		diffAsyncAPISection("asyncapi.operations", "operation", oldDoc.Operations, newDoc.Operations)...)
}

// asyncAPIDoc holds the consumer-facing sections of an AsyncAPI document.
// Operations exist only in AsyncAPI 3.x; a 2.x document leaves the map nil and
// the operations diff is then a no-op.
type asyncAPIDoc struct {
	Channels   map[string]any `yaml:"channels"`
	Operations map[string]any `yaml:"operations"`
}

// readAsyncAPIDoc parses an AsyncAPI file. yaml.v3 reads JSON as well as YAML,
// so this one reader covers both .yaml and .json documents.
func readAsyncAPIDoc(fsys fs.FS, path string) (*asyncAPIDoc, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}

	var doc asyncAPIDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// diffAsyncAPISection diffs one name-keyed AsyncAPI map (channels or operations)
// in sorted key order. An entry present on both sides is deep-diffed with
// diffJSON so a payload property, a type change or a `required` entry surfaces
// as its own change instead of one opaque blob. diffJSON already returns nil
// for equal values, so no separate equality guard is needed here.
func diffAsyncAPISection(prefix, noun string, old, new map[string]any) []Change {
	var changes []Change

	for _, name := range slices.Sorted(maps.Keys(old)) {
		newVal, exists := new[name]
		if !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("%s[%s]", prefix, name),
				Type:           Removed,
				OldValue:       name,
				Classification: classify(prefix, Removed),
				Reason:         fmt.Sprintf("%s %s removed", noun, name),
			})
			continue
		}
		// dirUnknown: a channel is published by one service and subscribed by
		// another, so the document cannot say which side a `required` entry
		// constrains. Both readings stay breaking.
		changes = append(changes, diffJSON(dirUnknown, fmt.Sprintf("%s[%s]", prefix, name), old[name], newVal)...)
	}

	for _, name := range slices.Sorted(maps.Keys(new)) {
		if _, exists := old[name]; !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("%s[%s]", prefix, name),
				Type:           Added,
				NewValue:       name,
				Classification: classify(prefix, Added),
				Reason:         fmt.Sprintf("%s %s added", noun, name),
			})
		}
	}

	return changes
}
