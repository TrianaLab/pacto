/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evalsink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Docker labels a container declares its contract and bindings with.
const (
	LabelContract   = "dev.pacto.contract"
	LabelBindPrefix = "dev.pacto.bind."
	LabelDepPrefix  = "dev.pacto.dep."
)

const (
	// dockerTimeout bounds every Engine API request.
	dockerTimeout = 5 * time.Second
	// maxDockerBody bounds a response body read: a container list of thousands stays well inside it.
	maxDockerBody = 8 << 20
)

// Docker reads labelled containers from the Docker Engine API. It only ever sends GET /containers/json and
// GET /containers/{id}/json.
type Docker struct {
	// Client reaches the Engine; NewDocker dials its unix socket.
	Client *http.Client
	// Base is the Engine's URL; the host is ignored by the unix dialer.
	Base string
	// Labels narrows the list to containers that also carry every one of these
	// labels ("key" or "key=value"), passed to the Engine as label filters. It is
	// how one agent on a shared daemon keeps to one Compose project.
	Labels []string
}

// NewDocker returns a Docker source over the Engine's unix socket.
func NewDocker(socket string) *Docker {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}
	return &Docker{Client: &http.Client{Timeout: dockerTimeout, Transport: &http.Transport{DialContext: dial}}, Base: "http://docker"}
}

// dockerContainer is the part of GET /containers/{id}/json a target needs.
type dockerContainer struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		StartedAt time.Time `json:"StartedAt"`
	} `json:"State"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Targets lists every running container labelled with a contract. A container whose labels do not parse is
// left out and reported in the error next to the targets that did.
func (d *Docker) Targets(ctx context.Context) ([]Target, error) {
	f, _ := json.Marshal(map[string][]string{ //nolint:errcheck // a map of strings always marshals
		"label":  append([]string{LabelContract}, d.Labels...),
		"status": {"running"},
	})
	filters := url.QueryEscape(string(f))
	var list []struct {
		ID string `json:"Id"`
	}
	if err := d.get(ctx, "/containers/json?filters="+filters, &list); err != nil {
		return nil, err
	}
	var out []Target
	var errs []error
	for _, c := range list {
		var info dockerContainer
		if err := d.get(ctx, "/containers/"+url.PathEscape(c.ID)+"/json", &info); err != nil {
			errs = append(errs, err)
			continue
		}
		t, err := containerTarget(info)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, t)
	}
	return out, errors.Join(errs...)
}

// get decodes the JSON answer of one GET.
func (d *Docker) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.Base+path, nil)
	if err != nil {
		return err
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker: GET %s: %s", strings.SplitN(path, "?", 2)[0], resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDockerBody)).Decode(v); err != nil {
		return fmt.Errorf("docker: GET %s: %w", strings.SplitN(path, "?", 2)[0], err)
	}
	return nil
}

// containerTarget turns a labelled container into a target. Probes dial the container's address on its
// first network by name, or the loopback address when it shares the host's network.
func containerTarget(c dockerContainer) (Target, error) {
	name := strings.TrimPrefix(c.Name, "/")
	t := Target{
		Name:         "container/" + name,
		Contract:     c.Config.Labels[LabelContract],
		Match:        Selector{Container: c.ID},
		Start:        c.State.StartedAt,
		Interfaces:   map[string]int32{},
		Dependencies: map[string]string{},
	}
	if c.HostConfig.NetworkMode == "host" {
		t.Host = defaultHost
	} else {
		for _, n := range slices.Sorted(maps.Keys(c.NetworkSettings.Networks)) {
			if ip := c.NetworkSettings.Networks[n].IPAddress; ip != "" {
				t.Host = ip
				break
			}
		}
	}
	for k, v := range c.Config.Labels {
		if iface, ok := strings.CutPrefix(k, LabelBindPrefix); ok {
			port, err := strconv.ParseInt(v, 10, 32)
			if err != nil {
				return Target{}, fmt.Errorf("container %s: label %s: %q is not a port", name, k, v)
			}
			t.Interfaces[iface] = int32(port)
		}
		if dep, ok := strings.CutPrefix(k, LabelDepPrefix); ok {
			t.Dependencies[dep] = v
		}
	}
	if err := t.Validate(); err != nil {
		return Target{}, fmt.Errorf("container %s: %w", name, err)
	}
	return t, nil
}
