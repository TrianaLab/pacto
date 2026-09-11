package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// CheckResult holds the outcome of a version check.
type CheckResult struct {
	CurrentVersion string
	LatestVersion  string
}

// cache represents the on-disk update check cache.
type cache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// githubRelease is the subset of GitHub's release API response we need.
type githubRelease struct {
	TagName string `json:"tag_name"`
}

const cacheTTL = 24 * time.Hour
const cacheFileName = "update-check.json"

// timeNow is the only remaining package-level seam; the cache TTL is the one
// piece of state that is genuinely process-wide.
var timeNow = time.Now

// Updater carries everything the update path talks to: the two GitHub
// endpoints, the two HTTP clients and the running executable. It is a value the
// caller constructs so a test points its own copy at an httptest server instead
// of mutating package globals through an exported setter -- which made two
// updater tests unable to run concurrently and put the download endpoint of a
// shipped binary one assignment away from any importer.
type Updater struct {
	APIBaseURL      string
	DownloadBaseURL string
	// Client makes GitHub API calls and carries a short timeout.
	Client *http.Client
	// DownloadClient fetches binaries, which can be many MB, so it must not
	// share Client's short timeout.
	DownloadClient *http.Client
	// Executable reports the pacto binary to replace.
	Executable func() (string, error)
}

// New returns an Updater pointed at github.com.
func New() *Updater {
	return &Updater{
		APIBaseURL:      "https://api.github.com",
		DownloadBaseURL: "https://github.com",
		Client:          &http.Client{Timeout: 5 * time.Second},
		DownloadClient:  &http.Client{Timeout: 5 * time.Minute},
		Executable:      os.Executable,
	}
}

// CheckForUpdate checks whether a newer version of pacto is available against
// github.com. The startup notification needs no configuration, so it does not
// make the caller build an Updater for it.
func CheckForUpdate(currentVersion string) *CheckResult {
	return New().CheckForUpdate(currentVersion)
}

// CheckForUpdate checks whether a newer version of pacto is available.
// Returns nil if version is "dev", on any error, or if already up-to-date.
func (u *Updater) CheckForUpdate(currentVersion string) *CheckResult {
	if currentVersion == "dev" {
		return nil
	}

	cur, err := semver.NewVersion(currentVersion)
	if err != nil {
		return nil
	}

	latestStr, err := u.cachedOrFetchLatest()
	if err != nil {
		return nil
	}

	latest, err := semver.NewVersion(latestStr)
	if err != nil {
		return nil
	}

	if !latest.GreaterThan(cur) {
		return nil
	}

	return &CheckResult{
		CurrentVersion: currentVersion,
		LatestVersion:  latestStr,
	}
}

// cachedOrFetchLatest returns the latest version, using cache when fresh.
func (u *Updater) cachedOrFetchLatest() (string, error) {
	c, cachePath := readCache()
	if c != nil && timeNow().Sub(c.CheckedAt) < cacheTTL {
		return c.LatestVersion, nil
	}

	latest, err := u.fetchLatestVersion()
	if err != nil {
		return "", err
	}

	writeCache(cachePath, latest)
	return latest, nil
}

// cachePath returns the path to the cache file.
func cachePath() string {
	dir, err := oci.PactoConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, cacheFileName)
}

// readCache reads the cache file. Returns nil cache if not found or invalid.
func readCache() (*cache, string) {
	p := cachePath()
	if p == "" {
		return nil, ""
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return nil, p
	}

	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, p
	}
	return &c, p
}

// writeCache writes the cache file (best-effort, errors ignored).
func writeCache(path, latestVersion string) {
	if path == "" {
		return
	}
	c := cache{
		CheckedAt:     timeNow(),
		LatestVersion: latestVersion,
	}
	data, _ := json.Marshal(c)
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, data, 0600)
}

// fetchLatestVersion fetches the latest release tag for pacto from GitHub.
func (u *Updater) fetchLatestVersion() (string, error) {
	return u.fetchLatestRepoVersion("TrianaLab/pacto")
}

// fetchLatestRepoVersion fetches the latest release tag for the given repo from GitHub.
func (u *Updater) fetchLatestRepoVersion(repo string) (string, error) {
	url := u.APIBaseURL + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := u.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return "", err
	}

	if release.TagName == "" {
		return "", fmt.Errorf("empty tag_name in GitHub response")
	}

	return release.TagName, nil
}
