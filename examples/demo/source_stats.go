package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/trianalab/pacto/v4/examples/story"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// demoStats is the browser demo's stats repository, in memory: the story's
// run records placed before now, plus the weekly coverage trend ending at
// coverage when one is given. It generates once, so the numbers hold still
// for a session, and lists each record as a registry read would: under its
// service's anchor tag, keyed by the digest of its encoding. A read naming
// services lists only their anchors, as a registry read of named anchors does.
func demoStats(now time.Time, coverage *runrecord.Coverage) (func(context.Context, []string) (stats.RecordSet, error), error) {
	records, err := story.RunRecords(now, story.StatsServices())
	if err != nil {
		return nil, err
	}
	if coverage != nil {
		records = append(records, story.CoverageRecords(now, *coverage)...)
	}
	var set stats.RecordSet
	for _, r := range records {
		data, err := runrecord.Encode(r)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		set.Entries = append(set.Entries, stats.Entry{Record: r, Digest: "sha256:" + hex.EncodeToString(sum[:]), Anchor: runrecord.AnchorTag(r.Service)})
	}
	return func(_ context.Context, services []string) (stats.RecordSet, error) {
		if services == nil {
			return set, nil
		}
		anchors := map[string]bool{}
		for _, s := range services {
			anchors[runrecord.AnchorTag(s)] = true
		}
		var named stats.RecordSet
		for _, e := range set.Entries {
			if anchors[e.Anchor] {
				named.Entries = append(named.Entries, e)
			}
		}
		return named, nil
	}, nil
}
