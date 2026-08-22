package dashboard

import (
	"net/url"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// This file is the SINGLE canonical dashboard route builder. Route emission is a
// transport concern (ADR-2): pkg/fleet stays route-neutral and returns canonical
// identities, and this layer turns those identities into navigable hash-router
// hrefs. Nothing else in the dashboard constructs a "/fleet/..." string, so
// route construction is never duplicated. The strings are plain paths; the
// frontend hash-router prepends "#".

const fleetRouteRoot = "/fleet"

// hrefForEntity returns the canonical detail href for any entity kind, built from
// the exact canonical key. An unknown kind falls back to the overview href rather
// than forging a broken per-kind path.
func hrefForEntity(kind fleet.EntityKind, key string) string {
	switch kind {
	case fleet.KindService:
		return routeEntity("services", key)
	case fleet.KindRevision:
		return routeEntity("revisions", key)
	case fleet.KindTarget:
		return routeEntity("targets", key)
	case fleet.KindOwner:
		return routeEntity("owners", key)
	case fleet.KindSource:
		return routeEntity("sources", key)
	default:
		return fleetRouteRoot
	}
}

// hrefForGraph returns the canonical focused-graph href for an entity.
func hrefForGraph(kind fleet.EntityKind, key string) string {
	return fleetRouteRoot + "/graph/" + url.PathEscape(string(kind)) + "/" + url.PathEscape(key)
}

// hrefForEntryPoint maps a route-neutral overview entry-point descriptor
// (view + optional category) to a canonical href.
func hrefForEntryPoint(view fleet.EntryPointView, category string) string {
	switch view {
	case fleet.EntryPointAttention:
		if category == "" {
			return fleetRouteRoot + "/attention"
		}
		return fleetRouteRoot + "/attention?category=" + url.QueryEscape(category)
	case fleet.EntryPointServices:
		return fleetRouteRoot + "/services"
	default: // overview
		return fleetRouteRoot
	}
}

// routeEntity builds "/fleet/<kind>/<escaped-key>". The key is path-escaped so a
// slash-bearing key (a domain-qualified ServiceKey or a TargetKey) round-trips.
func routeEntity(kind, key string) string {
	return fleetRouteRoot + "/" + kind + "/" + url.PathEscape(key)
}
