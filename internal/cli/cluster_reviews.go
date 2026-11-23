package cli

import (
	"context"
	"sync"
	"time"

	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

// homeCluster is the cluster people sign in against: the in-cluster config
// in a pod, else the first named dashboard context, else the ambient one. It
// signs people in and names the one cluster a ServiceAccount or node
// identity is reviewed in; it never reviews another cluster's items.
type homeCluster struct {
	Config *rest.Config
	// UID is the sign-in cluster's id, false while it cannot be read.
	UID func(ctx context.Context) (string, bool)
}

// newHomeCluster is cfg as the sign-in cluster, its id read with the
// process's own credentials, kept once read and asked again at most once
// per failureTTL while it fails.
func newHomeCluster(cfg *rest.Config, d deps) homeCluster {
	var mu sync.Mutex
	var uid string
	var tried time.Time
	return homeCluster{Config: cfg, UID: func(ctx context.Context) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if uid != "" || cfg == nil || time.Since(tried) < failureTTL {
			return uid, uid != ""
		}
		tried = time.Now()
		dyn, err := d.dynamicFor(cfg)
		if err == nil {
			ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
			defer cancel()
			uid, _ = kubecluster.ID(ctx, dyn) // "" on failure: asked again after failureTTL
		}
		return uid, uid != ""
	}}
}

// fleetEntries resolves the contexts fopts reads, the same list its sources
// are built from.
func fleetEntries(svc *app.Service, d deps, fopts app.FleetOptions) func(context.Context) []app.K8sEntry {
	return func(ctx context.Context) []app.K8sEntry {
		return d.resolveK8s(svc, ctx, fopts.IncludeK8s, fopts.K8sContexts)
	}
}

// clusterReviewSet is the reviewer of every cluster the fleet reads, built
// from the same resolved entries as the sources: each identified cluster is
// reviewed with its own context's credentials and keyed by its id. Entries
// are resolved again at most once per failureTTL while one is unidentified.
type clusterReviewSet struct {
	entries func(context.Context) []app.K8sEntry
	home    homeCluster
	review  func(*rest.Config) authn.Review
	now     func() time.Time
	// members adds a reviewer per live member link; nil adds none.
	members *memberHub

	mu      sync.Mutex
	at      time.Time
	settled bool
	reviews map[string]authn.Review
	homeKey string
	homeOK  bool
}

// clusterReviews reviews each cluster of entries in mode with that cluster's
// own credentials, and each live member of members, which no context
// reviews, through its link.
func clusterReviews(entries func(context.Context) []app.K8sEntry, home homeCluster, d deps, mode string, members *memberHub) *clusterReviewSet {
	return &clusterReviewSet{entries: entries, home: home, now: time.Now, members: members,
		review: func(cfg *rest.Config) authn.Review { return clusterReview(cfg, d.dynamicFor, mode) }}
}

// Reviews implements [authn.Reviews].
func (c *clusterReviewSet) Reviews(ctx context.Context) map[string]authn.Review {
	c.resolve(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.members.add(c.reviews)
}

// Home is the reviewer key of the sign-in cluster: its id, or "" when the
// only reviewer is the clusterless entry on the sign-in server.
func (c *clusterReviewSet) Home(ctx context.Context) (string, bool) {
	c.resolve(ctx)
	c.mu.Lock()
	key, ok := c.homeKey, c.homeOK
	c.mu.Unlock()
	if ok {
		return key, true
	}
	return c.home.UID(ctx)
}

// resolve builds the reviewers from the entries, unless they are settled
// or were built less than failureTTL ago.
func (c *clusterReviewSet) resolve(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reviews != nil && (c.settled || c.now().Sub(c.at) < failureTTL) {
		return
	}
	entries := c.entries(ctx)
	c.at, c.settled, c.homeKey, c.homeOK = c.now(), true, "", false
	c.reviews = map[string]authn.Review{}
	conflict := map[string]bool{}
	for _, e := range entries {
		switch {
		case e.Alias != "":
		case e.Conflict != "":
			conflict[e.UID] = true
		case e.Err != nil || e.UID == "":
			c.settled = false
			if e.Err == nil && len(entries) == 1 {
				// The one clusterless source is reviewed with its own
				// credentials, never the sign-in cluster's.
				c.reviews[""] = c.review(e.Config)
				c.homeOK = sameServer(e.Config, c.home.Config)
			}
		default:
			c.reviews[e.UID] = c.review(e.Config)
		}
	}
	for uid := range conflict {
		delete(c.reviews, uid)
	}
	if len(entries) == 0 && c.home.Config != nil {
		// No Kubernetes source (a registry-only fleet): the items without a
		// cluster id are reviewed in the sign-in cluster, as before contexts.
		c.reviews[""], c.homeOK = c.review(c.home.Config), true
	}
}

// sameServer reports whether a and b reach one API server with one CA.
func sameServer(a, b *rest.Config) bool {
	if a == nil || b == nil {
		return false
	}
	ah, aca := k8sclient.ServerOf(a)
	bh, bca := k8sclient.ServerOf(b)
	return ah == bh && aca == bca
}
