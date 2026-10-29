package diff

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func makeAsyncAPIFS(content string) fstest.MapFS {
	return fstest.MapFS{
		"asyncapi.yaml": &fstest.MapFile{Data: []byte(content)},
	}
}

const baseAsyncAPI = `asyncapi: "2.6.0"
info:
  title: events
  version: 1.0.0
channels:
  order.created:
    publish:
      operationId: orderCreated
      message:
        payload:
          type: object
          properties:
            order_id:
              type: string
          required: ["order_id"]
  order.cancelled:
    publish:
      operationId: orderCancelled
      message:
        payload:
          type: object
          properties:
            order_id:
              type: string
`

func diffAsync(t *testing.T, oldDoc, newDoc string) []Change {
	t.Helper()
	return diffAsyncAPI("asyncapi.yaml", "asyncapi.yaml", makeAsyncAPIFS(oldDoc), makeAsyncAPIFS(newDoc))
}

func TestDiffAsyncAPI_Guards(t *testing.T) {
	fsys := makeAsyncAPIFS(baseAsyncAPI)
	tests := []struct {
		name             string
		oldPath, newPath string
		oldFS, newFS     fstest.MapFS
	}{
		{name: "empty paths", oldFS: fsys, newFS: fsys},
		{name: "empty old path", newPath: "asyncapi.yaml", oldFS: fsys, newFS: fsys},
		{name: "empty new path", oldPath: "asyncapi.yaml", oldFS: fsys, newFS: fsys},
		{name: "old missing file", oldPath: "asyncapi.yaml", newPath: "asyncapi.yaml", oldFS: fstest.MapFS{}, newFS: fsys},
		{name: "new missing file", oldPath: "asyncapi.yaml", newPath: "asyncapi.yaml", oldFS: fsys, newFS: fstest.MapFS{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if changes := diffAsyncAPI(tt.oldPath, tt.newPath, tt.oldFS, tt.newFS); len(changes) != 0 {
				t.Errorf("expected 0 changes, got %d", len(changes))
			}
		})
	}
}

func TestDiffAsyncAPI_NilFS(t *testing.T) {
	fsys := makeAsyncAPIFS(baseAsyncAPI)
	if changes := diffAsyncAPI("asyncapi.yaml", "asyncapi.yaml", nil, nil); len(changes) != 0 {
		t.Errorf("both nil: expected 0 changes, got %d", len(changes))
	}
	if changes := diffAsyncAPI("asyncapi.yaml", "asyncapi.yaml", nil, fsys); len(changes) != 0 {
		t.Errorf("old nil: expected 0 changes, got %d", len(changes))
	}
	if changes := diffAsyncAPI("asyncapi.yaml", "asyncapi.yaml", fsys, nil); len(changes) != 0 {
		t.Errorf("new nil: expected 0 changes, got %d", len(changes))
	}
}

func TestDiffAsyncAPI_Unparseable(t *testing.T) {
	bad := "channels: [unclosed\n  nope: :"
	if changes := diffAsync(t, bad, baseAsyncAPI); len(changes) != 0 {
		t.Errorf("bad old doc: expected 0 changes, got %d", len(changes))
	}
	if changes := diffAsync(t, baseAsyncAPI, bad); len(changes) != 0 {
		t.Errorf("bad new doc: expected 0 changes, got %d", len(changes))
	}
}

func TestDiffAsyncAPI_Identical(t *testing.T) {
	if changes := diffAsync(t, baseAsyncAPI, baseAsyncAPI); len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d: %+v", len(changes), changes)
	}
}

func TestDiffAsyncAPI_ChannelRemoved(t *testing.T) {
	newDoc := strings.Split(baseAsyncAPI, "  order.cancelled:")[0]
	changes := diffAsync(t, baseAsyncAPI, newDoc)

	c, ok := findChange(changes, "asyncapi.channels[order.cancelled]", Removed)
	if !ok {
		t.Fatalf("expected removed channel, got %+v", changes)
	}
	if c.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", c.Classification)
	}
	if c.OldValue != "order.cancelled" {
		t.Errorf("expected OldValue order.cancelled, got %v", c.OldValue)
	}
	if c.Reason != "channel order.cancelled removed" {
		t.Errorf("unexpected reason %q", c.Reason)
	}
}

func TestDiffAsyncAPI_ChannelAdded(t *testing.T) {
	oldDoc := strings.Split(baseAsyncAPI, "  order.cancelled:")[0]
	changes := diffAsync(t, oldDoc, baseAsyncAPI)

	c, ok := findChange(changes, "asyncapi.channels[order.cancelled]", Added)
	if !ok {
		t.Fatalf("expected added channel, got %+v", changes)
	}
	if c.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", c.Classification)
	}
	if c.NewValue != "order.cancelled" {
		t.Errorf("expected NewValue order.cancelled, got %v", c.NewValue)
	}
	if c.Reason != "channel order.cancelled added" {
		t.Errorf("unexpected reason %q", c.Reason)
	}
}

func TestDiffAsyncAPI_ChannelPayloadGainsRequiredProperty(t *testing.T) {
	newDoc := strings.Replace(baseAsyncAPI, `          required: ["order_id"]`,
		`          required: ["order_id", "tenant_id"]`, 1)
	changes := diffAsync(t, baseAsyncAPI, newDoc)

	path := "asyncapi.channels[order.created].publish.message.payload.required[tenant_id]"
	c, ok := findChange(changes, path, Added)
	if !ok {
		t.Fatalf("expected deep required change at %s, got %+v", path, changes)
	}
	// The deep differ escalates `required` on its own.
	if c.Classification != Breaking {
		t.Errorf("expected BREAKING for a new required property, got %s", c.Classification)
	}
}

func TestDiffAsyncAPI_ChannelPayloadTypeChanged(t *testing.T) {
	newDoc := strings.Replace(baseAsyncAPI, `            order_id:
              type: string`, `            order_id:
              type: integer`, 1)
	changes := diffAsync(t, baseAsyncAPI, newDoc)

	path := "asyncapi.channels[order.created].publish.message.payload.properties.order_id.type"
	c, ok := findChange(changes, path, Modified)
	if !ok {
		t.Fatalf("expected deep type change at %s, got %+v", path, changes)
	}
	if c.OldValue != "string" || c.NewValue != "integer" {
		t.Errorf("expected string -> integer, got %v -> %v", c.OldValue, c.NewValue)
	}
}

// A 2.x document has no top-level `operations`, so the operations diff is a no-op.
func TestDiffAsyncAPI_NoOperationsInV2(t *testing.T) {
	newDoc := strings.Replace(baseAsyncAPI, "order.created", "order.placed", 1)
	for _, c := range diffAsync(t, baseAsyncAPI, newDoc) {
		if strings.HasPrefix(c.Path, "asyncapi.operations") {
			t.Errorf("unexpected operations change on a 2.x document: %+v", c)
		}
	}
}

const baseAsyncAPIV3 = `asyncapi: "3.0.0"
channels:
  orders:
    address: orders
operations:
  sendOrder:
    action: send
    channel:
      $ref: "#/channels/orders"
  receiveOrder:
    action: receive
    channel:
      $ref: "#/channels/orders"
`

func TestDiffAsyncAPI_OperationRemoved(t *testing.T) {
	newDoc := strings.Split(baseAsyncAPIV3, "  receiveOrder:")[0]
	changes := diffAsync(t, baseAsyncAPIV3, newDoc)

	c, ok := findChange(changes, "asyncapi.operations[receiveOrder]", Removed)
	if !ok {
		t.Fatalf("expected removed operation, got %+v", changes)
	}
	if c.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", c.Classification)
	}
	if c.Reason != "operation receiveOrder removed" {
		t.Errorf("unexpected reason %q", c.Reason)
	}
}

func TestDiffAsyncAPI_OperationAdded(t *testing.T) {
	oldDoc := strings.Split(baseAsyncAPIV3, "  receiveOrder:")[0]
	changes := diffAsync(t, oldDoc, baseAsyncAPIV3)

	c, ok := findChange(changes, "asyncapi.operations[receiveOrder]", Added)
	if !ok {
		t.Fatalf("expected added operation, got %+v", changes)
	}
	if c.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", c.Classification)
	}
	if c.Reason != "operation receiveOrder added" {
		t.Errorf("unexpected reason %q", c.Reason)
	}
}

func TestDiffAsyncAPI_OperationModified(t *testing.T) {
	newDoc := strings.Replace(baseAsyncAPIV3, `  sendOrder:
    action: send`, `  sendOrder:
    action: receive`, 1)
	changes := diffAsync(t, baseAsyncAPIV3, newDoc)

	c, ok := findChange(changes, "asyncapi.operations[sendOrder].action", Modified)
	if !ok {
		t.Fatalf("expected modified operation action, got %+v", changes)
	}
	if c.OldValue != "send" || c.NewValue != "receive" {
		t.Errorf("expected send -> receive, got %v -> %v", c.OldValue, c.NewValue)
	}
}

// A channel or operation present on both sides is never reported as one opaque
// Modified change: it is deep-diffed, and every inner difference is classified
// by its own rules: a retyped payload field is unsafe in one direction and a
// flipped action changes who sends, so both are BREAKING. This is why the rule table carries no
// {"asyncapi.channels", Modified} / {"asyncapi.operations", Modified} entry —
// nothing would ever consult it.
func TestDiffAsyncAPI_ModifiedIsAlwaysDeepDiffed(t *testing.T) {
	channelDoc := strings.Replace(baseAsyncAPI, `            order_id:
              type: string`, `            order_id:
              type: integer`, 1)
	opDoc := strings.Replace(baseAsyncAPIV3, "    action: send", "    action: receive", 1)

	for _, tt := range []struct {
		name, prefix, deepPath string
		old, new               string
		wantDeep               Classification
	}{
		{
			name: "channel", prefix: "asyncapi.channels[order.created]",
			old: baseAsyncAPI, new: channelDoc,
			deepPath: "asyncapi.channels[order.created].publish.message.payload.properties.order_id.type",
			wantDeep: Breaking,
		},
		{
			name: "operation", prefix: "asyncapi.operations[sendOrder]",
			old: baseAsyncAPIV3, new: opDoc,
			deepPath: "asyncapi.operations[sendOrder].action",
			wantDeep: Breaking,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changes := diffAsync(t, tt.old, tt.new)
			if hasChange(changes, tt.prefix, Modified) {
				t.Errorf("expected no opaque Modified change at %s, got %+v", tt.prefix, changes)
			}
			c, ok := findChange(changes, tt.deepPath, Modified)
			if !ok {
				t.Fatalf("expected a deep change at %s, got %+v", tt.deepPath, changes)
			}
			if c.Classification != tt.wantDeep {
				t.Errorf("expected %s, got %s", tt.wantDeep, c.Classification)
			}
		})
	}
}

// TestDiffAsyncAPI_DemoFixtures runs the differ over the two real AsyncAPI 2.6
// documents shipped with the demo bundles, proving the engine against real
// content rather than only hand-built strings.
func TestDiffAsyncAPI_DemoFixtures(t *testing.T) {
	base := "../../examples/demo/bundles/payments-service"
	oldFS := os.DirFS(base + "/v1.0.0")
	newFS := os.DirFS(base + "/v2.0.1")

	changes := diffAsyncAPI("interfaces/events.json", "interfaces/events.json", oldFS, newFS)

	c, ok := findChange(changes, "asyncapi.channels[payment.completed]", Removed)
	if !ok {
		t.Fatalf("expected payment.completed removed, got %+v", changes)
	}
	if c.Classification != Breaking {
		t.Errorf("expected BREAKING for a removed channel, got %s", c.Classification)
	}
	if !hasChange(changes, "asyncapi.channels[payment.intent.created]", Added) {
		t.Errorf("expected payment.intent.created added, got %+v", changes)
	}
	// payment.refunded survives but its payload swapped charge_id for
	// payment_intent_id, which must surface as a deep required-set change.
	if !hasChange(changes, "asyncapi.channels[payment.refunded].publish.message.payload.required[charge_id]", Removed) {
		t.Errorf("expected removed required charge_id on payment.refunded, got %+v", changes)
	}
}

// An AsyncAPI message is a record: old and new publishers and subscribers
// coexist, so an edit has to be safe in both directions. Adding an optional
// field and rewording a description pass; widening an enum does not.
func TestDiffAsyncAPI_PayloadIsARecord(t *testing.T) {
	const orderID = `            order_id:
              type: string
          required: ["order_id"]`
	tests := []struct {
		name, from, to, path string
		ct                   ChangeType
		want                 Classification
	}{
		{"optional property added", orderID, `            order_id:
              type: string
            note:
              type: string
          required: ["order_id"]`,
			"asyncapi.channels[order.created].publish.message.payload.properties.note", Added, NonBreaking},
		{"enum introduced", orderID, `            order_id:
              type: string
              enum: [a]
          required: ["order_id"]`,
			"asyncapi.channels[order.created].publish.message.payload.properties.order_id.enum", Added, Breaking},
		{"description added", `      operationId: orderCreated`, `      operationId: orderCreated
      description: sent once per order`,
			"asyncapi.channels[order.created].publish.description", Added, NonBreaking},
		{"operation renamed", `      operationId: orderCreated`, `      operationId: orderPlaced`,
			"asyncapi.channels[order.created].publish.operationId", Modified, PotentialBreaking},
		{"2.x operation added to a channel", `  order.cancelled:
    publish:`, `  order.cancelled:
    subscribe:
      operationId: onOrderCancelled
    publish:`,
			"asyncapi.channels[order.cancelled].subscribe", Added, NonBreaking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := diffAsync(t, baseAsyncAPI, strings.Replace(baseAsyncAPI, tt.from, tt.to, 1))
			c, ok := findChange(changes, tt.path, tt.ct)
			if !ok || len(changes) != 1 {
				t.Fatalf("expected one change at %s, got %+v", tt.path, changes)
			}
			if c.Classification != tt.want {
				t.Errorf("got %s, want %s", c.Classification, tt.want)
			}
		})
	}
}

// Only the payload and headers schemas are graded as records. Tags document a
// channel, and any other edit outside the schemas (a binding, a message name)
// is POTENTIAL_BREAKING, in AsyncAPI 2.x and 3.x alike.
func TestDiffAsyncAPI_OnlySchemasAreRecords(t *testing.T) {
	const v3 = `asyncapi: 3.0.0
channels:
  orders:
    address: orders
    messages:
      created:
        payload: {type: object, properties: {id: {type: string}}}
`
	const v3New = `asyncapi: 3.0.0
channels:
  orders:
    address: orders
    tags: [{name: billing}]
    bindings: {kafka: {partitions: 3}}
    messages:
      created:
        name: OrderCreated
        tags: [{name: orders}]
        payload: {type: object, properties: {id: {type: string}}}
`
	const v2New = `asyncapi: "2.6.0"
channels:
  order.created:
    bindings: {kafka: {partitions: 3}}
    publish:
      operationId: orderCreated
      tags: [{name: billing}]
      message:
        name: OrderCreated
        payload:
          type: object
          properties:
            order_id:
              type: string
          required: ["order_id"]
  order.cancelled:
    publish:
      operationId: orderCancelled
      message:
        payload:
          type: object
          properties:
            order_id:
              type: string
`
	want := map[string]Classification{
		"asyncapi.channels[orders].tags":                        NonBreaking,
		"asyncapi.channels[orders].bindings":                    PotentialBreaking,
		"asyncapi.channels[orders].messages.created.name":       PotentialBreaking,
		"asyncapi.channels[orders].messages.created.tags":       NonBreaking,
		"asyncapi.channels[order.created].bindings":             PotentialBreaking,
		"asyncapi.channels[order.created].publish.tags":         NonBreaking,
		"asyncapi.channels[order.created].publish.message.name": PotentialBreaking,
	}
	changes := append(diffAsync(t, v3, v3New), diffAsync(t, strings.Replace(baseAsyncAPI, "info:\n  title: events\n  version: 1.0.0\n", "", 1), v2New)...)
	if len(changes) != len(want) {
		t.Fatalf("got %+v", changes)
	}
	for _, c := range changes {
		if w, ok := want[c.Path]; !ok || c.Classification != w {
			t.Errorf("%s: got %s, want %s", c.Path, c.Classification, w)
		}
	}
}

// A 2.x channel that flips from publish to subscribe changes who sends: the
// removed operation is BREAKING. A 2.x operationId rename stays
// POTENTIAL_BREAKING.
func TestDiffAsyncAPI_V2ActionFlip(t *testing.T) {
	flipped := strings.Replace(baseAsyncAPI, "  order.cancelled:\n    publish:", "  order.cancelled:\n    subscribe:", 1)
	changes := diffAsync(t, baseAsyncAPI, flipped)
	if c, ok := findChange(changes, "asyncapi.channels[order.cancelled].publish", Removed); !ok || c.Classification != Breaking {
		t.Fatalf("publish removed: got %+v", changes)
	}
	if c, ok := findChange(changes, "asyncapi.channels[order.cancelled].subscribe", Added); !ok || c.Classification != NonBreaking {
		t.Fatalf("subscribe added: got %+v", changes)
	}
}
