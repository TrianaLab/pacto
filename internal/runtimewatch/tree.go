/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"bytes"
	"context"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
)

// ReasonUnprivileged marks a snapshot from TreeWatcher: polled from /proc
// without a kernel program. The collection is Running, because its listener
// view is complete when Truncated is false; what it cannot see is any failed
// connection, so it never records one and a dependency is never confirmed
// unreachable from it (runtimefacts only reads a fresh failure as negative).
const ReasonUnprivileged = "Unprivileged"

// unprivilegedMessage is the collection message of a TreeWatcher snapshot.
const unprivilegedMessage = "polled from /proc without eBPF: listeners are seen in full, connections only " +
	"while established at a poll, and failed connections never"

// tcpEstablished is TCP_ESTABLISHED as /proc/net/tcp prints it.
const tcpEstablished = "01"

// TreeWatcher is the unprivileged collector of `pacto run`: every poll it
// reads the socket descriptors of the descendants of Root (not Root itself)
// and matches their inodes against the tcp and tcp6 tables of their network
// namespaces. Every listener the tree holds at a poll is seen; a connection is
// seen only while it is established at a poll, counted once per socket and
// fresh for as long as it stays open.
//
// The tree is complete when it stays within maxAttributedPids processes and
// every descriptor directory and table is read in full: only then is Truncated
// clear and the listener view a complete one. It is not safe for concurrent
// use: Run owns it.
type TreeWatcher struct {
	Proc ProcFS
	Root int
	// Identity is the one workload the tree is published as.
	Identity runtimefacts.Identity
	Info     HostInfo
	Sink     Sink
	Now      func() time.Time

	observedSince time.Time
	baselineAt    time.Time
	err           error
	truncated     bool
	listeners     map[ListenerKey]int
	// open holds the inodes of the established sockets of the last poll, so a
	// connection is counted once however many polls see it.
	open  map[uint64]bool
	peers map[peerKey]*peer
}

// Run polls and publishes once, then on every tick until ctx ends.
func (w *TreeWatcher) Run(ctx context.Context, poll, publish <-chan time.Time) {
	w.Poll(w.Now())
	w.publish(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll:
			w.Poll(w.Now())
		case <-publish:
			w.publish(ctx)
		}
	}
}

func (w *TreeWatcher) publish(ctx context.Context) {
	if err := w.Sink.Publish(ctx, w.Snapshot(w.Now())); err != nil {
		logging.LoggerFromContext(ctx).Error("Publish failed", "error", err)
	}
}

// Poll reads the tree's sockets once. A failure to list processes keeps the
// previous view and fails the collection until a poll succeeds.
func (w *TreeWatcher) Poll(now time.Time) {
	entries, err := w.Proc.ReadDir(".")
	if err != nil {
		w.err = err
		return
	}
	pids, truncated := w.tree(entries)
	owned := map[uint64]bool{}
	netns := map[uint32]int{}
	for _, pid := range pids {
		if !socketInodes(w.Proc, pid, func(ino uint64) { owned[ino] = true }) && Running(w.Proc, pid) {
			truncated = true
		}
		ns, err := procNetns(w.Proc, strconv.Itoa(pid))
		if err != nil {
			truncated = truncated || Running(w.Proc, pid)
			continue
		}
		if _, ok := netns[ns]; !ok {
			netns[ns] = pid
		}
	}
	var sockets []tableSocket
	for _, pid := range netns {
		for _, file := range []string{"tcp", "tcp6"} {
			f, err := w.Proc.Open(strconv.Itoa(pid) + "/net/" + file)
			if err != nil {
				truncated = true
				continue
			}
			truncated = !scanTable(f, file == "tcp6", func(s tableSocket) {
				if owned[s.inode] {
					sockets = append(sockets, s)
				}
			}) || truncated
			_ = f.Close()
		}
	}
	w.record(sockets, now)
	if w.observedSince.IsZero() {
		w.observedSince = now
	}
	w.baselineAt, w.truncated, w.err = now, truncated, nil
}

// tree returns the running descendants of Root, at most maxAttributedPids of them;
// truncated is set when there were more.
func (w *TreeWatcher) tree(entries []fs.DirEntry) (pids []int, truncated bool) {
	children := map[int][]int{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if ppid, ok := procStat(w.Proc, pid); ok {
			children[ppid] = append(children[ppid], pid)
		}
	}
	queue := slices.Clone(children[w.Root])
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if len(pids) == maxAttributedPids {
			return pids, true
		}
		pids = append(pids, pid)
		queue = append(queue, children[pid]...)
	}
	return pids, false
}

// pfExiting is the kernel's PF_EXITING task flag: set as a process starts to
// exit, before it closes its descriptors and leaves its namespaces.
const pfExiting = 0x4

// Running reports whether a process exists and has not begun to exit: what an
// exiting process shows of its sockets is its teardown, not its running state,
// so it is not part of the tree, and a descriptor directory or namespace that
// could not be read hides nothing once its process has begun to exit.
func Running(p ProcFS, pid int) bool {
	_, ok := procStat(p, pid)
	return ok
}

// Exiting reports whether a process still exists but has begun to exit or is a
// zombie: from then on its sockets show its teardown.
func Exiting(p ProcFS, pid int) bool {
	_, err := p.ReadFile(strconv.Itoa(pid) + "/stat")
	return err == nil && !Running(p, pid)
}

// procStat reads a process's parent pid from its stat line; ok is false for a
// process that is gone, a zombie or exiting.
func procStat(p ProcFS, pid int) (int, bool) {
	b, err := p.ReadFile(strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	// "pid (comm) state ppid pgrp session tty_nr tpgid flags ...": comm may hold
	// anything, so split after its last parenthesis.
	f := bytes.Fields(b[bytes.LastIndexByte(b, ')')+1:])
	if len(f) < 7 || f[0][0] == 'Z' || f[0][0] == 'X' {
		return 0, false
	}
	ppid, err := strconv.Atoi(string(f[1]))
	flags, ferr := strconv.ParseUint(string(f[6]), 10, 64)
	return ppid, err == nil && ferr == nil && flags&pfExiting == 0
}

// record replaces the listeners and counts every established socket not seen at
// the last poll as one success towards its peer, whose last success is the
// latest poll that saw any of its connections established. An established socket whose
// local port the tree listens on was accepted, not dialled, and is skipped.
func (w *TreeWatcher) record(sockets []tableSocket, now time.Time) {
	w.listeners = map[ListenerKey]int{}
	ports := map[uint16]bool{}
	for _, s := range sockets {
		if s.state == tcpListen {
			w.listeners[ListenerKey{IPv6: s.local.Addr().Is6(), Addr: s.local.Addr(), Port: s.local.Port()}]++
			ports[s.local.Port()] = true
		}
	}
	if w.peers == nil {
		w.peers = map[peerKey]*peer{}
	}
	open := map[uint64]bool{}
	for _, s := range sockets {
		if s.state != tcpEstablished || ports[s.local.Port()] {
			continue
		}
		open[s.inode] = true
		key := peerKey{addr: s.remote.Addr().Unmap(), port: s.remote.Port()}
		p := w.peers[key]
		if p == nil {
			// ponytail: new peers past the bound are not recorded; they can only
			// cost a positive, since this collector never records a negative.
			if len(w.peers) >= DefaultBounds().MaxConnections {
				continue
			}
			p = &peer{}
			w.peers[key] = p
		}
		// An established connection is a success as of this poll; it counts once.
		p.lastSuccess = now
		if !w.open[s.inode] {
			p.successes++
		}
	}
	w.open = open
	cutoff := now.Add(-DefaultBounds().Retention)
	maps.DeleteFunc(w.peers, func(_ peerKey, p *peer) bool { return p.lastSuccess.Before(cutoff) })
}

// Snapshot renders the tree as a snapshot of one workload.
func (w *TreeWatcher) Snapshot(now time.Time) runtimefacts.Snapshot {
	col := runtimefacts.Collection{State: runtimefacts.StateRunning, Reason: ReasonUnprivileged,
		Message: unprivilegedMessage, LastResyncAt: seconds(w.baselineAt)}
	if w.err != nil {
		col.State, col.Reason, col.Message = runtimefacts.StateFailed, ReasonResyncFailed, truncate(w.err.Error(), 1024)
	}
	wl := runtimefacts.Workload{
		Identity:      w.Identity,
		ObservedSince: seconds(w.observedSince),
		BaselineAt:    seconds(w.baselineAt),
		Truncated:     w.truncated,
	}
	for _, k := range slices.SortedFunc(maps.Keys(w.listeners), compareListenerKeys) {
		wl.Listeners = append(wl.Listeners, runtimefacts.Listener{Port: int32(k.Port), Family: family(k.IPv6), Address: k.Addr.String()})
	}
	for _, k := range slices.SortedFunc(maps.Keys(w.peers), comparePeerKeys) {
		p := w.peers[k]
		wl.Connections = append(wl.Connections, runtimefacts.Connection{Address: k.addr.String(), Port: int32(k.port),
			Successes: p.successes, LastSuccessAt: seconds(p.lastSuccess)})
	}
	return runtimefacts.Snapshot{
		NodeName:               w.Info.NodeName,
		AgentVersion:           w.Info.AgentVersion,
		PublishIntervalSeconds: int32(w.Info.PublishInterval / time.Second),
		LastPublished:          seconds(now),
		ObservingSince:         seconds(w.Info.StartedAt),
		Collection:             col,
		Workloads:              []runtimefacts.Workload{wl},
	}
}
