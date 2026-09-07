package diff

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func makeProtoFS(content string) fstest.MapFS {
	return fstest.MapFS{
		"api.proto": &fstest.MapFile{Data: []byte(content)},
	}
}

// baseProto exercises both comment styles, an option line whose string literal
// contains "//", a streaming rpc, scalar/repeated/optional/map fields and the
// nested constructs the extractor must skip.
const baseProto = `syntax = "proto3";

package orders.v1;

option go_package = "github.com/trianalab/pacto/gen/orders/v1";

// The order service.
service OrderService {
  rpc GetOrder(GetOrderRequest) returns (GetOrderResponse);
  /* Streaming feed of order events.
     Spans several lines. */
  rpc WatchOrders(WatchRequest) returns (stream OrderEvent);
}

message GetOrderRequest {
  string order_id = 1;
}

message GetOrderResponse {
  string order_id = 1;
  repeated string items = 2;
  map<string, string> labels = 3;
  optional string note = 4;

  message Nested {
    string ignored = 1;
  }

  oneof result {
    string ok = 5;
    string err = 6;
  }

  reserved 7, 8;
  option deprecated = 9;
  ;
}
`

func diffProto(t *testing.T, oldSrc, newSrc string) []Change {
	t.Helper()
	return diffGRPC("api.proto", "api.proto", makeProtoFS(oldSrc), makeProtoFS(newSrc))
}

func TestDiffGRPC_Guards(t *testing.T) {
	fsys := makeProtoFS(baseProto)
	tests := []struct {
		name             string
		oldPath, newPath string
		oldFS, newFS     fstest.MapFS
	}{
		{name: "empty paths", oldFS: fsys, newFS: fsys},
		{name: "empty old path", newPath: "api.proto", oldFS: fsys, newFS: fsys},
		{name: "empty new path", oldPath: "api.proto", oldFS: fsys, newFS: fsys},
		{name: "old missing file", oldPath: "api.proto", newPath: "api.proto", oldFS: fstest.MapFS{}, newFS: fsys},
		{name: "new missing file", oldPath: "api.proto", newPath: "api.proto", oldFS: fsys, newFS: fstest.MapFS{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if changes := diffGRPC(tt.oldPath, tt.newPath, tt.oldFS, tt.newFS); len(changes) != 0 {
				t.Errorf("expected 0 changes, got %d", len(changes))
			}
		})
	}
}

func TestDiffGRPC_NilFS(t *testing.T) {
	fsys := makeProtoFS(baseProto)
	if changes := diffGRPC("api.proto", "api.proto", nil, nil); len(changes) != 0 {
		t.Errorf("both nil: expected 0 changes, got %d", len(changes))
	}
	if changes := diffGRPC("api.proto", "api.proto", nil, fsys); len(changes) != 0 {
		t.Errorf("old nil: expected 0 changes, got %d", len(changes))
	}
	if changes := diffGRPC("api.proto", "api.proto", fsys, nil); len(changes) != 0 {
		t.Errorf("new nil: expected 0 changes, got %d", len(changes))
	}
}

func TestDiffGRPC_Identical(t *testing.T) {
	if changes := diffProto(t, baseProto, baseProto); len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d: %+v", len(changes), changes)
	}
}

// An unbalanced block is not an error to report: it simply yields no changes.
func TestDiffGRPC_UnbalancedBraces(t *testing.T) {
	if changes := diffProto(t, baseProto, "message Broken {\n  string a = 1;\n"); !hasChange(changes, "grpc.services[OrderService]", Removed) {
		t.Errorf("expected the old side to still be extracted, got %+v", changes)
	}
	if changes := diffProto(t, "message Broken {\n", "message Broken {\n"); len(changes) != 0 {
		t.Errorf("expected 0 changes for two unparseable files, got %+v", changes)
	}
}

// A file with no service or message block at all stops the scan immediately.
func TestDiffGRPC_NoBlocks(t *testing.T) {
	only := `syntax = "proto3";
package orders.v1;
`
	if changes := diffProto(t, only, only); len(changes) != 0 {
		t.Errorf("expected 0 changes, got %+v", changes)
	}
}

// Comments must be stripped before scanning: the same API written with and
// without comments diffs clean.
func TestDiffGRPC_CommentsStripped(t *testing.T) {
	uncommented := `syntax = "proto3";

service OrderService {
  rpc GetOrder(GetOrderRequest) returns (GetOrderResponse);
  rpc WatchOrders(WatchRequest) returns (stream OrderEvent);
}
`
	commented := `syntax = "proto3";

// leading line comment
/* leading block comment */
service OrderService {
  // fetch one order
  rpc GetOrder(GetOrderRequest) returns (GetOrderResponse); // trailing
  /* stream them */
  rpc WatchOrders(WatchRequest) returns (stream OrderEvent);
}
`
	if changes := diffProto(t, uncommented, commented); len(changes) != 0 {
		t.Errorf("expected 0 changes, got %+v", changes)
	}
}

// A `//` inside a string literal must not truncate the line. Before the
// single-pass pre-scan it ate the closing brace of the enclosing message, the
// braces never balanced, and the WHOLE FILE extracted as an empty surface — so
// diffing it against anything reported every service and message as removed.
func TestExtractProto_CommentMarkerInsideStringLiteral(t *testing.T) {
	src := `syntax = "proto3";

message Order {
  option (my.ext) = { doc: "https://example.com/orders" };
  string id = 1;
}

service OrderService {
  rpc GetOrder(Order) returns (Order);
}
`
	api := extractProto(src)
	if got := api.messages["Order"]["id"]; got != "string = 1" {
		t.Errorf("Order.id = %q, want %q (surface: %+v)", got, "string = 1", api)
	}
	if got := api.services["OrderService"]["GetOrder"]; got != "(Order) returns (Order)" {
		t.Errorf("OrderService.GetOrder = %q (surface: %+v)", got, api)
	}
	if changes := diffProto(t, src, src); len(changes) != 0 {
		t.Errorf("expected 0 changes against itself, got %+v", changes)
	}
}

// A `}` inside a string literal must not close the enclosing block early and
// discard every declaration after it.
func TestExtractProto_BraceInsideStringLiteral(t *testing.T) {
	src := `syntax = "proto3";

message Order {
  option (my.ext) = "a } b";
  string id = 1;
}
`
	if got := extractProto(src).messages["Order"]["id"]; got != "string = 1" {
		t.Errorf("Order.id = %q, want %q", got, "string = 1")
	}
}

// Both proto quote styles, and a backslash-escaped quote that must not end the
// literal. Every marker inside these strings is inert.
func TestExtractProto_QuoteStylesAndEscapes(t *testing.T) {
	src := `syntax = "proto3";

message Order {
  option (a) = "he said \"} // still a string\" and stopped";
  option (b) = 'single quoted } /* also inert */';
  string id = 1;
}
`
	got := extractProto(src).messages["Order"]
	want := map[string]string{"id": "string = 1"}
	if len(got) != len(want) || got["id"] != want["id"] {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

// Comment-vs-comment precedence: a `/*` that appears inside a line comment is
// not a block-comment opener. The old two-regex strip ran the block pattern
// first, so this swallowed everything up to the next `*/` in the file.
func TestExtractProto_BlockMarkerInsideLineComment(t *testing.T) {
	src := `syntax = "proto3";

// TODO /* revisit this before v2
message A {
  string a = 1;
}

/* a genuine block comment */
message B {
  string b = 1;
}
`
	api := extractProto(src)
	if got := api.messages["A"]["a"]; got != "string = 1" {
		t.Errorf("message A was swallowed by a phantom block comment: %+v", api.messages)
	}
	if got := api.messages["B"]["b"]; got != "string = 1" {
		t.Errorf("B.b = %q, want %q", got, "string = 1")
	}
}

// Unterminated noise must not panic, read past the end of the buffer, or run
// away and blank the rest of the file.
func TestExtractProto_UnterminatedNoise(t *testing.T) {
	tests := map[string]string{
		"unterminated block comment": "message A {\n  string a = 1;\n}\n/* dangling",
		"trailing slash":             "message A {\n  string a = 1;\n}\n/",
		"trailing star":              "message A {\n  string a = 1;\n}\n/* x *",
		"trailing backslash":         "message A {\n  string a = 1;\n}\noption x = \"y\\",
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			if got := extractProto(src).messages["A"]["a"]; got != "string = 1" {
				t.Errorf("A.a = %q, want %q", got, "string = 1")
			}
		})
	}
}

// A proto string literal cannot span a raw newline, so a quote with no closing
// quote before the line break is not a literal at all. Nothing on that line is
// blanked, which keeps the statement's terminating `;` — blanking it would run
// the malformed statement into the next declaration and lose a real field.
func TestExtractProto_UnterminatedStringStopsAtNewline(t *testing.T) {
	api := extractProto(`message A {
  option x = "dangling;
  string a = 1;
}

message B {
  string b = 1;
}
`)
	if got := api.messages["A"]["a"]; got != "string = 1" {
		t.Errorf("the unterminated literal swallowed A.a: %+v", api.messages)
	}
	if got := api.messages["B"]["b"]; got != "string = 1" {
		t.Errorf("the runaway literal swallowed message B: %+v", api.messages)
	}
}

// A missing closing quote must not fabricate a change. The only difference
// between these two sources is that one option value is never closed; blanking
// to the newline ate its `;`, the next field was swallowed by the run-on
// statement and the diff reported a BREAKING field removal that never happened.
func TestDiffGRPC_UnterminatedStringIsNotABreakingChange(t *testing.T) {
	const (
		closed   = "syntax = \"proto3\";\n\nmessage Account {\n  option (my.ext) = \"ok\";\n  string email = 1;\n  int32 balance = 2;\n}\n"
		unclosed = "syntax = \"proto3\";\n\nmessage Account {\n  option (my.ext) = \"ok;\n  string email = 1;\n  int32 balance = 2;\n}\n"
	)
	want := map[string]string{"email": "string = 1", "balance": "int32 = 2"}

	for name, src := range map[string]string{"closed": closed, "unclosed": unclosed} {
		t.Run(name, func(t *testing.T) {
			got := extractProto(src).messages["Account"]
			if len(got) != len(want) {
				t.Fatalf("fields = %v, want %v", got, want)
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("field %s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
	if changes := diffProto(t, closed, unclosed); len(changes) != 0 {
		t.Errorf("expected 0 changes, got %+v", changes)
	}
}

// Two unterminated literals in one body must each stop at their own line
// instead of chaining into one run-on statement that eats every field.
func TestExtractProto_TwoUnterminatedStringsKeepEveryField(t *testing.T) {
	got := extractProto("message A {\n  option a = \"x;\n  string keep = 1;\n  option b = \"y;\n  string alsokeep = 2;\n}\n").messages["A"]
	want := map[string]string{"keep": "string = 1", "alsokeep": "string = 2"}
	if len(got) != len(want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("field %s = %q, want %q", k, got[k], v)
		}
	}
}

// Inline field options are recognised but not recorded: a retype behind an
// option block is still a Modified field, and adding an option block to an
// otherwise unchanged field is not a change at all.
func TestDiffGRPC_InlineFieldOptions(t *testing.T) {
	const (
		plain      = "message Account {\n  string email = 1;\n  int32 amount = 3;\n}\n"
		optioned   = "message Account {\n  string email = 1 [deprecated = true];\n  int32 amount = 3 [deprecated = true];\n}\n"
		retypedOpt = "message Account {\n  string email = 1 [deprecated = true];\n  string amount = 3 [deprecated = true];\n}\n"
	)

	t.Run("adding an option block is a no-op", func(t *testing.T) {
		if changes := diffProto(t, plain, optioned); len(changes) != 0 {
			t.Errorf("expected 0 changes, got %+v", changes)
		}
	})
	t.Run("removing an option block is a no-op", func(t *testing.T) {
		if changes := diffProto(t, optioned, plain); len(changes) != 0 {
			t.Errorf("expected 0 changes, got %+v", changes)
		}
	})
	t.Run("a retype behind an option block is still BREAKING", func(t *testing.T) {
		changes := diffProto(t, optioned, retypedOpt)
		c, ok := findChange(changes, "grpc.messages[Account].fields[amount]", Modified)
		if !ok {
			t.Fatalf("expected the retype to surface, got %+v", changes)
		}
		if c.OldValue != "int32 = 3" || c.NewValue != "string = 3" {
			t.Errorf("expected int32 = 3 -> string = 3, got %v -> %v", c.OldValue, c.NewValue)
		}
		if c.Classification != Breaking {
			t.Errorf("expected BREAKING, got %s", c.Classification)
		}
	})
	t.Run("an option block containing a string is not truncated", func(t *testing.T) {
		withStr := "message Account {\n  string email = 1 [(v.rules).string.pattern = \"^[a-z]+$\"];\n}\n"
		if got := extractProto(withStr).messages["Account"]["email"]; got != "string = 1" {
			t.Errorf("Account.email = %q, want %q", got, "string = 1")
		}
	})
}

func TestDiffGRPC_ServiceRemovedAndAdded(t *testing.T) {
	renamed := strings.Replace(baseProto, "service OrderService {", "service OrdersV2 {", 1)
	changes := diffProto(t, baseProto, renamed)

	removed, ok := findChange(changes, "grpc.services[OrderService]", Removed)
	if !ok {
		t.Fatalf("expected service removed, got %+v", changes)
	}
	if removed.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", removed.Classification)
	}
	if removed.Reason != "service OrderService removed" {
		t.Errorf("unexpected reason %q", removed.Reason)
	}

	added, ok := findChange(changes, "grpc.services[OrdersV2]", Added)
	if !ok {
		t.Fatalf("expected service added, got %+v", changes)
	}
	if added.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", added.Classification)
	}
	if added.Reason != "service OrdersV2 added" {
		t.Errorf("unexpected reason %q", added.Reason)
	}
}

func TestDiffGRPC_RPCRemoved(t *testing.T) {
	newSrc := strings.Replace(baseProto,
		"  rpc WatchOrders(WatchRequest) returns (stream OrderEvent);\n", "", 1)
	changes := diffProto(t, baseProto, newSrc)

	c, ok := findChange(changes, "grpc.rpcs[OrderService.WatchOrders]", Removed)
	if !ok {
		t.Fatalf("expected rpc removed, got %+v", changes)
	}
	if c.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", c.Classification)
	}
	if c.OldValue != "(WatchRequest) returns (stream OrderEvent)" {
		t.Errorf("unexpected OldValue %v", c.OldValue)
	}
	if c.Reason != "rpc OrderService.WatchOrders removed" {
		t.Errorf("unexpected reason %q", c.Reason)
	}
}

func TestDiffGRPC_RPCAdded(t *testing.T) {
	oldSrc := strings.Replace(baseProto,
		"  rpc WatchOrders(WatchRequest) returns (stream OrderEvent);\n", "", 1)
	changes := diffProto(t, oldSrc, baseProto)

	c, ok := findChange(changes, "grpc.rpcs[OrderService.WatchOrders]", Added)
	if !ok {
		t.Fatalf("expected rpc added, got %+v", changes)
	}
	if c.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", c.Classification)
	}
	if c.Reason != "rpc OrderService.WatchOrders added" {
		t.Errorf("unexpected reason %q", c.Reason)
	}
}

// Turning a unary rpc into a server-streaming one changes the wire contract.
func TestDiffGRPC_RPCSignatureChanged(t *testing.T) {
	newSrc := strings.Replace(baseProto,
		"rpc GetOrder(GetOrderRequest) returns (GetOrderResponse);",
		"rpc GetOrder(GetOrderRequest) returns (stream GetOrderResponse);", 1)
	changes := diffProto(t, baseProto, newSrc)

	c, ok := findChange(changes, "grpc.rpcs[OrderService.GetOrder]", Modified)
	if !ok {
		t.Fatalf("expected rpc modified, got %+v", changes)
	}
	if c.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", c.Classification)
	}
	if c.OldValue != "(GetOrderRequest) returns (GetOrderResponse)" ||
		c.NewValue != "(GetOrderRequest) returns (stream GetOrderResponse)" {
		t.Errorf("unexpected signatures %v -> %v", c.OldValue, c.NewValue)
	}
}

// Reformatting an rpc across lines is not a signature change.
func TestDiffGRPC_RPCWhitespaceCollapsed(t *testing.T) {
	newSrc := strings.Replace(baseProto,
		"rpc GetOrder(GetOrderRequest) returns (GetOrderResponse);",
		"rpc GetOrder(\n    GetOrderRequest\n  ) returns (\n    GetOrderResponse\n  );", 1)
	if changes := diffProto(t, baseProto, newSrc); len(changes) != 0 {
		t.Errorf("expected 0 changes, got %+v", changes)
	}
}

func TestDiffGRPC_MessageRemovedAndAdded(t *testing.T) {
	renamed := strings.Replace(baseProto, "message GetOrderRequest {", "message FetchOrderRequest {", 1)
	changes := diffProto(t, baseProto, renamed)

	removed, ok := findChange(changes, "grpc.messages[GetOrderRequest]", Removed)
	if !ok {
		t.Fatalf("expected message removed, got %+v", changes)
	}
	if removed.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", removed.Classification)
	}
	if removed.Reason != "message GetOrderRequest removed" {
		t.Errorf("unexpected reason %q", removed.Reason)
	}

	added, ok := findChange(changes, "grpc.messages[FetchOrderRequest]", Added)
	if !ok {
		t.Fatalf("expected message added, got %+v", changes)
	}
	if added.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", added.Classification)
	}
	if added.Reason != "message FetchOrderRequest added" {
		t.Errorf("unexpected reason %q", added.Reason)
	}
}

func TestDiffGRPC_FieldChanges(t *testing.T) {
	tests := []struct {
		name           string
		old, new       string
		path           string
		ct             ChangeType
		oldVal, newVal string
		want           Classification
	}{
		{
			name: "added", old: "", new: "  int64 total_cents = 9;\n",
			path: "grpc.messages[GetOrderResponse].fields[total_cents]", ct: Added,
			newVal: "int64 = 9", want: NonBreaking,
		},
		{
			name: "removed", old: "  repeated string items = 2;\n", new: "",
			path: "grpc.messages[GetOrderResponse].fields[items]", ct: Removed,
			oldVal: "repeated string = 2", want: Breaking,
		},
		{
			name: "retyped", old: "  string order_id = 1;\n  repeated", new: "  int64 order_id = 1;\n  repeated",
			path: "grpc.messages[GetOrderResponse].fields[order_id]", ct: Modified,
			oldVal: "string = 1", newVal: "int64 = 1", want: Breaking,
		},
		{
			name: "renumbered", old: "  optional string note = 4;", new: "  optional string note = 14;",
			path: "grpc.messages[GetOrderResponse].fields[note]", ct: Modified,
			oldVal: "optional string = 4", newVal: "optional string = 14", want: Breaking,
		},
		{
			name: "map value retyped", old: "  map<string, string> labels = 3;", new: "  map<string, int32> labels = 3;",
			path: "grpc.messages[GetOrderResponse].fields[labels]", ct: Modified,
			oldVal: "map<string, string> = 3", newVal: "map<string, int32> = 3", want: Breaking,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var newSrc string
			if tt.old == "" {
				newSrc = strings.Replace(baseProto, "  string order_id = 1;\n  repeated",
					"  string order_id = 1;\n"+tt.new+"  repeated", 1)
			} else {
				newSrc = strings.Replace(baseProto, tt.old, tt.new, 1)
			}
			changes := diffProto(t, baseProto, newSrc)

			c, ok := findChange(changes, tt.path, tt.ct)
			if !ok {
				t.Fatalf("expected %s at %s, got %+v", tt.ct, tt.path, changes)
			}
			if c.Classification != tt.want {
				t.Errorf("expected %s, got %s", tt.want, c.Classification)
			}
			if tt.oldVal != "" && c.OldValue != tt.oldVal {
				t.Errorf("expected OldValue %q, got %v", tt.oldVal, c.OldValue)
			}
			if tt.newVal != "" && c.NewValue != tt.newVal {
				t.Errorf("expected NewValue %q, got %v", tt.newVal, c.NewValue)
			}
		})
	}
}

// Nested messages, oneof bodies, reserved ranges and option statements are not
// fields, and a nested message is not a top-level message either.
func TestExtractProto_SkipsNonFields(t *testing.T) {
	api := extractProto(baseProto)

	if _, ok := api.messages["Nested"]; ok {
		t.Error("nested message must not be extracted as a top-level message")
	}
	for _, name := range []string{"ignored", "ok", "err", "deprecated", "reserved"} {
		if _, ok := api.messages["GetOrderResponse"][name]; ok {
			t.Errorf("%q must not be extracted as a field of GetOrderResponse", name)
		}
	}
	want := map[string]string{
		"order_id": "string = 1",
		"items":    "repeated string = 2",
		"labels":   "map<string, string> = 3",
		"note":     "optional string = 4",
	}
	for k, v := range want {
		if got := api.messages["GetOrderResponse"][k]; got != v {
			t.Errorf("field %s = %q, want %q", k, got, v)
		}
	}
	if len(api.messages["GetOrderResponse"]) != len(want) {
		t.Errorf("got %d fields, want %d: %v", len(api.messages["GetOrderResponse"]), len(want), api.messages["GetOrderResponse"])
	}
}

// TestExtractProto_DemoFixture runs the extractor over the real proto3 file
// shipped with the demo bundles.
func TestExtractProto_DemoFixture(t *testing.T) {
	data, err := os.ReadFile("../../examples/demo/bundles/fraud-service/interfaces/fraud.proto")
	if err != nil {
		t.Fatal(err)
	}
	api := extractProto(string(data))

	rpcs, ok := api.services["FraudService"]
	if !ok {
		t.Fatalf("expected service FraudService, got %v", api.services)
	}
	if len(rpcs) != 2 {
		t.Fatalf("expected 2 rpcs, got %v", rpcs)
	}
	if got := rpcs["EvaluateTransaction"]; got != "(EvaluateTransactionRequest) returns (EvaluateTransactionResponse)" {
		t.Errorf("unexpected EvaluateTransaction signature %q", got)
	}
	if got := rpcs["ReportFraud"]; got != "(ReportFraudRequest) returns (ReportFraudResponse)" {
		t.Errorf("unexpected ReportFraud signature %q", got)
	}
	if len(api.messages) != 4 {
		t.Fatalf("expected 4 messages, got %v", api.messages)
	}
	for _, name := range []string{
		"EvaluateTransactionRequest", "EvaluateTransactionResponse",
		"ReportFraudRequest", "ReportFraudResponse",
	} {
		if len(api.messages[name]) == 0 {
			t.Errorf("expected message %s with fields, got %v", name, api.messages[name])
		}
	}
	if got := api.messages["EvaluateTransactionRequest"]["metadata"]; got != "map<string, string> = 7" {
		t.Errorf("unexpected metadata field %q", got)
	}
	if got := api.messages["EvaluateTransactionResponse"]["risk_signals"]; got != "repeated string = 4" {
		t.Errorf("unexpected risk_signals field %q", got)
	}
	if got := api.messages["ReportFraudResponse"]["acknowledged"]; got != "bool = 1" {
		t.Errorf("unexpected acknowledged field %q", got)
	}
}
