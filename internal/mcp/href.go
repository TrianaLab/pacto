package mcp

import (
	"context"
	"encoding/json"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/dashboard/productview"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// linkHrefs makes handler h's answers carry dashboard links: the legacy search,
// graph and status answers gain an href per service, node or item, and every
// href (the product tools' too) is prefixed with the dashboard's base URL. An
// empty prefix returns h unchanged, so a server without --dashboard-url never
// emits an href.
// ponytail: one extra decode and encode per call, only with --dashboard-url.
func linkHrefs[A any](tool, prefix string, h mcpsdk.ToolHandlerFor[A, any]) mcpsdk.ToolHandlerFor[A, any] {
	if prefix == "" {
		return h
	}
	return func(ctx context.Context, req *mcpsdk.CallToolRequest, a A) (*mcpsdk.CallToolResult, any, error) {
		res, out, err := h(ctx, req, a)
		if err != nil || res.IsError {
			return res, out, err
		}
		// Every fleet handler answers through jsonResult: one text block of JSON.
		dec := json.NewDecoder(strings.NewReader(res.Content[0].(*mcpsdk.TextContent).Text))
		dec.UseNumber() // counts and limits survive the round trip exactly
		var v any
		_ = dec.Decode(&v)
		r, err := jsonResult(prefixHrefs(addLegacyHrefs(tool, v), prefix))
		return r, out, err
	}
}

// addLegacyHrefs adds dashboard paths to the answers that have no product
// transport type: each search service and graph node (by service key), the
// graph root (as rootHref), each status item (by kind and name) and each
// contracted impact consumer (by domain and service).
func addLegacyHrefs(tool string, v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	service := func(o map[string]any) {
		o["href"] = productview.Entity(fleet.KindService, str(o["key"]))
	}
	switch tool {
	case "pacto_fleet_search":
		each(m["services"], service)
	case "pacto_fleet_graph":
		each(m["nodes"], service)
		if root := str(m["root"]); root != "" {
			m["rootHref"] = productview.Entity(fleet.KindService, root)
		}
	case "pacto_fleet_status":
		each(m["items"], func(o map[string]any) {
			o["href"] = productview.Entity(fleet.EntityKind(str(o["kind"])), str(o["name"]))
		})
	case "pacto_impact":
		// A caller with no contract has no entity page to link to.
		each(m["consumers"], func(o map[string]any) {
			if o["uncontracted"] != true {
				o["href"] = productview.Entity(fleet.KindService, string(fleet.NewServiceKeyDomain(str(o["domain"]), str(o["service"]))))
			}
		})
	}
	return m
}

// prefixHrefs prepends prefix to every href and rootHref string in v.
func prefixHrefs(v any, prefix string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok && (k == "href" || k == "rootHref") {
				t[k] = prefix + s
				continue
			}
			t[k] = prefixHrefs(val, prefix)
		}
	case []any:
		for i, val := range t {
			t[i] = prefixHrefs(val, prefix)
		}
	}
	return v
}

func each(v any, f func(map[string]any)) {
	list, _ := v.([]any)
	for _, it := range list {
		if o, ok := it.(map[string]any); ok {
			f(o)
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
