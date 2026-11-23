package cloud

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// gcpAssetScope is the OAuth scope Cloud Asset search accepts; the role
// bound to the service account decides that it only reads.
const gcpAssetScope = "https://www.googleapis.com/auth/cloud-platform"

// gcpPageSize is the Cloud Asset search page.
const gcpPageSize = 500

// gcpPager searches the Cloud Asset Inventory of the annotated service
// account's project for types, once the metadata server confirms the pod
// runs as that account.
func gcpPager(ctx context.Context, id Identity, hc *http.Client, types []string) (pager, error) {
	tok, err := gcpToken(ctx, id, hc, gcpAssetScope)
	if err != nil {
		return pager{}, err
	}
	project, err := gcpProject(id.Name)
	if err != nil {
		return pager{}, err
	}
	return pager{
		request: func(token string) (*http.Request, error) {
			q := url.Values{"assetTypes": types, "pageSize": {strconv.Itoa(gcpPageSize)}}
			if token != "" {
				q.Set("pageToken", token)
			}
			return bearer(ctx, http.MethodGet, endpoint(endpoints.gcpAsset, "{project}", project)+"?"+q.Encode(), tok, nil)
		},
		decode: func(b []byte) ([]row, string, error) { return decodeGCP(b, project) },
	}, nil
}

// decodeGCP reads a searchAllResources answer of project: only each
// result's name, asset type, display name and location are decoded; its
// labels and attributes never are.
func decodeGCP(b []byte, project string) ([]row, string, error) {
	var page struct {
		Results []struct {
			Name, AssetType, DisplayName, Location string
		}
		NextPageToken string
	}
	if err := decodeJSON(b, &page); err != nil {
		return nil, "", err
	}
	rows := make([]row, 0, len(page.Results))
	for _, r := range page.Results {
		id := cmp.Or(r.DisplayName, r.Name[strings.LastIndex(r.Name, "/")+1:])
		rows = append(rows, row{typ: r.AssetType, account: project, region: r.Location, id: id})
	}
	return rows, page.NextPageToken, nil
}
