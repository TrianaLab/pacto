//go:build unix

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/runtimefacts"
)

const (
	cid  = "9b262f08efcd0000000000000000000000000000000000000000000000000b29"
	cid2 = "d5ea5af500000000000000000000000000000000000000000000000000000001"
	hash = "5ff6c2ddf3a1a4e1e8e0d9f2b1c3a4d5" // a static pod's config hash
)

func TestIdentityFromPath(t *testing.T) {
	podID := func(id string) runtimefacts.Identity {
		return runtimefacts.Identity{Kind: runtimefacts.KindPod, ID: id}
	}
	for name, tc := range map[string]struct {
		path string
		want runtimefacts.Identity
	}{
		"systemd driver": {
			"/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod0f1e2d3c_4b5a_6978_8796_a5b4c3d2e1f0.slice/cri-containerd-" + cid + ".scope",
			podID(uidA),
		},
		"kind, nested in Docker": {
			"/docker/" + cid2 + "/kubelet.slice/kubelet-kubepods.slice/kubelet-kubepods-besteffort.slice/" +
				"kubelet-kubepods-besteffort-pod11111111_2222_3333_4444_555555555555.slice/cri-containerd-" + cid + ".scope",
			podID(uidB),
		},
		"cgroupfs driver":      {"/kubepods/burstable/pod" + uidA + "/" + cid, podID(uidA)},
		"cgroupfs guaranteed":  {"/kubepods/pod" + uidA + "/" + cid, podID(uidA)},
		"static pod":           {"/kubepods/burstable/pod" + hash + "/" + cid, podID(hash)},
		"relative, own ns":     {"/../../burstable/pod" + uidA + "/" + cid, podID(uidA)},
		"pod slice, no scope":  {"/kubepods.slice/kubepods-pod0f1e2d3c_4b5a_6978_8796_a5b4c3d2e1f0.slice", podID(uidA)},
		"docker in a pod":      {"/kubepods/pod" + uidA + "/" + cid2 + "/docker/" + cid, runtimefacts.Identity{Kind: runtimefacts.KindContainer, ID: cid}},
		"docker cgroupfs":      {"/docker/" + cid, runtimefacts.Identity{Kind: runtimefacts.KindContainer, ID: cid}},
		"docker systemd":       {"/system.slice/docker-" + cid + ".scope", runtimefacts.Identity{Kind: runtimefacts.KindContainer, ID: cid}},
		"containerd scope":     {"/system.slice/cri-containerd-" + cid + ".scope", runtimefacts.Identity{Kind: runtimefacts.KindContainer, ID: cid}},
		"crio scope":           {"/machine.slice/crio-" + cid + ".scope", runtimefacts.Identity{Kind: runtimefacts.KindContainer, ID: cid}},
		"systemd service":      {"/system.slice/pacto-demo.service", runtimefacts.Identity{Kind: runtimefacts.KindUnit, ID: "pacto-demo.service", Name: "pacto-demo.service"}},
		"systemd scope":        {"/system.slice/pacto-run-demo.scope", runtimefacts.Identity{Kind: runtimefacts.KindSession, ID: "pacto-run-demo", Name: "pacto-run-demo"}},
		"pacto run":            {"/pacto-run-1", runtimefacts.Identity{Kind: runtimefacts.KindSession, ID: "pacto-run-1", Name: "pacto-run-1"}},
		"unit in a container":  {"/docker/" + cid + "/system.slice/pacto-demo.service", runtimefacts.Identity{Kind: runtimefacts.KindUnit, ID: cid + "/pacto-demo.service", Name: "pacto-demo.service"}},
		"innermost outer wins": {"/docker/" + cid2 + "/docker/" + cid + "/init.scope", runtimefacts.Identity{Kind: runtimefacts.KindUnit, ID: cid + "/init.scope", Name: "init.scope"}},
		"root":                 {"/", runtimefacts.Identity{Kind: runtimefacts.KindProcess, ID: "/"}},
		"daemon slice":         {"/restricted", runtimefacts.Identity{Kind: runtimefacts.KindProcess, ID: "/restricted"}},
		"short container id":   {"/docker/abc", runtimefacts.Identity{Kind: runtimefacts.KindProcess, ID: "/docker/abc"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := IdentityFromPath(tc.path); got != tc.want {
				t.Fatalf("IdentityFromPath(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIdentityFromProcCgroup(t *testing.T) {
	v1 := "12:memory:/kubepods/burstable/pod" + uidA + "/" + cid + "\n"
	for name, tc := range map[string]struct {
		in   string
		want runtimefacts.Identity
	}{
		"v2":                    {"0::/system.slice/web.service\n", runtimefacts.Identity{Kind: runtimefacts.KindUnit, ID: "web.service", Name: "web.service"}},
		"v1 only":               {"bogus\n" + v1 + "11:cpu:/kubepods/burstable/pod" + uidB + "/" + cid + "\n", pod(uidA)},
		"hybrid, v2 names none": {v1 + "0::/\n", pod(uidA)},
		"hybrid, v2 wins":       {v1 + "0::/docker/" + cid + "\n", runtimefacts.Identity{Kind: runtimefacts.KindContainer, ID: cid}},
		"no workload":           {"0::/init.scope.d\n", runtimefacts.Identity{Kind: runtimefacts.KindProcess}},
		"named v1 line not unified": {"1:name=systemd:/system.slice/a.service\n0::/docker/" + cid + "\n",
			runtimefacts.Identity{Kind: runtimefacts.KindContainer, ID: cid}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := IdentityFromProcCgroup([]byte(tc.in)); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func dir(ino uint64) *fstest.MapFile {
	return &fstest.MapFile{Mode: os.ModeDir | 0o555, Sys: &syscall.Stat_t{Ino: ino}}
}

func TestCgroupResolver(t *testing.T) {
	now := t0
	cgfs := fstest.MapFS{
		"cgroup.controllers":                    {Data: []byte("cpu memory\n")},
		".":                                     dir(1),
		"system.slice":                          dir(2),
		"system.slice/web.service":              dir(3),
		"system.slice/web.service/cgroup.procs": {Data: []byte("42\n")},
		"nostat":                                {Mode: os.ModeDir | 0o555},
	}
	r := &CgroupResolver{FS: cgfs, MinRefresh: time.Second, Now: func() time.Time { return now }}
	if _, ok := r.Path(0); ok {
		t.Fatal("cgroup id 0 must never resolve")
	}
	if p, ok := r.Path(3); !ok || p != "/system.slice/web.service" {
		t.Fatalf("Path(3) = %q, %v", p, ok)
	}
	if p, ok := r.Path(1); !ok || p != "/" {
		t.Fatalf("Path(1) = %q, %v", p, ok)
	}
	if id, ok := r.Identify(3); !ok || id.Kind != runtimefacts.KindUnit || id.ID != "web.service" {
		t.Fatalf("Identify(3) = %+v, %v", id, ok)
	}
	if _, ok := r.Identify(1); ok {
		t.Fatal("the root cgroup names no workload")
	}

	// A cgroup created after the walk: a miss within MinRefresh does not walk,
	// one after it does.
	cgfs["pacto-run-7"] = dir(9)
	if _, ok := r.Identify(9); ok {
		t.Fatal("walked again within MinRefresh")
	}
	now = now.Add(2 * time.Second)
	if id, ok := r.Identify(9); !ok || id.Kind != runtimefacts.KindSession {
		t.Fatalf("Identify(9) after MinRefresh = %+v, %v", id, ok)
	}
	// Refresh forgets deleted cgroups.
	delete(cgfs, "pacto-run-7")
	r.Refresh()
	if _, ok := r.paths[9]; ok {
		t.Fatal("deleted cgroup kept")
	}

	// Not cgroup v2: nothing resolves.
	v1 := &CgroupResolver{FS: fstest.MapFS{"memory": dir(5)}, Now: time.Now}
	if _, ok := v1.Path(5); ok {
		t.Fatal("a cgroup v1 root must resolve nothing")
	}
}

// TestCgroupResolverOnDisk checks inodeOf against a real directory: os.DirFS
// carries a syscall.Stat_t.
func TestCgroupResolverOnDisk(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "pacto-run-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "gone"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "gone"), 0o755) })
	fi, err := os.Stat(filepath.Join(root, "pacto-run-1"))
	if err != nil {
		t.Fatal(err)
	}
	r := &CgroupResolver{FS: os.DirFS(root), Now: time.Now}
	if p, ok := r.Path(fi.Sys().(*syscall.Stat_t).Ino); !ok || !strings.HasSuffix(p, "/pacto-run-1") {
		t.Fatalf("Path = %q, %v", p, ok)
	}
}
