/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
)

// The workload a cgroup path belongs to, from the innermost path component
// outwards. Every pattern is anchored at the end, so a nested setup (a kind
// node in Docker, systemd in a container) resolves to the innermost workload;
// patterns are tried in this order, so a pod absorbs its container scopes.
var (
	// A pod in either cgroup driver, with at most one container segment below
	// it: cgroupfs ".../pod<uid>/<cid>" and systemd
	// "...kubepods-burstable-pod<uid>.slice/cri-containerd-<cid>.scope". The
	// systemd driver writes the UID's dashes as underscores; a static pod's
	// cgroup carries its 32-hex config hash instead of a UID. The prefix is not
	// anchored: a reader in its own cgroup namespace sees paths relative to it
	// ("/../../burstable/pod<uid>/<cid>"), without the kubepods component.
	podPattern = regexp.MustCompile(
		`(?:^|[/-])pod([0-9a-f]{8}[-_][0-9a-f]{4}[-_][0-9a-f]{4}[-_][0-9a-f]{4}[-_][0-9a-f]{12}|[0-9a-f]{32})` +
			`(?:\.slice)?(?:/[^/]*)?$`)
	// A container outside Kubernetes: Docker's cgroupfs "/docker/<id>" and the
	// systemd driver's "docker-<id>.scope", containerd's and CRI-O's scopes.
	containerPattern = regexp.MustCompile(`/(?:docker/|docker-|cri-containerd-|crio-)([0-9a-f]{64})(?:\.scope)?$`)
	// The cgroup `pacto run` creates for its child.
	sessionPattern = regexp.MustCompile(`/(pacto-run-[0-9A-Za-z_-]+)(?:\.scope)?$`)
	// A systemd unit with its own cgroup.
	unitPattern = regexp.MustCompile(`/([^/]+\.(?:service|scope))$`)
	// A container enclosing a unit or session, anywhere above it. Nothing after
	// the id is consumed, so nested containers all match.
	outerPattern = regexp.MustCompile(`/(?:docker/|docker-|cri-containerd-|crio-)([0-9a-f]{64})`)
)

// IdentityFromPath names the workload a cgroup v2 path belongs to. Anything no
// pattern matches is a process identified by its path: the host's own daemons,
// a login session, the root cgroup.
func IdentityFromPath(p string) runtimefacts.Identity {
	if m := podPattern.FindStringSubmatch(p); m != nil {
		return runtimefacts.Identity{Kind: runtimefacts.KindPod, ID: strings.ReplaceAll(m[1], "_", "-")}
	}
	if m := containerPattern.FindStringSubmatch(p); m != nil {
		return runtimefacts.Identity{Kind: runtimefacts.KindContainer, ID: m[1]}
	}
	for _, named := range []struct {
		kind string
		re   *regexp.Regexp
	}{{runtimefacts.KindSession, sessionPattern}, {runtimefacts.KindUnit, unitPattern}} {
		m := named.re.FindStringSubmatch(p)
		if m == nil {
			continue
		}
		// The same unit name runs in several containers on one host (every
		// systemd container has a dbus.service): the enclosing container's id
		// keeps them apart.
		id := m[1]
		if outer := outerPattern.FindAllStringSubmatch(p[:len(p)-len(m[0])], -1); outer != nil {
			id = outer[len(outer)-1][1] + "/" + id
		}
		return runtimefacts.Identity{Kind: named.kind, ID: id, Name: m[1]}
	}
	return runtimefacts.Identity{Kind: runtimefacts.KindProcess, ID: p}
}

// IdentityFromProcCgroup names the workload of a /proc/<pid>/cgroup file. The
// unified (cgroup v2) line wins; on a cgroup v1 or hybrid host whose v2 line
// names no workload, the first v1 hierarchy that does.
func IdentityFromProcCgroup(b []byte) runtimefacts.Identity {
	best := runtimefacts.Identity{Kind: runtimefacts.KindProcess}
	for line := range strings.SplitSeq(string(b), "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 {
			continue
		}
		id := IdentityFromPath(fields[2])
		if id.Kind == runtimefacts.KindProcess {
			continue
		}
		if fields[0] == "0" && fields[1] == "" {
			return id
		}
		if best.Kind == runtimefacts.KindProcess {
			best = id
		}
	}
	return best
}

// CgroupResolver maps a cgroup id from a kernel event to its path by walking a
// cgroup v2 hierarchy and matching directory inodes (a cgroup's id is its
// kernfs inode, the same in every cgroup namespace). It is not safe for
// concurrent use: the Runner owns it.
type CgroupResolver struct {
	// FS is the host's cgroup v2 root, e.g. os.DirFS("/sys/fs/cgroup"). A root
	// with no cgroup.controllers (cgroup v1 or a hybrid host's tmpfs) resolves
	// nothing, so every event falls back to its network namespace: walking v1
	// hierarchies would mix inode numbers of several filesystems.
	FS fs.FS
	// MinRefresh is the least time between two walks a cache miss triggers, so a
	// stream of events from short-lived cgroups costs one walk a period.
	MinRefresh time.Duration
	Now        func() time.Time

	paths    map[uint64]string
	walkedAt time.Time
}

// Refresh re-reads the hierarchy, forgetting cgroups that no longer exist.
// Cgroups that vanish mid-walk are skipped.
func (r *CgroupResolver) Refresh() {
	r.walkedAt = r.Now()
	r.paths = map[uint64]string{}
	if _, err := fs.Stat(r.FS, "cgroup.controllers"); err != nil {
		return
	}
	_ = fs.WalkDir(r.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			if ino := inodeOf(info); ino != 0 {
				r.paths[ino] = path.Join("/", p)
			}
		}
		return nil
	})
}

// Path returns the path of the cgroup with the given id, walking again on a
// miss at most once per MinRefresh. 0 is never a cgroup.
func (r *CgroupResolver) Path(id uint64) (string, bool) {
	if id == 0 {
		return "", false
	}
	if p, ok := r.paths[id]; ok {
		return p, true
	}
	if r.paths != nil && r.Now().Sub(r.walkedAt) < r.MinRefresh {
		return "", false
	}
	r.Refresh()
	p, ok := r.paths[id]
	return p, ok
}

// Identify names the workload of a cgroup id; ok is false when the id is
// unknown or its cgroup names no workload, and the caller falls back to the
// network namespace.
func (r *CgroupResolver) Identify(id uint64) (runtimefacts.Identity, bool) {
	p, ok := r.Path(id)
	if !ok {
		return runtimefacts.Identity{}, false
	}
	ident := IdentityFromPath(p)
	return ident, ident.Kind != runtimefacts.KindProcess
}
