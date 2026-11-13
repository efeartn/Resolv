// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

// Package updater checks GitHub Releases for newer versions of Resolv, verifies
// SHA-256 checksums against SHA256SUMS.txt, and atomically replaces the
// running application binary while keeping user configuration untouched.
package updater

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"resolv/internal/config"
)

const (
	DefaultRepo    = "efeartn/Resolv"
	UserAgent      = "Resolv-Updater"
	defaultAPIBase = "https://api.github.com"
)

// ReleaseInfo holds resolved metadata for the latest GitHub release.
type ReleaseInfo struct {
	CurrentVersion string `json:"currentVersion"`
	Version        string `json:"version"`
	TagName        string `json:"tagName"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	PublishedAt    string `json:"publishedAt"`
	HTMLURL        string `json:"htmlUrl"`
	AssetURL       string `json:"assetUrl,omitempty"`
	AssetName      string `json:"assetName,omitempty"`
	AssetSize      int64  `json:"assetSize,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	IsAvailable    bool   `json:"isAvailable"`
	AssetReady     bool   `json:"assetReady"`
	CanSelfUpdate  bool   `json:"canSelfUpdate"`
	UpdateNote     string `json:"updateNote,omitempty"`
}

// UpdateProgress reports real-time download and installation state.
type UpdateProgress struct {
	Phase           string `json:"phase"` // downloading | verifying | installing
	DownloadedBytes int64  `json:"downloadedBytes"`
	TotalBytes      int64  `json:"totalBytes"`
	Percent         int    `json:"percent"`
	Status          string `json:"status"`
}

// semver holds parsed major.minor.patch and optional prerelease tag.
type semver struct {
	major, minor, patch int
	pre                 string
}

var reDigits = regexp.MustCompile(`^\d+`)

// ParseSemver parses a version string like "v1.1.0" or "1.1.0-beta.1".
func ParseSemver(v string) (int, int, int) {
	s := parseSemverFull(v)
	return s.major, s.minor, s.patch
}

func parseSemverFull(v string) semver {
	clean := strings.TrimLeft(strings.TrimSpace(v), "vV")
	if idx := strings.IndexByte(clean, '+'); idx >= 0 {
		clean = clean[:idx]
	}
	base, pre, _ := strings.Cut(clean, "-")
	parts := strings.Split(base, ".")
	var nums [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		if m := reDigits.FindString(parts[i]); m != "" {
			nums[i], _ = strconv.Atoi(m)
		}
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2], pre: pre}
}

// IsVersionNewer returns true if candidate is strictly newer than current.
func IsVersionNewer(current, candidate string) bool {
	c := parseSemverFull(current)
	k := parseSemverFull(candidate)
	if k.major != c.major {
		return k.major > c.major
	}
	if k.minor != c.minor {
		return k.minor > c.minor
	}
	if k.patch != c.patch {
		return k.patch > c.patch
	}
	// A stable release (empty prerelease) is newer than a prerelease of the same version.
	if c.pre != "" && k.pre == "" {
		return true
	}
	return false
}

// PlatformArtifactName returns the release asset filename built by CI for the current OS.
func PlatformArtifactName() string {
	switch runtime.GOOS {
	case "windows":
		return "resolv-windows-amd64.exe"
	case "darwin":
		return "resolv-macos-universal.zip"
	case "linux":
		return "resolv-linux-amd64"
	default:
		return fmt.Sprintf("resolv-%s-%s", runtime.GOOS, runtime.GOARCH)
	}
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// CheckForUpdates queries the public GitHub Releases API for repo and checks
// if a newer release binary + SHA-256 checksum exists for the current OS.
func CheckForUpdates(ctx context.Context, currentVersion, repo string) (*ReleaseInfo, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	endpoint := fmt.Sprintf("%s/repos/%s/releases/latest", defaultAPIBase, repo)
	return checkForUpdatesFromEndpoint(ctx, endpoint, currentVersion, nil)
}

func checkForUpdatesFromEndpoint(ctx context.Context, endpoint, currentVersion string, client *http.Client) (*ReleaseInfo, error) {
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", UserAgent, currentVersion))
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("no published releases found yet on GitHub")
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("GitHub API rate limit reached (HTTP %d) — try again in a few minutes", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("invalid release payload: %w", err)
	}

	targetAsset := PlatformArtifactName()
	var binAsset, sumsAsset *ghAsset
	for i := range rel.Assets {
		a := &rel.Assets[i]
		switch a.Name {
		case targetAsset:
			binAsset = a
		case "SHA256SUMS.txt":
			sumsAsset = a
		}
	}

	var expectedSHA string
	if sumsAsset != nil && sumsAsset.BrowserDownloadURL != "" {
		if sha, err := fetchChecksumFromURL(ctx, sumsAsset.BrowserDownloadURL, targetAsset, client); err == nil {
			expectedSHA = sha
		}
	}

	candidateVer := strings.TrimLeft(strings.TrimSpace(rel.TagName), "vV")
	isNewer := IsVersionNewer(currentVersion, candidateVer)

	title := strings.TrimSpace(rel.Name)
	if title == "" {
		title = fmt.Sprintf("Resolv %s", rel.TagName)
	}

	_, writable, targetNote := checkSelfUpdateTarget()

	info := &ReleaseInfo{
		CurrentVersion: strings.TrimLeft(strings.TrimSpace(currentVersion), "vV"),
		Version:        candidateVer,
		TagName:        rel.TagName,
		Title:          title,
		Body:           rel.Body,
		PublishedAt:    rel.PublishedAt.Format(time.RFC3339),
		HTMLURL:        rel.HTMLURL,
		SHA256:         expectedSHA,
		IsAvailable:    isNewer,
		CanSelfUpdate:  writable,
		UpdateNote:     targetNote,
	}

	if binAsset != nil {
		info.AssetURL = binAsset.BrowserDownloadURL
		info.AssetName = binAsset.Name
		info.AssetSize = binAsset.Size
		info.AssetReady = info.AssetURL != "" && info.SHA256 != ""
	}
	if isNewer && !info.AssetReady {
		if binAsset == nil {
			info.UpdateNote = fmt.Sprintf("Release %s was published, but %s is still building on GitHub Actions. Check back in 2–3 minutes or open GitHub Release.", rel.TagName, targetAsset)
		} else if info.SHA256 == "" {
			info.UpdateNote = "SHA256SUMS.txt verification manifest is missing or still uploading for this release."
		}
	}

	return info, nil
}

func fetchChecksumFromURL(ctx context.Context, sumsURL, targetName string, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d fetching checksums", resp.StatusCode)
	}

	sc := bufio.NewScanner(io.LimitReader(resp.Body, 512*1024))
	for sc.Scan() {
		fields := strings.Fields(strings.TrimSpace(sc.Text()))
		if len(fields) >= 2 {
			fileCol := strings.TrimPrefix(fields[len(fields)-1], "*")
			if fileCol == targetName && len(fields[0]) == 64 {
				return strings.ToLower(fields[0]), nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("checksum for %s not found in SHA256SUMS.txt", targetName)
}

// VerifyAndDownload downloads info.AssetURL into destDir, verifies the SHA-256
// digest strictly against info.SHA256, and returns the verified local file path.
func VerifyAndDownload(ctx context.Context, info *ReleaseInfo, destDir string, client *http.Client, progressCb func(UpdateProgress)) (string, error) {
	if info == nil || info.AssetURL == "" {
		return "", errors.New("no downloadable binary asset found for your platform")
	}
	if info.SHA256 == "" {
		return "", errors.New("refusing unverified update: SHA-256 checksum is missing from release")
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}

	assetName := info.AssetName
	if assetName == "" {
		assetName = PlatformArtifactName()
	}
	targetFile := filepath.Join(destDir, assetName)
	tmpFile := targetFile + ".downloading"
	_ = os.Remove(tmpFile)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.AssetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	totalBytes := resp.ContentLength
	if totalBytes <= 0 {
		totalBytes = info.AssetSize
	}

	f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}

	hasher := sha256.New()
	writer := io.MultiWriter(f, hasher)
	buf := make([]byte, 64*1024)
	var downloaded int64
	lastEmit := time.Now().Add(-time.Second)

	for {
		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := writer.Write(buf[:n]); wErr != nil {
				_ = f.Close()
				_ = os.Remove(tmpFile)
				return "", wErr
			}
			downloaded += int64(n)
			if progressCb != nil && (time.Since(lastEmit) >= 100*time.Millisecond || downloaded == totalBytes) {
				pct := 0
				if totalBytes > 0 {
					pct = min(98, int((downloaded*95)/totalBytes))
				}
				progressCb(UpdateProgress{
					Phase:           "downloading",
					DownloadedBytes: downloaded,
					TotalBytes:      totalBytes,
					Percent:         pct,
					Status:          fmt.Sprintf("Downloading %s...", assetName),
				})
				lastEmit = time.Now()
			}
		}
		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			_ = f.Close()
			_ = os.Remove(tmpFile)
			return "", rErr
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpFile)
		return "", err
	}

	if progressCb != nil {
		progressCb(UpdateProgress{
			Phase:           "verifying",
			DownloadedBytes: downloaded,
			TotalBytes:      totalBytes,
			Percent:         98,
			Status:          "Verifying SHA-256 integrity...",
		})
	}

	computed := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(computed, info.SHA256) {
		_ = os.Remove(tmpFile)
		return "", fmt.Errorf("SHA-256 verification failed: expected %s, got %s", info.SHA256, computed)
	}

	_ = os.Remove(targetFile)
	if err := os.Rename(tmpFile, targetFile); err != nil {
		_ = os.Remove(tmpFile)
		return "", err
	}
	return targetFile, nil
}

// DownloadAndApplyUpdate downloads and verifies the target release binary,
// validates replacement permissions, and launches the OS swap + restart helper.
func DownloadAndApplyUpdate(ctx context.Context, info *ReleaseInfo, progressCb func(UpdateProgress)) error {
	targetPath, writable, note := checkSelfUpdateTarget()
	if !writable {
		if note != "" {
			return errors.New(note)
		}
		return fmt.Errorf("cannot overwrite %s without elevated permissions; download the update manually from GitHub", targetPath)
	}

	cfgDir, err := config.Dir()
	if err != nil {
		return err
	}
	updatesDir := filepath.Join(cfgDir, "updates")

	verifiedPath, err := VerifyAndDownload(ctx, info, updatesDir, nil, progressCb)
	if err != nil {
		return err
	}

	if progressCb != nil {
		progressCb(UpdateProgress{
			Phase:   "installing",
			Percent: 100,
			Status:  "Integrity verified. Installing & restarting Resolv...",
		})
	}

	return hotSwapAndRelaunch(verifiedPath, targetPath)
}

func isDirWritable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".resolv-write-check-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return true
}
