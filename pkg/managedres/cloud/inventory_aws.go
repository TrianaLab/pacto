package cloud

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// awsPager searches AWS Resource Explorer, in the identity's region, for
// types, with SigV4. Without IRSA or Pod Identity nothing is asked.
func awsPager(ctx context.Context, types []string) (pager, error) {
	creds, region, err := awsCredentials(ctx)
	if err != nil {
		return pager{}, err
	}
	if region == "" {
		return pager{}, errors.New("no AWS region in the environment (AWS_REGION)")
	}
	query := "resourcetype:" + strings.Join(types, " resourcetype:")
	return pager{
		request: func(token string) (*http.Request, error) {
			body, _ := json.Marshal(struct { // ponytail: strings and an int always marshal
				QueryString string
				MaxResults  int
				NextToken   string `json:",omitempty"`
			}{query, MaxInventoryItems, token})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(endpoints.awsExplorer, "{region}", region), bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			return req, sign(ctx, req, creds, body, "resource-explorer-2", region)
		},
		decode: decodeAWS,
	}, nil
}

// decodeAWS reads a Search answer: only each resource's ARN, type, region
// and account are decoded; its properties never are.
func decodeAWS(b []byte) ([]row, string, error) {
	var page struct {
		Resources []struct{ Arn, ResourceType, Region, OwningAccountId string }
		NextToken string
	}
	if err := decodeJSON(b, &page); err != nil {
		return nil, "", err
	}
	rows := make([]row, 0, len(page.Resources))
	for _, r := range page.Resources {
		parts := strings.SplitN(r.Arn, ":", 6)
		account, id := r.OwningAccountId, ""
		if len(parts) == 6 {
			account = cmp.Or(account, parts[4])
			id = parts[5][strings.LastIndexAny(parts[5], ":/")+1:]
		}
		rows = append(rows, row{typ: r.ResourceType, account: account, region: r.Region, id: id})
	}
	return rows, page.NextToken, nil
}
