package mcp

import (
	"io"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
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
	registerTools(server, a)
	if m.Fleet != nil {
		registerFleetTools(server, m.Fleet.Src, m.Fleet.Opts)
	}
	if m.Delivery != nil {
		registerDelivery(server, m.Delivery.API, m.Delivery.Writes)
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
