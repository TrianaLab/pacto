package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// azureManagementScope is the token scope of Azure Resource Graph.
const azureManagementScope = "https://management.azure.com/.default"

// azurePager queries Azure Resource Graph for types, across the
// subscriptions the workload identity may read. The types are catalog
// values, which never hold a quote.
func azurePager(ctx context.Context, id Identity, types []string) (pager, error) {
	tok, err := azureToken(ctx, id, azureManagementScope)
	if err != nil {
		return pager{}, err
	}
	kql := "Resources | where type in~ ('" + strings.Join(types, "', '") + "') | project id, type, location, subscriptionId, name"
	return pager{
		request: func(token string) (*http.Request, error) {
			opts := map[string]any{"$top": MaxInventoryItems, "resultFormat": "objectArray"}
			if token != "" {
				opts["$skipToken"] = token
			}
			body, _ := json.Marshal(map[string]any{"query": kql, "options": opts}) // ponytail: strings and ints always marshal
			req, err := bearer(ctx, http.MethodPost, endpoints.azureGraph, tok, bytes.NewReader(body))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
			}
			return req, err
		},
		decode: decodeAzure,
	}, nil
}

// decodeAzure reads a Resource Graph answer: only the projected type,
// subscription, location and name are decoded.
func decodeAzure(b []byte) ([]row, string, error) {
	var page struct {
		Data []struct {
			Type, Location, SubscriptionID, Name string
		} `json:"data"`
		SkipToken string `json:"$skipToken"`
	}
	if err := decodeJSON(b, &page); err != nil {
		return nil, "", err
	}
	rows := make([]row, 0, len(page.Data))
	for _, r := range page.Data {
		rows = append(rows, row{typ: r.Type, account: r.SubscriptionID, region: r.Location, id: r.Name})
	}
	return rows, page.SkipToken, nil
}
