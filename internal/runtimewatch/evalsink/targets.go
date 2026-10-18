/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evalsink

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"

	"gopkg.in/yaml.v3"
)

// Target is one workload on the host and the contract it is held to.
type Target struct {
	// Name identifies the target in output and is the evidence subject: "unit/api.service",
	// "container/web".
	Name string
	// Contract is a local bundle directory or an oci:// reference.
	Contract string
	Match    Selector
	// Start is when the workload started; zero when unknown, which keeps every connection timestamp.
	Start time.Time
	// Host is the address capability and protocol probes dial; empty disables them. It comes from the
	// binding source (the bindings file, the Docker Engine), never from the contract.
	Host string
	// Interfaces maps a contract interface name to the port the workload serves it on.
	Interfaces map[string]int32
	// Dependencies maps a contract dependency name to the host:port the workload reaches it at. The host is
	// resolved at every evaluation.
	Dependencies map[string]string
}

// Selector names the workload of a target by exactly one field.
type Selector struct {
	// Unit is a systemd unit name ("api.service").
	Unit string `yaml:"unit,omitempty"`
	// Container is a container id, in full or at least its first 12 characters.
	Container string `yaml:"container,omitempty"`
	// Cgroup is a cgroup v2 path; the workload is the one that path names ("/system.slice/api.service").
	Cgroup string `yaml:"cgroup,omitempty"`
	// Session is a `pacto run` session cgroup name ("pacto-run-1").
	Session string `yaml:"session,omitempty"`
	// PID is a process id; the workload is the one its cgroup names at each evaluation.
	PID int `yaml:"pid,omitempty"`
}

// String is the selector as kind/value, the default target name.
func (s Selector) String() string {
	switch {
	case s.Unit != "":
		return "unit/" + s.Unit
	case s.Container != "":
		return "container/" + s.Container
	case s.Cgroup != "":
		return "cgroup/" + strings.TrimPrefix(s.Cgroup, "/")
	case s.Session != "":
		return "session/" + s.Session
	default:
		return "pid/" + strconv.Itoa(s.PID)
	}
}

// count is how many selector fields are set.
func (s Selector) count() int {
	n := 0
	for _, set := range []bool{s.Unit != "", s.Container != "", s.Cgroup != "", s.Session != "", s.PID != 0} {
		if set {
			n++
		}
	}
	return n
}

// bindingEntry is one entry of a bindings file.
type bindingEntry struct {
	Match        Selector          `yaml:"match"`
	Contract     string            `yaml:"contract"`
	Host         string            `yaml:"host,omitempty"`
	Interfaces   map[string]int32  `yaml:"interfaces,omitempty"`
	Dependencies map[string]string `yaml:"dependencies,omitempty"`
}

// defaultHost is where probes go when a bindings file entry names no host: the agent shares the host's
// network with the workloads a bindings file describes.
const defaultHost = "127.0.0.1"

// LoadBindings reads a bindings file: a YAML list of {match, contract, host, interfaces, dependencies}.
// Unknown fields are errors. A relative contract directory is relative to the file.
func LoadBindings(path string) ([]Target, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []bindingEntry
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("bindings %s: %w", path, err)
	}
	out := make([]Target, 0, len(entries))
	var errs []error
	for i, e := range entries {
		t, err := e.target(filepath.Dir(path))
		if err != nil {
			errs = append(errs, fmt.Errorf("bindings %s: entry %d: %w", path, i, err))
			continue
		}
		out = append(out, t)
	}
	return out, errors.Join(errs...)
}

// target validates an entry and turns it into a Target.
func (e bindingEntry) target(dir string) (Target, error) {
	if e.Match.count() != 1 {
		return Target{}, errors.New("match must set exactly one of unit, container, cgroup, session or pid")
	}
	contractRef := e.Contract
	if contractRef != "" && !strings.HasPrefix(contractRef, contract.OCIScheme) && !filepath.IsAbs(contractRef) {
		contractRef = filepath.Join(dir, contractRef)
	}
	host := e.Host
	if host == "" {
		host = defaultHost
	}
	t := Target{Name: e.Match.String(), Contract: contractRef, Match: e.Match, Host: host, Interfaces: e.Interfaces,
		Dependencies: e.Dependencies}
	return t, t.Validate()
}

// Validate checks a target names a contract and that every port and dependency address parses.
func (t Target) Validate() error {
	if t.Contract == "" {
		return errors.New("no contract")
	}
	for name, port := range t.Interfaces {
		if port < 1 || port > 65535 {
			return fmt.Errorf("interface %q: port %d is out of range", name, port)
		}
	}
	for name, addr := range t.Dependencies {
		if _, _, err := splitAddress(addr); err != nil {
			return fmt.Errorf("dependency %q: %w", name, err)
		}
	}
	return nil
}

// splitAddress splits host:port and checks the port.
func splitAddress(addr string) (string, int32, error) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil || port == 0 || host == "" {
		return "", 0, fmt.Errorf("%q is not a host:port address", addr)
	}
	return host, int32(port), nil
}
