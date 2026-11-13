// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseSemverAndIsVersionNewer(t *testing.T) {
	maj, min, pat := ParseSemver("v1.2.3-rc.1+build7")
	if maj != 1 || min != 2 || pat != 3 {
		t.Fatalf("ParseSemver unexpected: %d.%d.%d", maj, min, pat)
	}

	cases := []struct {
		current   string
		candidate string
		want      bool
	}{
		{"1.0.1", "v1.1.0", true},
		{"v1.1.0", "v1.1.0", false},
		{"1.1.0", "1.0.9", false},
		{"2.0.0", "1.9.9", false},
		{"1.1.0-beta.1", "v1.1.0", true},
		{"1.1.0", "1.1.0-rc.1", false},
		{"1.0.1", "1.0.2", true},
	}

	for _, tc := range cases {
		got := IsVersionNewer(tc.current, tc.candidate)
		if got != tc.want {
			t.Errorf("IsVersionNewer(%q, %q) = %v, want %v", tc.current, tc.candidate, got, tc.want)
		}
	}
}

func TestPlatformArtifactName(t *testing.T) {
	got := PlatformArtifactName()
	switch runtime.GOOS {
	case "windows":
		if got != "resolv-windows-amd64.exe" {
			t.Fatalf("got %q on windows", got)
		}
	case "darwin":
		if got != "resolv-macos-universal.zip" {
			t.Fatalf("got %q on darwin", got)
		}
	case "linux":
		if got != "resolv-linux-amd64" {
			t.Fatalf("got %q on linux", got)
		}
	}
}

func TestCheckForUpdatesFromEndpointAndVerify(t *testing.T) {
	payload := []byte("resolv-binary-payload-v1.1.0")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	assetName := PlatformArtifactName()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			rel := ghRelease{
				TagName: "v1.1.0",
				Name:    "Resolv v1.1.0",
				Body:    "- Auto-updater\n- Live streaming",
				HTMLURL: "https://github.com/efeartn/Resolv/releases/tag/v1.1.0",
				Assets: []ghAsset{
					{Name: assetName, BrowserDownloadURL: srv.URL + "/bin", Size: int64(len(payload))},
					{Name: "SHA256SUMS.txt", BrowserDownloadURL: srv.URL + "/sums", Size: 128},
				},
			}
			_ = json.NewEncoder(w).Encode(rel)
		case "/latest-building":
			rel := ghRelease{
				TagName: "v1.1.0",
				Assets:  []ghAsset{},
			}
			_ = json.NewEncoder(w).Encode(rel)
		case "/ratelimit":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/sums":
			fmt.Fprintf(w, "%s  *%s\n", hexSum, assetName)
		case "/bin":
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			_, _ = w.Write(payload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	info, err := checkForUpdatesFromEndpoint(ctx, srv.URL+"/latest", "1.0.1", srv.Client())
	if err != nil {
		t.Fatalf("checkForUpdatesFromEndpoint error: %v", err)
	}
	if !info.IsAvailable || !info.AssetReady {
		t.Fatalf("expected available & ready update, got %+v", info)
	}
	if !strings.EqualFold(info.SHA256, hexSum) {
		t.Fatalf("checksum mismatch: got %q, want %q", info.SHA256, hexSum)
	}

	dest := t.TempDir()
	var phases []string
	savedPath, err := VerifyAndDownload(ctx, info, dest, srv.Client(), func(p UpdateProgress) {
		phases = append(phases, p.Phase)
	})
	if err != nil {
		t.Fatalf("VerifyAndDownload error: %v", err)
	}
	gotBytes, err := os.ReadFile(savedPath)
	if err != nil || string(gotBytes) != string(payload) {
		t.Fatalf("downloaded file content mismatch: err=%v len=%d", err, len(gotBytes))
	}
	if len(phases) == 0 {
		t.Fatalf("expected progress callbacks during download/verify")
	}

	// Verify strict SHA-256 mismatch rejection and cleanup of .downloading file.
	badInfo := *info
	badInfo.SHA256 = strings.Repeat("0", 64)
	if _, err := VerifyAndDownload(ctx, &badInfo, dest, srv.Client(), nil); err == nil {
		t.Fatalf("expected SHA-256 mismatch error")
	}
	if _, err := os.Stat(filepath.Join(dest, assetName+".downloading")); !os.IsNotExist(err) {
		t.Fatalf("expected temporary .downloading file to be cleaned up on checksum failure")
	}

	// Verify release-building note when asset is missing.
	buildingInfo, err := checkForUpdatesFromEndpoint(ctx, srv.URL+"/latest-building", "1.0.1", srv.Client())
	if err != nil {
		t.Fatalf("latest-building error: %v", err)
	}
	if !buildingInfo.IsAvailable || buildingInfo.AssetReady || buildingInfo.UpdateNote == "" {
		t.Fatalf("expected IsAvailable=true, AssetReady=false, UpdateNote!=empty; got %+v", buildingInfo)
	}

	// Verify rate-limit error handling.
	if _, err := checkForUpdatesFromEndpoint(ctx, srv.URL+"/ratelimit", "1.0.1", srv.Client()); err == nil {
		t.Fatalf("expected rate-limit error on HTTP 429")
	}
}
