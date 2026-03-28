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
	"github.com/trianalab/pacto/pkg/oci"
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

// Testability hooks.
var (
	httpClient        = &http.Client{Timeout: 5 * time.Second}
	timeNow           = time.Now
	githubAPIBaseURL  = "https://api.github.com"
	githubDownloadURL = "https://github.com"
)

// CheckForUpdate checks whether a newer version of pacto is available.
// Returns nil if version is "dev", on any error, or if already up-to-date.
func CheckForUpdate(currentVersion string) *CheckResult {
	if currentVersion == "dev" {
		return nil
	}

	cur, err := semver.NewVersion(currentVersion)
	if err != nil {
		return nil
	}

	latestStr, err := cachedOrFetchLatest()
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
func cachedOrFetchLatest() (string, error) {
	c, cachePath := readCache()
	if c != nil && timeNow().Sub(c.CheckedAt) < cacheTTL {
		return c.LatestVersion, nil
	}

	latest, err := fetchLatestVersion()
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

// WriteCacheAfterUpdate updates the cache so a stale notification isn't shown after an update.
func WriteCacheAfterUpdate(latestVersion string) {
	writeCache(cachePath(), latestVersion)
}

// SetTestOverrides overrides package-level settings for external test packages.
// Returns a cleanup function that restores the originals.
func SetTestOverrides(client *http.Client, apiBaseURL, downloadBaseURL string, execFn func() (string, error)) func() {
	origClient, origAPI, origDownload, origExec := httpClient, githubAPIBaseURL, githubDownloadURL, osExecutable
	if client != nil {
		httpClient = client
	}
	if apiBaseURL != "" {
		githubAPIBaseURL = apiBaseURL
	}
	if downloadBaseURL != "" {
		githubDownloadURL = downloadBaseURL
	}
	if execFn != nil {
		osExecutable = execFn
	}
	return func() {
		httpClient, githubAPIBaseURL, githubDownloadURL, osExecutable = origClient, origAPI, origDownload, origExec
	}
}

// fetchLatestVersion fetches the latest release tag for pacto from GitHub.
func fetchLatestVersion() (string, error) {
	return fetchLatestRepoVersion("TrianaLab/pacto")
}

// fetchLatestRepoVersion fetches the latest release tag for the given repo from GitHub.
func fetchLatestRepoVersion(repo string) (string, error) {
	url := githubAPIBaseURL + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
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
