package diff

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

// refSpec uses one schema component in a request and a response, a response
// component that points into another file, a parameter component, a header
// component and a component nothing uses.
const refSpec = `openapi: 3.1.0
paths:
  /orders:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Order'}
      responses:
        "201": {$ref: '#/components/responses/Created'}
  /orders/{id}:
    get:
      parameters:
        - $ref: '#/components/parameters/Id'
      responses:
        "200":
          description: ok
          headers:
            X-Rate: {$ref: '#/components/headers/Rate'}
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Order'}
components:
  parameters:
    Id: {name: id, in: path, required: true, schema: {type: string, maxLength: 36}}
  headers:
    Rate: {schema: {type: integer, maximum: 10}}
  responses:
    Created:
      description: created
      content:
        application/json:
          schema: {$ref: 'schemas/receipt.yaml#/Receipt'}
  schemas:
    Order:
      type: object
      properties:
        id: {type: string}
        status: {type: string, enum: [new, paid]}
    Legacy: {type: string}
`

const refReceipt = `Receipt:
  type: object
  properties:
    orderId: {type: string}
    total: {$ref: '#/Money'}
Money: {type: number}
`

func refFS(spec, receipt string) fstest.MapFS {
	return fstest.MapFS{
		"api/openapi.yaml":         {Data: []byte(spec)},
		"api/schemas/receipt.yaml": {Data: []byte(receipt)},
	}
}

func diffRefSpecs(t *testing.T, oldSpec, newSpec, oldReceipt, newReceipt string) []Change {
	t.Helper()
	return diffOpenAPI("api/openapi.yaml", "api/openapi.yaml", refFS(oldSpec, oldReceipt), refFS(newSpec, newReceipt))
}

func edit(t *testing.T, s, from, to string) string {
	t.Helper()
	if !strings.Contains(s, from) {
		t.Fatalf("fixture has no %q", from)
	}
	return strings.Replace(s, from, to, 1)
}

func onlyChange(t *testing.T, changes []Change) Change {
	t.Helper()
	if len(changes) != 1 {
		t.Fatalf("expected one change, got %d: %+v", len(changes), changes)
	}
	return changes[0]
}

const orderResponse = "openapi.paths[/orders/{id}].methods[GET].responses[200].content.application/json.schema"

// A component used by a request and a response is judged in both positions.
// A new enum value is safe to send and breaks a reader, so the one change
// reported is BREAKING at the response, and its reason names both usages.
func TestRefs_ComponentJudgedWhereUsed(t *testing.T) {
	changes := diffRefSpecs(t, refSpec, edit(t, refSpec, "[new, paid]", "[new, paid, shipped]"), refReceipt, refReceipt)
	c := onlyChange(t, changes)
	if c.Path != orderResponse+".properties.status.enum[shipped]" || c.Classification != Breaking {
		t.Fatalf("got %s %s", c.Path, c.Classification)
	}
	for _, want := range []string{"(in #/components/schemas/Order;", "request at openapi.paths[/orders].methods[POST]", "response at " + orderResponse} {
		if !strings.Contains(c.Reason, want) {
			t.Errorf("reason %q lacks %q", c.Reason, want)
		}
	}
	if c.refPair != "" || c.refRel != "" {
		t.Error("merge left its bookkeeping on the change")
	}
}

// Dropping an enum value is the mirror: it breaks the request and is safe
// in the response, so the worst grade comes from the request usage.
func TestRefs_WorstPositionWins(t *testing.T) {
	c := onlyChange(t, diffRefSpecs(t, refSpec, edit(t, refSpec, "[new, paid]", "[new]"), refReceipt, refReceipt))
	if c.Path != "openapi.paths[/orders].methods[POST].request-body.content.application/json.schema.properties.status.enum[paid]" || c.Classification != Breaking {
		t.Fatalf("got %s %s", c.Path, c.Classification)
	}
}

// Renaming a component whose content is identical changes nothing a
// consumer reads.
func TestRefs_RenameIsNoChange(t *testing.T) {
	renamed := strings.ReplaceAll(refSpec, "schemas/Order", "schemas/Purchase")
	renamed = edit(t, renamed, "    Order:\n", "    Purchase:\n")
	renamed = edit(t, renamed, "parameters/Id'", "parameters/OrderId'")
	renamed = edit(t, renamed, "    Id: {", "    OrderId: {")
	if changes := diffRefSpecs(t, refSpec, renamed, refReceipt, refReceipt); len(changes) != 0 {
		t.Fatalf("expected no change, got %+v", changes)
	}
}

// A ref pointed at a structurally different schema is graded by the
// structural difference, and the reason names both components.
func TestRefs_RepointedRefIsStructural(t *testing.T) {
	spec := edit(t, refSpec, "    Legacy: {type: string}", "    Legacy: {type: string}\n    Thin: {type: object, properties: {id: {type: string}}}")
	repointed := edit(t, spec, "        \"200\":\n          description: ok", "        \"200\":\n          description: ok\n")
	repointed = strings.Replace(repointed, "schema: {$ref: '#/components/schemas/Order'}", "schema: {$ref: '#/components/schemas/Thin'}", 2)
	repointed = strings.Replace(repointed, "schema: {$ref: '#/components/schemas/Thin'}", "schema: {$ref: '#/components/schemas/Order'}", 1)
	changes := diffRefSpecs(t, spec, repointed, refReceipt, refReceipt)
	c, ok := findChange(changes, orderResponse+".properties.status", Removed)
	if !ok || c.Classification != Breaking {
		t.Fatalf("expected status removed from the response, got %+v", changes)
	}
	if !strings.Contains(c.Reason, "(in #/components/schemas/Thin, was #/components/schemas/Order)") {
		t.Errorf("reason: %s", c.Reason)
	}
}

// A ref Pacto cannot resolve is a missing observation: POTENTIAL_BREAKING
// with a reason saying so, never a confirmed break. The same unresolved ref
// on both sides shows no change.
// assertUnresolved wants at least one change at a usage path, and every one
// of them POTENTIAL_BREAKING with a reason naming the revision that failed.
func assertUnresolved(t *testing.T, changes []Change, rev string) {
	t.Helper()
	used := 0
	for _, c := range changes {
		if strings.HasPrefix(c.Path, "openapi.components") {
			continue // a component nothing uses
		}
		used++
		if c.Classification != PotentialBreaking || !strings.Contains(c.Reason, "could not resolve $ref") || !strings.Contains(c.Reason, "in the "+rev+" revision") {
			t.Errorf("%s %s: %s", c.Path, c.Classification, c.Reason)
		}
	}
	if used == 0 {
		t.Errorf("no change at a usage path: %+v", changes)
	}
}

func TestRefs_Unresolvable(t *testing.T) {
	remote := strings.Replace(refSpec, "'#/components/schemas/Order'}\n      responses", "'https://example.com/order.yaml'}\n      responses", 1)
	tests := []struct{ name, old, new, rev string }{
		{"remote ref introduced", refSpec, remote, "new"},
		{"remote ref replaced", remote, refSpec, "old"},
		{"target removed", refSpec, edit(t, refSpec, "    Order:\n", "    Gone:\n"), "new"},
		{"file target gone", refSpec, edit(t, refSpec, "receipt.yaml#/Receipt", "missing.yaml#/Receipt"), "new"},
		{"anchor", refSpec, edit(t, refSpec, "receipt.yaml#/Receipt", "receipt.yaml#Receipt"), "new"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertUnresolved(t, diffRefSpecs(t, tt.old, tt.new, refReceipt, refReceipt), tt.rev)
		})
	}
	// The ref text stays the same while its target goes away: the receipt
	// file dropped from the new bundle.
	t.Run("file dropped from bundle", func(t *testing.T) {
		nw := fstest.MapFS{"api/openapi.yaml": {Data: []byte(refSpec)}}
		assertUnresolved(t, diffOpenAPI("api/openapi.yaml", "api/openapi.yaml", refFS(refSpec, refReceipt), nw), "new")
	})
	t.Run("file added to bundle", func(t *testing.T) {
		old := fstest.MapFS{"api/openapi.yaml": {Data: []byte(refSpec)}}
		assertUnresolved(t, diffOpenAPI("api/openapi.yaml", "api/openapi.yaml", old, refFS(refSpec, refReceipt)), "old")
	})
	if changes := diffRefSpecs(t, remote, remote, refReceipt, refReceipt); len(changes) != 0 {
		t.Errorf("same unresolved ref: %+v", changes)
	}
}

// The component behind a ref inside an external file is followed, against
// that file: Receipt's `#/Money` resolves in receipt.yaml.
func TestRefs_RelativeFile(t *testing.T) {
	changes := diffRefSpecs(t, refSpec, refSpec, refReceipt, edit(t, refReceipt, "Money: {type: number}", "Money: {type: integer}"))
	c := onlyChange(t, changes)
	if c.Path != "openapi.paths[/orders].methods[POST].responses[201].content.application/json.schema.properties.total.type" || c.Classification != NonBreaking {
		t.Fatalf("narrowing a response type: got %s %s", c.Path, c.Classification)
	}
	if !strings.Contains(c.Reason, "(in api/schemas/receipt.yaml#/Money)") {
		t.Errorf("reason: %s", c.Reason)
	}
}

// requestBodies, responses, parameters and headers components are resolved
// like schemas.
func TestRefs_NonSchemaComponents(t *testing.T) {
	t.Run("parameter", func(t *testing.T) {
		c := onlyChange(t, diffRefSpecs(t, refSpec, edit(t, refSpec, "maxLength: 36", "maxLength: 12"), refReceipt, refReceipt))
		if c.Path != "openapi.paths[/orders/{id}].methods[GET].parameters[id:path]" || c.Classification != Breaking {
			t.Fatalf("got %s %s", c.Path, c.Classification)
		}
	})
	t.Run("header is capped", func(t *testing.T) {
		c := onlyChange(t, diffRefSpecs(t, refSpec, edit(t, refSpec, "maximum: 10", "maximum: 20"), refReceipt, refReceipt))
		if c.Path != "openapi.paths[/orders/{id}].methods[GET].responses[200].headers.X-Rate.schema.maximum" || c.Classification != PotentialBreaking {
			t.Fatalf("got %s %s", c.Path, c.Classification)
		}
	})
	t.Run("request body", func(t *testing.T) {
		spec := edit(t, refSpec, "      requestBody:\n        content:\n          application/json:\n            schema: {$ref: '#/components/schemas/Order'}",
			"      requestBody: {$ref: '#/components/requestBodies/Order'}")
		spec = edit(t, spec, "  responses:\n    Created:", "  requestBodies:\n    Order: {required: false, content: {application/json: {schema: {type: object}}}}\n  responses:\n    Created:")
		c := onlyChange(t, diffRefSpecs(t, spec, edit(t, spec, "required: false", "required: true"), refReceipt, refReceipt))
		if c.Path != "openapi.paths[/orders].methods[POST].request-body.required" || c.Classification != Breaking {
			t.Fatalf("got %s %s", c.Path, c.Classification)
		}
	})
}

// A component no operation reaches breaks nobody: one NON_BREAKING change
// per component, whether it was changed, added or removed.
func TestRefs_UnusedComponents(t *testing.T) {
	tests := []struct {
		name, new string
		ct        ChangeType
	}{
		{"modified", edit(t, refSpec, "Legacy: {type: string}", "Legacy: {type: integer}"), Modified},
		{"removed", edit(t, refSpec, "    Legacy: {type: string}\n", ""), Removed},
		{"added", edit(t, refSpec, "    Legacy: {type: string}", "    Legacy: {type: string}\n    Spare: {type: string}"), Added},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := onlyChange(t, diffRefSpecs(t, refSpec, tt.new, refReceipt, refReceipt))
			if !strings.HasPrefix(c.Path, "openapi.components.schemas[") || c.Type != tt.ct || c.Classification != NonBreaking {
				t.Fatalf("got %s %s %s", c.Path, c.Type, c.Classification)
			}
		})
	}
	// A security scheme is used by name, never through a $ref, so it is never
	// reported as unused.
	sec := edit(t, refSpec, "components:\n", "components:\n  securitySchemes:\n    key: {type: apiKey}\n")
	if changes := diffRefSpecs(t, sec, edit(t, sec, "apiKey", "http"), refReceipt, refReceipt); len(changes) != 0 {
		t.Errorf("security scheme: %+v", changes)
	}
}

// A recursive schema terminates, and a change deep in it is reported once.
func TestRefs_Cyclic(t *testing.T) {
	const tree = `openapi: 3.1.0
paths:
  /tree:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Node'}
components:
  schemas:
    Node:
      type: object
      properties:
        name: {type: string}
        children: {type: array, items: {$ref: '#/components/schemas/Node'}}
        loop: {$ref: '#/components/schemas/Loop'}
    Loop: {$ref: '#/components/schemas/Loop'}
`
	c := onlyChange(t, diffRefSpecs(t, tree, edit(t, tree, "name: {type: string}", "name: {type: string, maxLength: 9}"), "", ""))
	if c.Path != "openapi.paths[/tree].methods[GET].responses[200].content.application/json.schema.properties.name.maxLength" || c.Classification != NonBreaking {
		t.Fatalf("got %s %s", c.Path, c.Classification)
	}
}

// A component used in many places is one change; the reason names three
// usages and counts the rest.
func TestRefs_ManyUsagesOneChange(t *testing.T) {
	var b strings.Builder
	b.WriteString("openapi: 3.1.0\npaths:\n")
	for i := range 5 {
		fmt.Fprintf(&b, "  /p%d:\n    get:\n      responses:\n        \"200\":\n          content:\n            application/json:\n              schema: {$ref: '#/components/schemas/S'}\n", i)
	}
	b.WriteString("components:\n  schemas:\n    S: {type: object, properties: {a: {type: string}}}\n")
	spec := b.String()
	c := onlyChange(t, diffRefSpecs(t, spec, edit(t, spec, "a: {type: string}", "a: {type: integer}"), "", ""))
	if !strings.HasSuffix(c.Reason, ", 2 more)") {
		t.Errorf("reason: %s", c.Reason)
	}
}

// Keywords written beside a $ref belong to the usage: a description added
// there is documentation.
func TestRefs_Siblings(t *testing.T) {
	c := onlyChange(t, diffRefSpecs(t, refSpec, edit(t, refSpec, "X-Rate: {$ref: '#/components/headers/Rate'}", "X-Rate: {$ref: '#/components/headers/Rate', description: rate}"), refReceipt, refReceipt))
	if c.Path != "openapi.paths[/orders/{id}].methods[GET].responses[200].headers.X-Rate.description" || c.Classification != NonBreaking {
		t.Fatalf("got %s %s", c.Path, c.Classification)
	}
}

// One revision inlines what the other references: the inline schema is
// compared with the resolved component, so identical content is no change.
func TestRefs_InlinedOnOneSide(t *testing.T) {
	inlined := edit(t, refSpec, "X-Rate: {$ref: '#/components/headers/Rate'}", "X-Rate: {schema: {type: integer, maximum: 10}}")
	if changes := diffRefSpecs(t, refSpec, inlined, refReceipt, refReceipt); len(changes) != 0 {
		t.Fatalf("expected no change, got %+v", changes)
	}
}

// A parameter $ref Pacto cannot resolve may be the parameter that looks
// removed or added, so neither is a confirmed break.
func TestRefs_UnresolvedParameter(t *testing.T) {
	gone := edit(t, refSpec, "'#/components/parameters/Id'", "'#/components/parameters/Gone'")
	t.Run("new side", func(t *testing.T) {
		changes := diffRefSpecs(t, refSpec, gone, refReceipt, refReceipt)
		if len(changes) != 2 {
			t.Fatalf("got %+v", changes)
		}
		for _, c := range changes {
			if c.Classification != PotentialBreaking {
				t.Errorf("%s: %s", c.Path, c.Classification)
			}
		}
		if c, _ := findChange(changes, "openapi.paths[/orders/{id}].methods[GET].parameters[$ref:#/components/parameters/Gone]", Added); !strings.Contains(c.Reason, "could not resolve") {
			t.Errorf("reason: %q", c.Reason)
		}
	})
	t.Run("old side", func(t *testing.T) {
		c, ok := findChange(diffRefSpecs(t, gone, refSpec, refReceipt, refReceipt), "openapi.paths[/orders/{id}].methods[GET].parameters[id:path]", Added)
		if !ok || c.Classification != PotentialBreaking {
			t.Fatalf("got %+v", c)
		}
	})
	t.Run("both sides", func(t *testing.T) {
		if changes := diffRefSpecs(t, gone, gone, refReceipt, refReceipt); len(changes) != 0 {
			t.Fatalf("got %+v", changes)
		}
	})
}

// A parameter ref that points at another ref resolves through the chain, so
// it does not hide a confirmed break of an unrelated parameter. A cycle never
// resolves.
func TestRefs_ChainedParameter(t *testing.T) {
	chained := edit(t, refSpec, "    Id: {name: id,", "    Id: {$ref: '#/components/parameters/IdBase'}\n    IdBase: {name: id,")
	withQ := edit(t, chained, "        - $ref: '#/components/parameters/Id'\n", "        - $ref: '#/components/parameters/Id'\n        - {name: q, in: query, schema: {type: string}}\n")
	c, ok := findChange(diffRefSpecs(t, withQ, chained, refReceipt, refReceipt), "openapi.paths[/orders/{id}].methods[GET].parameters[q:query]", Removed)
	if !ok || c.Classification != Breaking {
		t.Fatalf("chained ref: got %+v", c)
	}
	cyclic := edit(t, chained, "    IdBase: {name: id,", "    IdBase: {$ref: '#/components/parameters/Id'}\n    Unused: {name: id,")
	cyclicQ := edit(t, withQ, "    IdBase: {name: id,", "    IdBase: {$ref: '#/components/parameters/Id'}\n    Unused: {name: id,")
	c, ok = findChange(diffRefSpecs(t, cyclicQ, cyclic, refReceipt, refReceipt), "openapi.paths[/orders/{id}].methods[GET].parameters[q:query]", Removed)
	if !ok || c.Classification != PotentialBreaking {
		t.Fatalf("cyclic ref: got %+v", c)
	}
}

const pathItemSpec = `openapi: 3.1.0
paths:
  /users:
    $ref: 'paths/users.yaml'
components:
  schemas:
    User: {type: object, properties: {id: {type: string}, name: {type: string}}}
`

const pathItemUsers = `get:
  responses:
    "200":
      description: ok
      content:
        application/json:
          schema: {$ref: '../openapi.yaml#/components/schemas/User'}
`

func pathItemFS(spec, users string) fstest.MapFS {
	return fstest.MapFS{"api/openapi.yaml": {Data: []byte(spec)}, "api/paths/users.yaml": {Data: []byte(users)}}
}

// A path item may be a $ref to a file. Its operations are compared where the
// file holds them, and a component reached only through it is used.
func TestRefs_PathItem(t *testing.T) {
	diff := func(oldSpec, newSpec, oldUsers, newUsers string) []Change {
		return diffOpenAPI("api/openapi.yaml", "api/openapi.yaml", pathItemFS(oldSpec, oldUsers), pathItemFS(newSpec, newUsers))
	}
	t.Run("operation edited in the file", func(t *testing.T) {
		changes := diff(pathItemSpec, pathItemSpec, pathItemUsers, edit(t, pathItemUsers, "\"200\":", "\"201\":"))
		if _, ok := findChange(changes, "openapi.paths[/users].methods[GET].responses[200]", Removed); !ok {
			t.Fatalf("got %+v", changes)
		}
	})
	t.Run("component reached through the file", func(t *testing.T) {
		c := onlyChange(t, diff(pathItemSpec, edit(t, pathItemSpec, ", name: {type: string}", ""), pathItemUsers, pathItemUsers))
		if c.Path != "openapi.paths[/users].methods[GET].responses[200].content.application/json.schema.properties.name" || c.Classification != Breaking {
			t.Fatalf("got %s %s", c.Path, c.Classification)
		}
	})
	t.Run("file dropped from the bundle", func(t *testing.T) {
		nw := fstest.MapFS{"api/openapi.yaml": {Data: []byte(pathItemSpec)}}
		c := onlyChange(t, diffOpenAPI("api/openapi.yaml", "api/openapi.yaml", pathItemFS(pathItemSpec, pathItemUsers), nw))
		if c.Path != "openapi.paths[/users]" || c.Classification != PotentialBreaking || !strings.Contains(c.Reason, "in the new revision") {
			t.Fatalf("got %+v", c)
		}
	})
	t.Run("unresolved on both sides", func(t *testing.T) {
		nw := fstest.MapFS{"api/openapi.yaml": {Data: []byte(pathItemSpec)}}
		if changes := diffOpenAPI("api/openapi.yaml", "api/openapi.yaml", nw, nw); len(changes) != 0 {
			t.Fatalf("got %+v", changes)
		}
	})
}

const refAsyncV2 = `asyncapi: 2.6.0
channels:
  orders:
    subscribe:
      message: {$ref: '#/components/messages/Placed'}
components:
  messages:
    Placed:
      payload: {$ref: '#/components/schemas/Placed'}
  schemas:
    Placed:
      type: object
      properties:
        id: {type: string}
        note: {type: string}
`

const refAsyncV3 = `asyncapi: 3.0.0
channels:
  orders:
    messages:
      Placed: {$ref: '#/components/messages/Placed'}
operations:
  send:
    action: send
    channel: {$ref: '#/channels/orders'}
    messages: [{$ref: '#/channels/orders/messages/Placed'}]
components:
  messages:
    Placed:
      payload: {$ref: '#/components/schemas/Placed'}
  schemas:
    Placed:
      type: object
      properties:
        id: {type: string}
        note: {type: string}
`

// AsyncAPI 2.x and 3.x components are judged as records where a channel uses
// them. In 3.x an operation's refs to its channel are links compared as text,
// so the change is reported once, under the channel.
func TestRefs_AsyncAPI(t *testing.T) {
	for _, tt := range []struct{ name, doc, path string }{
		{"2.x", refAsyncV2, "asyncapi.channels[orders].subscribe.message.payload.properties.note"},
		{"3.x", refAsyncV3, "asyncapi.channels[orders].messages.Placed.payload.properties.note"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := onlyChange(t, diffAsync(t, tt.doc, edit(t, tt.doc, "        note: {type: string}\n", "")))
			if c.Path != tt.path || c.Classification != Breaking || !strings.Contains(c.Reason, "(in #/components/schemas/Placed)") {
				t.Fatalf("got %s %s: %s", c.Path, c.Classification, c.Reason)
			}
		})
	}
	relinked := edit(t, refAsyncV3, "channel: {$ref: '#/channels/orders'}", "channel: {$ref: '#/channels/other'}")
	c := onlyChange(t, diffAsync(t, refAsyncV3, relinked))
	if c.Path != "asyncapi.operations[send].channel.$ref" || c.Classification != Breaking {
		t.Fatalf("relinked operation: got %s %s", c.Path, c.Classification)
	}
}

func TestLookup(t *testing.T) {
	doc := map[string]any{"a/b": []any{"x", map[any]any{1: "one"}}, "t~": "tilde", "%zz": "raw"}
	for ptr, want := range map[string]any{"/a~1b/0": "x", "/a~1b/1/1": "one", "/t~0": "tilde", "/t%7E": "tilde", "/%zz": "raw"} {
		if got, err := lookup(doc, ptr); err != nil || got != want {
			t.Errorf("%s: got %v, %v", ptr, got, err)
		}
	}
	if got, err := lookup("whole", ""); err != nil || got != "whole" {
		t.Errorf("whole-file ref: got %v, %v", got, err)
	}
	for _, ptr := range []string{"/a~1b/x", "/a~1b/-1", "/missing", "anchor"} {
		if _, err := lookup(doc, ptr); err == nil {
			t.Errorf("%s resolved", ptr)
		}
	}
}

func TestRefDoc_Unparseable(t *testing.T) {
	s := &refSide{fsys: fstest.MapFS{"bad.yaml": {Data: []byte("a: [")}}, files: map[string]any{}}
	if _, ok := s.doc("bad.yaml"); ok {
		t.Fatal("parsed")
	}
	if _, ok := s.doc("bad.yaml"); ok {
		t.Fatal("cached as parsed")
	}
}
