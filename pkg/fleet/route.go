package fleet

import "net/url"

// Canonical product-UI routes. The backend is authoritative about identity AND
// routing: it emits these canonical, reversible path strings so the frontend
// never re-derives a route from a raw key (requirement 2 and requirement 13).
// The strings are plain paths (the dashboard hash-router prepends "#"); emitting
// a string pulls in no dashboard or transport dependency, so the fleet layer
// stays framework-neutral and the architecture boundary test still holds.
const routeRoot = "/fleet"

// RouteForService returns the canonical detail route for a logical service.
func RouteForService(k ServiceKey) string { return routeEntity("services", string(k)) }

// RouteForRevision returns the canonical detail route for a contract revision.
func RouteForRevision(k RevisionKey) string { return routeEntity("revisions", string(k)) }

// RouteForTarget returns the canonical detail route for an operational target.
func RouteForTarget(k TargetKey) string { return routeEntity("targets", string(k)) }

// RouteForOwner returns the canonical detail route for an owner.
func RouteForOwner(ownerKey string) string { return routeEntity("owners", ownerKey) }

// RouteForSource returns the canonical detail route for a source.
func RouteForSource(id string) string { return routeEntity("sources", id) }

// RouteForImpact returns the canonical contextual-impact route for a service.
func RouteForImpact(k ServiceKey) string { return routeEntity("impact", string(k)) }

// RouteForCompare returns the canonical revision-compare route for a service.
func RouteForCompare(k ServiceKey) string { return routeEntity("compare", string(k)) }

// RouteForGraph returns the canonical focused-graph route for an entity.
func RouteForGraph(kind EntityKind, key string) string {
	return routeRoot + "/graph/" + url.PathEscape(string(kind)) + "/" + url.PathEscape(key)
}

// RouteForAttention returns the canonical attention-list route.
func RouteForAttention() string { return routeRoot + "/attention" }

// RouteForServices returns the canonical services-list route.
func RouteForServices() string { return routeRoot + "/services" }

// RouteForOverview returns the canonical overview route.
func RouteForOverview() string { return routeRoot }

// RouteForEntity returns the canonical detail route for any entity kind. An
// unknown kind falls back to the overview route rather than forging a broken
// per-kind path.
func RouteForEntity(kind EntityKind, key string) string {
	switch kind {
	case KindService:
		return RouteForService(ServiceKey(key))
	case KindRevision:
		return RouteForRevision(RevisionKey(key))
	case KindTarget:
		return RouteForTarget(TargetKey(key))
	case KindOwner:
		return RouteForOwner(key)
	case KindSource:
		return RouteForSource(key)
	default:
		return RouteForOverview()
	}
}

// RouteForServicesFilter returns the services list route with a single filter
// descriptor applied, so an overview card can navigate to a pre-filtered list.
func RouteForServicesFilter(param, value string) string {
	if value == "" {
		return RouteForServices()
	}
	return RouteForServices() + "?" + url.QueryEscape(param) + "=" + url.QueryEscape(value)
}

// RouteForAttentionFilter returns the attention route filtered to one category.
func RouteForAttentionFilter(category string) string {
	if category == "" {
		return RouteForAttention()
	}
	return RouteForAttention() + "?category=" + url.QueryEscape(category)
}

func routeEntity(kind, key string) string {
	return routeRoot + "/" + kind + "/" + url.PathEscape(key)
}
