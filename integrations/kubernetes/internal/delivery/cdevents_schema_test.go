/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// cdeventsSchemas compiles the CDEvents v0.5.1 event schemas vendored from
// github.com/cdevents/spec (tag v0.5.1, schemas/), keyed by event type.
func cdeventsSchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	dir := filepath.Join("testdata", "cdevents-"+cdeventsVersion)
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	links, _ := filepath.Glob(filepath.Join(dir, "links", "*.json"))
	for _, f := range append(files, links...) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(b)))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource(doc.(map[string]any)["$id"].(string), doc); err != nil {
			t.Fatal(err)
		}
	}
	out := map[string]*jsonschema.Schema{}
	for _, f := range files {
		var doc struct {
			ID         string `json:"$id"`
			Properties struct {
				Context struct {
					Properties struct {
						Type struct {
							Default string `json:"default"`
						} `json:"type"`
					} `json:"properties"`
				} `json:"context"`
			} `json:"properties"`
		}
		b, _ := os.ReadFile(f)
		_ = json.Unmarshal(b, &doc)
		s, err := c.Compile(doc.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[doc.Properties.Context.Properties.Type.Default] = s
	}
	return out
}

// TestCDEventsConformToSpec validates the data of every standard CDEvent the
// recorder posts, including a record and a deleted Stage with no known time,
// against the vendored v0.5.1 schemas.
func TestCDEventsConformToSpec(t *testing.T) {
	schemas := cdeventsSchemas(t)
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	stage := envStage("prod", "payments-prod", "acme", "u1")
	op := func(typ, before, after, finished string) delivery.Statement {
		return delivery.Statement{Predicate: delivery.RecordPredicate{Kind: delivery.RecordOperation, Type: &delivery.RecordType{ID: typ},
			Time:    delivery.RecordTime{Finished: finished},
			Outcome: &delivery.RecordOutcome{Result: delivery.Succeeded, Pointer: delivery.PointerChange{Before: before, After: after}}}}
	}
	events := make([]map[string]any, 0, 6)
	for _, st := range []delivery.Statement{
		op(delivery.TypePromote, "", dA, "2026-10-20T09:00:00Z"),
		op(delivery.TypePromote, dA, dB, ""),
		op(delivery.TypeRollback, dB, dA, "2026-10-20T09:00:00Z"),
	} {
		events = append(events, cdEvents("/pacto-delivery/cid", stage, st, "sha256:r", true, now)[1])
	}
	events = append(events, environmentEvent("/pacto-delivery/cid", stage, true, now), environmentEvent("/pacto-delivery/cid", stage, false, now))
	gone := metav1.NewTime(now.Add(-time.Minute))
	stage.DeletionTimestamp = &gone
	events = append(events, environmentEvent("/pacto-delivery/cid", stage, false, now))
	seen := map[string]bool{}
	for _, ev := range events {
		typ := ev["type"].(string)
		s := schemas[typ]
		if s == nil {
			t.Fatalf("no vendored schema for %s", typ)
		}
		seen[typ] = true
		b, _ := json.Marshal(ev["data"])
		data, _ := jsonschema.UnmarshalJSON(strings.NewReader(string(b)))
		if err := s.Validate(data); err != nil {
			t.Errorf("%s does not conform: %v", typ, err)
		}
	}
	if len(seen) != len(schemas) {
		t.Fatalf("validated %v of %d schemas", seen, len(schemas))
	}
}

// TestCDEventUnknownTimeIsObserved: an occurrence with no known time has no
// CloudEvent time; the required CDEvents timestamp is when the recorder saw
// it, stated in customData.observedAt.
func TestCDEventUnknownTimeIsObserved(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	ev := cloudEvent("src", "id", eventConcluded, "", now, map[string]any{}, nil)
	data := ev["data"].(map[string]any)
	if _, ok := ev["time"]; ok || data["context"].(map[string]any)["timestamp"] != "2026-10-20T10:00:00Z" ||
		data["customData"].(map[string]any)["observedAt"] != "2026-10-20T10:00:00Z" {
		t.Fatalf("event = %v", ev)
	}
	ev = cloudEvent("src", "id", eventConcluded, "2026-10-20T09:00:00Z", now, map[string]any{}, nil)
	data = ev["data"].(map[string]any)
	if ev["time"] != "2026-10-20T09:00:00Z" || data["customData"] != nil || data["context"].(map[string]any)["specversion"] != cdeventsVersion {
		t.Fatalf("event = %v", ev)
	}
}
