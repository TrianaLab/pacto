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
