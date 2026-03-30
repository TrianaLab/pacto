package dashboard

import "testing"

func TestClassifyVersionPolicy(t *testing.T) {
	tests := []struct {
		name        string
		resolvedRef string
		want        string
	}{
		// Conservative fallback: ambiguous refs return empty string
		{"empty ref", "", ""},
		{"no tag", "ghcr.io/org/service", ""},
		{"latest tag", "ghcr.io/org/service:latest", ""},
		{"non-semver tag", "ghcr.io/org/service:main", ""},
		{"dev branch tag", "ghcr.io/org/service:dev", ""},
		// Unambiguous cases: semver tag and digest
		{"semver tag", "ghcr.io/org/service:1.0.0", VersionPolicyPinnedTag},
		{"semver with v prefix", "ghcr.io/org/service:v2.3.4", VersionPolicyPinnedTag},
		{"semver prerelease", "ghcr.io/org/service:1.0.0-beta.1", VersionPolicyPinnedTag},
		{"digest only", "ghcr.io/org/service@sha256:abc123def456", VersionPolicyPinnedDigest},
		{"tag plus digest", "ghcr.io/org/service:1.0.0@sha256:abc123def456", VersionPolicyPinnedDigest},
		{"digest with long hash", "ghcr.io/org/service@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", VersionPolicyPinnedDigest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyVersionPolicy(tt.resolvedRef)
			if got != tt.want {
				t.Errorf("ClassifyVersionPolicy(%q) = %q, want %q", tt.resolvedRef, got, tt.want)
			}
		})
	}
}

func TestNormalizeResolutionPolicy(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Latest", VersionPolicyTracking},
		{"PinnedTag", VersionPolicyPinnedTag},
		{"PinnedDigest", VersionPolicyPinnedDigest},
		{"", ""},
		{"Unknown", ""},
		{"latest", ""}, // case-sensitive: operator uses PascalCase
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeResolutionPolicy(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeResolutionPolicy(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestComputeLatestAvailable(t *testing.T) {
	tests := []struct {
		name     string
		versions []Version
		want     string
	}{
		{"empty list", nil, ""},
		{"single version", []Version{{Version: "1.0.0"}}, "1.0.0"},
		{"sorted descending", []Version{{Version: "2.0.0"}, {Version: "1.0.0"}}, "2.0.0"},
		{"non-semver skipped", []Version{{Version: "invalid"}, {Version: "1.5.0"}}, "1.5.0"},
		{"all non-semver", []Version{{Version: "latest"}, {Version: "dev"}}, ""},
		{"with v prefix", []Version{{Version: "v3.1.0"}, {Version: "2.0.0"}}, "v3.1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeLatestAvailable(tt.versions)
			if got != tt.want {
				t.Errorf("ComputeLatestAvailable() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsUpdateAvailable(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"empty current", "", "1.0.0", false},
		{"empty latest", "1.0.0", "", false},
		{"both empty", "", "", false},
		{"same version", "1.0.0", "1.0.0", false},
		{"newer available", "1.0.0", "2.0.0", true},
		{"older latest", "2.0.0", "1.0.0", false},
		{"patch update", "1.0.0", "1.0.1", true},
		{"minor update", "1.0.0", "1.1.0", true},
		{"with v prefix current", "v1.0.0", "2.0.0", true},
		{"with v prefix latest", "1.0.0", "v2.0.0", true},
		{"invalid current", "not-semver", "2.0.0", false},
		{"invalid latest", "1.0.0", "not-semver", false},
		{"prerelease newer", "1.0.0", "2.0.0-beta.1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsUpdateAvailable(tt.current, tt.latest)
			if got != tt.want {
				t.Errorf("IsUpdateAvailable(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

func TestMarkCurrentVersion(t *testing.T) {
	t.Run("marks matching version", func(t *testing.T) {
		versions := []Version{
			{Version: "2.0.0"},
			{Version: "1.0.0"},
		}
		MarkCurrentVersion(versions, "1.0.0")
		if versions[0].IsCurrent {
			t.Error("2.0.0 should not be marked current")
		}
		if !versions[1].IsCurrent {
			t.Error("1.0.0 should be marked current")
		}
	})

	t.Run("no match", func(t *testing.T) {
		versions := []Version{
			{Version: "2.0.0"},
			{Version: "1.0.0"},
		}
		MarkCurrentVersion(versions, "3.0.0")
		for _, v := range versions {
			if v.IsCurrent {
				t.Errorf("%s should not be marked current", v.Version)
			}
		}
	})

	t.Run("empty current version", func(t *testing.T) {
		versions := []Version{{Version: "1.0.0"}}
		MarkCurrentVersion(versions, "")
		if versions[0].IsCurrent {
			t.Error("should not mark any version when current is empty")
		}
	})
}

func TestExtractTag(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"ghcr.io/org/svc:1.0.0", "1.0.0"},
		{"ghcr.io/org/svc:latest", "latest"},
		{"ghcr.io/org/svc", ""},
		{"ghcr.io/org/svc@sha256:abc", ""},
		{"ghcr.io/org/svc:1.0.0@sha256:abc", "1.0.0"},
		{"localhost:5000/repo:v1", "v1"},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got := extractTag(tt.ref)
			if got != tt.want {
				t.Errorf("extractTag(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}
