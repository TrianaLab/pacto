package cli

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/singleflight"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// statsTTL is how long the Metrics page serves one read of the stats
// repository; a failed read is kept failureTTL.
const statsTTL = time.Minute

// maxStatsReads bounds how many distinct reads, one per set of services read,
// statsCaches keeps.
const maxStatsReads = 8

// statsCaches keeps one statsCache per set of services read: every record
// for unrestricted viewers and one per restricted view, so a viewer's read
// never holds or depends on another view's records.
type statsCaches struct {
	read func(ctx context.Context, services []string) (stats.RecordSet, error)
	now  func() time.Time

	mu    sync.Mutex
	order []string // keys, least recently used first
	byKey map[string]*statsCache
}

// get is the cached read of services; nil reads every record.
func (c *statsCaches) get(ctx context.Context, services []string) (stats.RecordSet, error) {
	key := "*"
	if services != nil {
		key = "=" + strings.Join(services, "\x00")
	}
	c.mu.Lock()
	e := c.byKey[key]
	if e == nil {
		// ponytail: an LRU of maxStatsReads reads, each bounded by the reader;
		// key by anchor if many distinct views share one dashboard.
		if len(c.order) >= maxStatsReads {
			delete(c.byKey, c.order[0])
			c.order = c.order[1:]
		}
		names := slices.Clone(services)
		e = &statsCache{now: c.now, read: func(ctx context.Context) (stats.RecordSet, error) { return c.read(ctx, names) }}
		c.byKey[key] = e
	} else {
		c.order = slices.DeleteFunc(c.order, func(k string) bool { return k == key })
	}
	c.order = append(c.order, key)
	c.mu.Unlock()
	return e.get(ctx)
}

// statsCache keeps the last read of one set of services, so every viewer of
// that set and every chart on a page share one read. It holds one read,
// which the reader bounds (stats.MaxRecords records of at most
// runrecord.MaxRecordBytes), so it is bounded by construction.
type statsCache struct {
	read func(context.Context) (stats.RecordSet, error)
	now  func() time.Time

	group singleflight.Group
	mu    sync.Mutex
	at    time.Time
	set   stats.RecordSet
	err   error
}

func (c *statsCache) get(ctx context.Context) (stats.RecordSet, error) {
	c.mu.Lock()
	ttl := statsTTL
	if c.err != nil {
		ttl = failureTTL
	}
	if !c.at.IsZero() && c.now().Sub(c.at) < ttl {
		defer c.mu.Unlock()
		return c.set, c.err
	}
	c.mu.Unlock()
	v, err, _ := c.group.Do("", func() (any, error) {
		// Not the caller's cancel: every waiter shares this read, which the
		// reader's own timeout bounds.
		set, err := c.read(context.WithoutCancel(ctx))
		c.mu.Lock()
		c.at, c.set, c.err = c.now(), set, err
		c.mu.Unlock()
		return set, err
	})
	return v.(stats.RecordSet), err
}

// dashboardStats is the Metrics page's reader and the repository it reads:
// --stats-registry, else PACTO_STATS_REGISTRY (no viper key: viper also reads
// ./pacto.yaml, the contract). Empty: the page explains how to turn
// recording on. The read uses the dashboard's registry credentials, and
// anonymous access to a public repository.
func dashboardStats(cmd *cobra.Command, svc *app.Service) (func(context.Context, []string) (stats.RecordSet, error), string) {
	repo := flagOrEnv(cmd, "stats-registry", "PACTO_STATS_REGISTRY")
	if repo == "" {
		return nil, ""
	}
	c := &statsCaches{now: time.Now, byKey: map[string]*statsCache{}, read: func(ctx context.Context, services []string) (stats.RecordSet, error) {
		return svc.ReadRunRecords(ctx, repo, services)
	}}
	return c.get, repo
}
