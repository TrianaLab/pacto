package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/managedres"
)

// MaxInventoryItems bounds the rows one cloud inventory read keeps.
//
// ponytail: one read per detection interval keeps at most this many rows,
// and the detected ConfigMap keeps fewer (detect.MaxManagedResources); page
// through a narrower query per type if real accounts hit it.
const MaxInventoryItems = 1000

// maxPageBytes bounds one inventory answer.
const maxPageBytes = 16 << 20

// ErrTruncated says an inventory read stopped at [MaxInventoryItems]; the
// rows read so far are returned with it.
var ErrTruncated = fmt.Errorf("stopped at %d inventory rows", MaxInventoryItems)

// The [fleet.RawManagedResource] sources of the inventory readers.
const (
	SourceAWS   = "aws-resource-explorer"
	SourceGCP   = "gcp-cloud-asset"
	SourceAzure = "azure-resource-graph"
)

// row is the part of one inventory row kept: nothing else of a row, its
// tags and properties included, is ever decoded into a returned value.
type row struct{ typ, account, region, id string }

// Inventory lists the managed resources of the catalog's inventory types of
// id's cloud through id, with hc, observed at now: AWS Resource Explorer,
// GCP Cloud Asset Inventory or Azure Resource Graph. Each row keeps only its
// type, id, region and account; a type no catalog row names is skipped. At
// [MaxInventoryItems] rows it stops with [ErrTruncated].
func Inventory(ctx context.Context, id Identity, cat *externalcatalog.Catalog, hc *http.Client, now time.Time) ([]fleet.RawManagedResource, error) {
	var (
		pages  pager
		err    error
		source = map[string]string{CloudAWS: SourceAWS, CloudGCP: SourceGCP, CloudAzure: SourceAzure}[id.Cloud]
		types  = cat.InventoryTypes(id.Cloud)
	)
	switch {
	case source == "":
		return nil, fmt.Errorf("no cloud inventory for %q", id.Cloud)
	case len(types) == 0:
		return nil, nil // an empty query would list every resource
	case id.Cloud == CloudAWS:
		pages, err = awsPager(ctx, types)
	case id.Cloud == CloudGCP:
		pages, err = gcpPager(ctx, id, hc, types)
	default:
		pages, err = azurePager(ctx, id, types)
	}
	if err != nil {
		return nil, err
	}
	return collect(hc, pages, func(r row) (fleet.RawManagedResource, bool) {
		return managedres.Inventoried(cat, source, r.typ, r.account, r.region, r.id, now)
	})
}

// pager builds the request of the page after token ("" for the first) and
// decodes its answer into rows and the next token.
type pager struct {
	request func(token string) (*http.Request, error)
	decode  func(body []byte) ([]row, string, error)
}

// collect reads every page while fewer than MaxInventoryItems rows are
// kept, mapping each row with keep.
func collect(hc *http.Client, p pager, keep func(row) (fleet.RawManagedResource, bool)) ([]fleet.RawManagedResource, error) {
	var out []fleet.RawManagedResource
	token, seen := "", 0
	for {
		rows, next, err := page(hc, p, token)
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			if seen++; seen > MaxInventoryItems {
				return out, ErrTruncated
			}
			if m, ok := keep(r); ok {
				out = append(out, m)
			}
		}
		if token = next; token == "" {
			return out, nil
		}
	}
}

// page reads one page.
func page(hc *http.Client, p pager, token string) ([]row, string, error) {
	req, err := p.request(token)
	if err != nil {
		return nil, "", err
	}
	body, err := do(hc, req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = body.Close() }()
	b, err := io.ReadAll(io.LimitReader(body, maxPageBytes+1))
	switch {
	case err != nil:
		return nil, "", err
	case len(b) > maxPageBytes:
		return nil, "", errors.New("an inventory page is over 16 MiB")
	}
	return p.decode(b)
}

// decodeJSON decodes an inventory answer into v.
func decodeJSON(b []byte, v any) error {
	if err := json.Unmarshal(b, v); err != nil {
		return errors.New("the inventory answer is not valid JSON")
	}
	return nil
}
