package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// UpdateResult holds the outcome of a self-update.
type UpdateResult struct {
	PreviousVersion string
	NewVersion      string
}

// Testability hooks.
var (
	osExecutable  = os.Executable
	runtimeGOOS   = runtime.GOOS
	runtimeGOARCH = runtime.GOARCH
	osChmod       = os.Chmod
	osRename      = os.Rename
	osRemove      = os.Remove
)

// Update downloads and installs the specified version of pacto.
// If targetVersion is empty, it fetches the latest release.
func Update(currentVersion, targetVersion string) (*UpdateResult, error) {
	if targetVersion == "" {
		latest, err := fetchLatestVersion()
		if err != nil {
			return nil, fmt.Errorf("failed to determine latest version: %w", err)
		}
		targetVersion = latest
	}

	// Normalize version prefix
	if !strings.HasPrefix(targetVersion, "v") {
		targetVersion = "v" + targetVersion
	}

	// Validate the release exists
	if err := validateRelease(targetVersion); err != nil {
		return nil, err
	}

	// Fetch the published checksums so the downloaded binary can be verified
	// before it replaces the running executable (supply-chain integrity).
	expected, err := expectedChecksum(checksumsURL("TrianaLab/pacto", targetVersion), binaryAssetName())
	if err != nil {
		return nil, err
	}

	// Download, verify, and replace the binary.
	if err := downloadAndReplace(buildDownloadURL(targetVersion), expected); err != nil {
		return nil, err
	}

	// Update cache so notification isn't shown
	WriteCacheAfterUpdate(targetVersion)

	return &UpdateResult{
		PreviousVersion: currentVersion,
		NewVersion:      targetVersion,
	}, nil
}

// downloadAndReplace downloads the binary, verifies it against expectedSHA256,
// and atomically replaces the current executable.
func downloadAndReplace(downloadURL, expectedSHA256 string) error {
	execPath, err := osExecutable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	return downloadAndInstall(downloadURL, execPath, expectedSHA256)
}

// downloadAndInstall downloads a binary, verifies its SHA-256 against
// expectedSHA256, and atomically replaces the file at targetPath. The integrity
// check happens BEFORE the file is made executable or swapped in, so a
// corrupted, truncated, or tampered download is never installed.
func downloadAndInstall(downloadURL, targetPath, expectedSHA256 string) error {
	// Download to temp file in the same directory (ensures same filesystem for atomic rename)
	tmpFile, err := os.CreateTemp(filepath.Dir(targetPath), "pacto-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath) // Clean up temp file on any error
	}()

	if err := downloadBinary(downloadURL, tmpFile); err != nil {
		_ = tmpFile.Close()
		return err
	}
	_ = tmpFile.Close()

	if err := verifyChecksum(tmpPath, expectedSHA256); err != nil {
		return err
	}

	if err := osChmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("failed to set executable permission: %w", err)
	}

	return installFile(tmpPath, targetPath)
}

// installFile renames tmpPath onto targetPath. On Windows a running executable
// cannot be overwritten directly, so it falls back to moving the current file
// aside first and restoring it if the swap fails.
func installFile(tmpPath, targetPath string) error {
	if err := osRename(tmpPath, targetPath); err != nil {
		if runtimeGOOS != "windows" {
			return fmt.Errorf("failed to replace binary: %w", err)
		}
		old := targetPath + ".old"
		_ = osRemove(old)
		if rerr := osRename(targetPath, old); rerr != nil {
			return fmt.Errorf("failed to move current executable aside: %w", rerr)
		}
		if rerr := osRename(tmpPath, targetPath); rerr != nil {
			_ = osRename(old, targetPath) // best-effort restore
			return fmt.Errorf("failed to replace binary: %w", rerr)
		}
		_ = osRemove(old)
	}
	return nil
}

// verifyChecksum computes the SHA-256 of the file at path and compares it
// (case-insensitively) against expectedHex.
func verifyChecksum(path, expectedHex string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read downloaded file: %w", err)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, expectedHex) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHex, got)
	}
	return nil
}

// binaryAssetName returns the release asset filename for the current platform,
// matching the names produced by the release workflow (pacto_<os>_<arch>[.exe]).
func binaryAssetName() string {
	ext := ""
	if runtimeGOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("pacto_%s_%s%s", runtimeGOOS, runtimeGOARCH, ext)
}

// checksumsURL returns the URL of the checksums.txt asset for a release.
func checksumsURL(repo, tag string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/checksums.txt", githubDownloadURL, repo, tag)
}

// expectedChecksum fetches the release checksums and returns the expected
// SHA-256 for assetName, failing closed if it is absent.
func expectedChecksum(url, assetName string) (string, error) {
	sums, err := fetchChecksums(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch release checksums: %w", err)
	}
	expected, ok := sums[assetName]
	if !ok {
		return "", fmt.Errorf("no checksum published for %s", assetName)
	}
	return expected, nil
}

// fetchChecksums downloads and parses a sha256sum-format checksums file into a
// map of asset name -> hex digest.
func fetchChecksums(url string) (map[string]string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checksums download failed with status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseChecksums(string(body)), nil
}

// parseChecksums parses sha256sum output ("<hex>  <name>", name optionally
// prefixed with '*' for binary mode) into a map of name -> hex digest.
func parseChecksums(s string) map[string]string {
	sums := make(map[string]string)
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			name := strings.TrimPrefix(fields[len(fields)-1], "*")
			sums[name] = fields[0]
		}
	}
	return sums
}

// validateRelease checks that a release with the given tag exists on GitHub.
func validateRelease(tag string) error {
	url := fmt.Sprintf("%s/repos/TrianaLab/pacto/releases/tags/%s", githubAPIBaseURL, tag)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to check release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body) // drain body

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("release %s not found", tag)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}
	return nil
}

// buildDownloadURL constructs the download URL for the platform binary.
func buildDownloadURL(tag string) string {
	return fmt.Sprintf(
		"%s/TrianaLab/pacto/releases/download/%s/%s",
		githubDownloadURL, tag, binaryAssetName(),
	)
}

// downloadBinary downloads the binary from the given URL into the writer using
// the long-timeout download client (not the short API client), and verifies the
// number of bytes received against Content-Length to detect truncation.
func downloadBinary(downloadURL string, w io.Writer) error {
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := downloadClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download binary: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write binary: %w", err)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return fmt.Errorf("incomplete download: got %d bytes, expected %d", n, resp.ContentLength)
	}
	return nil
}
