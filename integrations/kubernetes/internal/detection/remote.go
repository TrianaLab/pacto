/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/component"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/managedres"
	"github.com/trianalab/pacto/v4/pkg/managedres/cloud"
)

// defaultHTTP is the client of the state and inventory reads.
var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

// remoteRead is one state or inventory read, kept for an Interval.
type remoteRead struct {
	at           time.Time
	res          []fleet.RawManagedResource
	unclassified int
	err          error
}

// remote reads the listed Terraform states and, when a person named the
// cloud, its inventory, through the operator's workload identity only, and
// appends their entries and resources after the cluster objects. Nothing is
// read, and no entry written, with no state listed and no identity.
func (d *Detector) remote(ctx context.Context, res *detect.Result) {
	getenv := d.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	id, ok := cloud.WorkloadIdentity(d.Readers.WorkloadIdentity, getenv)
	if len(d.Readers.TerraformStates) > 0 {
		res.Entries = append(res.Entries, d.terraform(ctx, res, id, ok))
	}
	if e := d.cloudInventory(ctx, res, id, ok); e.Kind != "" {
		res.Entries = append(res.Entries, e)
	}
}

// terraform reads each listed state, at most once per Interval, and keeps
// each failure per location.
func (d *Detector) terraform(ctx context.Context, res *detect.Result, id cloud.Identity, ok bool) detect.Entry {
	var read []string
	failed := map[string]string{}
	for _, s := range d.Readers.TerraformStates {
		loc, err := managedres.ParseLocation(s)
		if err != nil {
			failed[s] = "not a listed state location"
			continue
		}
		rs, unclassified, err := d.cached("state "+loc.String(), func() ([]fleet.RawManagedResource, int, error) { return d.state(ctx, loc, id, ok) })
		if err != nil {
			failed[loc.String()] = cloudReason(err)
			continue
		}
		read = append(read, loc.String())
		res.ManagedResourcesUnclassified += unclassified
		d.keep(res, rs)
	}
	return detect.TerraformEntry(read, failed)
}

// state fetches and parses one state; only names are kept, with the count
// of instances of types no catalog row names.
func (d *Detector) state(ctx context.Context, loc managedres.Location, id cloud.Identity, ok bool) ([]fleet.RawManagedResource, int, error) {
	if !ok {
		return nil, 0, errors.New(id.Reason)
	}
	body, err := cloud.FetchState(ctx, loc, id, d.client())
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = body.Close() }()
	return managedres.ParseState(body, loc.String(), externalcatalog.Default(), d.Now().UTC())
}

// cloudInventory reads the cloud inventory only when controller.cloudInventory
// names a cloud and the workload identity of that cloud holds; off with an
// identity, the entry names the opt-in.
func (d *Detector) cloudInventory(ctx context.Context, res *detect.Result, id cloud.Identity, ok bool) detect.Entry {
	want := d.Readers.CloudInventory
	if want == "" || want == "off" {
		return detect.CloudInventoryEntry("off", id.String(), id.Reason, ok, nil)
	}
	if ok && id.Cloud != want {
		ok, id.Reason = false, "no read-only "+want+" workload identity on the operator ServiceAccount, only "+id.String()
	}
	if !ok {
		return detect.CloudInventoryEntry(want, "", id.Reason, false, nil)
	}
	rs, _, err := d.cached("inventory "+want, func() ([]fleet.RawManagedResource, int, error) {
		rs, err := cloud.Inventory(ctx, id, externalcatalog.Default(), d.client(), d.Now().UTC())
		return rs, 0, err // the inventory queries only the catalog's types
	})
	d.keep(res, rs) // a truncated read keeps the rows it read
	if err != nil {
		err = errors.New(cloudReason(err))
	}
	return detect.CloudInventoryEntry(want, id.String(), "", true, err)
}

// cached runs read unless the same key was read less than an Interval ago.
func (d *Detector) cached(key string, read func() ([]fleet.RawManagedResource, int, error)) ([]fleet.RawManagedResource, int, error) {
	if c, ok := d.reads[key]; ok && d.Now().Sub(c.at) < cmp.Or(d.Interval, component.DefaultInterval) {
		return c.res, c.unclassified, c.err
	}
	rs, unclassified, err := read()
	if d.reads == nil {
		d.reads = map[string]remoteRead{}
	}
	d.reads[key] = remoteRead{at: d.Now(), res: rs, unclassified: unclassified, err: err}
	return rs, unclassified, err
}

// keep counts rs and keeps each, stamped with this cluster, while fewer
// than detect.MaxManagedResources are kept: the cluster objects come first,
// then the states, then the inventory.
func (d *Detector) keep(res *detect.Result, rs []fleet.RawManagedResource) {
	for _, r := range rs {
		if res.ManagedResourcesTotal++; len(res.ManagedResources) < detect.MaxManagedResources {
			r.Cluster, r.ClusterName = d.Cluster, d.ClusterName
			res.ManagedResources = append(res.ManagedResources, r)
		}
	}
}

// client is the HTTP client of the remote reads.
func (d *Detector) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return defaultHTTP
}

// cloudReason is err without an endpoint address: a transport failure
// reads as the endpoint not answering.
func cloudReason(err error) string {
	if ue := (*url.Error)(nil); errors.As(err, &ue) {
		return "the cloud endpoint did not answer"
	}
	return err.Error()
}
