package cli

import (
	"context"
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

// statsCache keeps the last read of the stats repository the Metrics page
// totals, so every viewer and every chart on a page share one read. It holds
// one repository's read, which the reader bounds (stats.MaxRecords records of
// at most runrecord.MaxRecordBytes), so it is bounded by construction.
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
func dashboardStats(cmd *cobra.Command, svc *app.Service) (func(context.Context) (stats.RecordSet, error), string) {
	repo := flagOrEnv(cmd, "stats-registry", "PACTO_STATS_REGISTRY")
	if repo == "" {
		return nil, ""
	}
	c := &statsCache{now: time.Now, read: func(ctx context.Context) (stats.RecordSet, error) {
		return svc.ReadRunRecords(ctx, repo, nil)
	}}
	return c.get, repo
}
