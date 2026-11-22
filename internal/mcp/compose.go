package mcp

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// Modes are the tool families one server serves beside the contract tools.
// Any combination may be set; a nil mode registers nothing and its
// instructions are left out.
type Modes struct {
	Fleet    *FleetMode
	Delivery *DeliveryMode
	Bundle   *BundleMode
	// Transport is how the server answers (stdio or http), as
	// pacto_capabilities reports it.
	Transport string
	// FleetOnlyHint, when set, is a server for callers whose token reads the
	// fleet only: pacto_capabilities says so and how to get more. Empty for
	// every server whose callers hold a credential.
	FleetOnlyHint string
	// DeliveryHidden: the host serves delivery, but not to this server's
	// callers (a fleet-only server), so installation does not read it as
	// absent.
	DeliveryHidden bool
	// publish is set by NewPublishServer, the one server that serves it.
	publish bool
}

// FleetMode serves the read-only fleet tools over Src.
type FleetMode struct {
	Src  FleetQuerySource
	Opts FleetOptions
}

// DeliveryMode serves the delivery tools for one caller. Writes registers
// propose and cancel; Caller is who the apiserver said the caller is and how
// the identity policy classified them.
type DeliveryMode struct {
	API    DeliveryAPI
	Writes bool
	Caller delivery.Caller
	// Reads, when set, serves the delivery read tools (caller, inbox,
	// environments, autonomy, published and refusals) to readers and agents
	// alike.
	Reads DeliveryReads
	// CallerAt is when Caller was read: once at startup over stdio, per
	// request remotely.
	CallerAt time.Time
	// Installed, when set, asks whether the cluster serves the delivery API,
	// as the dashboard's /api/capabilities does; nil counts as served.
	Installed func(context.Context) (bool, error)
	// InstalledAt, when set, is when Installed last asked the cluster (it
	// caches its answer).
	InstalledAt func() time.Time
	// Refs, when set, lets the record tools read a record by its registry
	// reference. Only the local (stdio) server sets it: it reads with this
	// machine's registry credentials, which a remote caller must never
	// borrow.
	Refs RecordReader
}

// RecordReader reads one delivery record by its registry reference.
type RecordReader interface {
	RecordAt(ctx context.Context, ref string) (*delivery.RecordAt, error)
}

// BundleMode serves one bundle's generated operations and pacto_skill.
type BundleMode struct {
	Bundle *contract.Bundle
	Opts   CapabilityOptions
	Stderr io.Writer
}

// New builds one server: the contract tools a allows, then each active mode
// in m and pacto_capabilities. Pacto's own tools register before the bundle's,
// and the bundle cannot take a pacto_ name, so it never shadows one.
func New(version string, a Authoring, m Modes) (*mcpsdk.Server, error) {
	server := newBareServer(version, instructionsFor(a, m))
	registerTools(server, version, a, editOptions(m))
	if m.Fleet != nil {
		registerFleetTools(server, m.Fleet.Src, m.Fleet.Opts)
	}
	if m.Delivery != nil {
		registerDelivery(server, m.Delivery.API, m.Delivery.Writes, m.Delivery.Refs)
		if m.Delivery.Reads != nil {
			registerDeliveryReads(server, m.Delivery.Reads)
		}
	}
	if m.Bundle != nil {
		if err := RegisterCapabilities(server, m.Bundle.Bundle, m.Bundle.Opts, m.Bundle.Stderr); err != nil {
			return nil, err
		}
	}
	registerCapabilitiesTool(server, a, m)
	return server, nil
}

// instructionsFor joins the texts of the contract tools and of every active
// mode, in registration order.
func instructionsFor(a Authoring, m Modes) string {
	parts := []string{baseFor(a)}
	if m.Fleet != nil {
		parts = append(parts, fleetInstructions)
		if m.Fleet.Opts.Stats != nil {
			parts = append(parts, statsInstructions)
		}
	}
	if m.Delivery != nil {
		parts = append(parts, deliveryInstructions)
		if m.Delivery.Reads != nil {
			parts = append(parts, deliveryReadsInstructions)
		}
	}
	if m.Bundle != nil {
		parts = append(parts, capabilityInstructions(m.Bundle.Bundle, m.Bundle.Opts))
	}
	return strings.Join(append(parts, capabilitiesInstructions), "\n\n")
}

// editOptions is what pacto_edit and pacto_create add on a server with modes
// m: the edit's impact over the fleet snapshot (narrowed to the caller's view,
// as pacto_impact is) when m serves pacto_impact, and the tools m registers.
// pacto_edit registers only over stdio, so the view has no token to read.
func editOptions(m Modes) EditOptions {
	opts := EditOptions{Served: servedBy(m)}
	if f := m.Fleet; f != nil && f.Opts.Impact != nil {
		opts.Impact = func(ctx context.Context, service, oldDir, newDir string) (*impact.Result, error) {
			q, err := viewQuery(ctx, nil, f.Src, f.Opts.View)
			if err != nil {
				return nil, err
			}
			// A local directory names no registry: the edit keys the service
			// the snapshot holds under that name, so its consumers are found
			// where a registry or cluster source put them.
			view, err := q.GetService(service)
			if err != nil {
				return nil, fmt.Errorf("the fleet snapshot cannot place %s: %w", service, err)
			}
			abs, _ := filepath.Abs(oldDir) //nolint:errcheck // fails only without a working directory
			res, err := f.Opts.Impact(ctx, q.SnapshotForSerialization(), ImpactRequest{OldRef: oldDir, NewRef: newDir, Domain: view.Service.Domain,
				NewDirName: filepath.Base(cmp.Or(abs, oldDir))})
			if err != nil || heldVersion(view, res.OldVersion) {
				return res, err
			}
			return unverified(res, "BASELINE_UNPUBLISHED", "the edit is compared with the contract on disk at "+res.OldVersion+
				", a version the fleet holds no revision of, so consumers may run another revision"), nil
		}
	}
	return opts
}

// heldVersion reports whether the fleet holds a revision of view's service at
// version. ponytail: a version match, since a registry digest and a working
// copy's content hash are never comparable; an edited copy that keeps a
// published version still reads as held.
func heldVersion(view *fleet.ServiceView, version string) bool {
	return slices.ContainsFunc(view.Revisions, func(r *fleet.ContractRevision) bool { return r.Version == version })
}

// servedBy reports whether a server with modes m registers one of the tools
// an edit's chain names.
func servedBy(m Modes) func(string) bool {
	return func(tool string) bool {
		switch tool {
		case "pacto_publish_plan", "pacto_publish":
			return m.publish
		case "pacto_delivery_operation_plan":
			return m.Delivery != nil
		case "pacto_impact":
			return m.Fleet != nil && m.Fleet.Opts.Impact != nil
		}
		return false
	}
}
