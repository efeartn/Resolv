// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

//go:build !windows

package updater

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func isAppTranslocated(exePath string) bool {
	return strings.Contains(exePath, "/AppTranslocation/")
}

func macAppBundleRoot(exePath string) string {
	const marker = ".app/Contents/MacOS/"
	idx := strings.Index(exePath, marker)
	if idx < 0 {
		return ""
	}
	return exePath[:idx+len(".app")]
}

func checkSelfUpdateTarget() (target string, writable bool, note string) {
	exe, err := os.Executable()
	if err != nil {
		return "", false, "Unable to determine executable path."
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	if runtime.GOOS == "darwin" && isAppTranslocated(exe) {
		return exe, false, "macOS App Translocation is active because Resolv was launched from a quarantined folder. Move Resolv.app to /Applications or a permanent folder to enable one-click updates."
	}

	checkTarget := exe
	if runtime.GOOS == "darwin" {
		if appRoot := macAppBundleRoot(exe); appRoot != "" {
			checkTarget = appRoot
		}
	}

	dir := filepath.Dir(checkTarget)
	if !isDirWritable(dir) {
		return exe, false, fmt.Sprintf("Resolv is installed in a protected folder (%s). Download the update manually from GitHub Releases.", dir)
	}
	return exe, true, ""
}

func hotSwapAndRelaunch(newArtifact, currentExe string) error {
	if runtime.GOOS == "darwin" {
		if appRoot := macAppBundleRoot(currentExe); appRoot != "" && strings.HasSuffix(strings.ToLower(newArtifact), ".zip") {
			return hotSwapMacAppBundle(newArtifact, appRoot)
		}
	}
	return hotSwapBinary(newArtifact, currentExe)
}

func hotSwapMacAppBundle(zipPath, targetApp string) error {
	// Stage next to targetApp so mv stays on the same filesystem for atomic replacement.
	stageDir, err := os.MkdirTemp(filepath.Dir(targetApp), ".resolv-update-stage-*")
	if err != nil {
		return err
	}

	// ditto preserves macOS resource forks, symlinks, signatures, and +x bits.
	out, err := exec.Command("/usr/bin/ditto", "-x", "-k", zipPath, stageDir).CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return fmt.Errorf("failed to extract macOS update bundle: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	stagedApp := filepath.Join(stageDir, "Resolv.app")
	if info, err := os.Stat(stagedApp); err != nil || !info.IsDir() {
		_ = os.RemoveAll(stageDir)
		return fmt.Errorf("extracted archive does not contain Resolv.app")
	}

	_ = exec.Command("/usr/bin/xattr", "-dr", "com.apple.quarantine", stagedApp).Run()

	swapScript := `
pid="$1"
src="$2"
dst="$3"
stage="$4"
bak="${dst}.bak"
i=0
while kill -0 "$pid" 2>/dev/null && [ $i -lt 50 ]; do
  sleep 0.2
  i=$((i + 1))
done
rm -rf "$bak"
if mv "$dst" "$bak"; then
  if mv "$src" "$dst"; then
    rm -rf "$bak" "$stage"
    open -n "$dst"
    exit 0
  else
    mv "$bak" "$dst"
  fi
fi
rm -rf "$stage"
`
	cmd := exec.Command("/bin/sh", "-c", swapScript, "_", strconv.Itoa(os.Getpid()), stagedApp, targetApp, stageDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(stageDir)
		return fmt.Errorf("failed to launch macOS swap helper: %w", err)
	}
	_ = os.Remove(zipPath)
	return nil
}

func hotSwapBinary(newBinary, currentExe string) error {
	// Copy to the target directory first so os.Rename never fails with EXDEV across mount points.
	dstDir := filepath.Dir(currentExe)
	tmpFile, err := os.CreateTemp(dstDir, ".resolv-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	srcFile, err := os.Open(newBinary)
	if err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		_ = srcFile.Close()
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	_ = srcFile.Close()
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, currentExe); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	_ = os.Remove(newBinary)

	// Wait briefly for the current process to exit in a detached session before relaunching.
	relaunchScript := `
pid="$1"
exe="$2"
shift 2
i=0
while kill -0 "$pid" 2>/dev/null && [ $i -lt 50 ]; do
  sleep 0.2
  i=$((i + 1))
done
exec "$exe" "$@"
`
	args := append([]string{"-c", relaunchScript, "_", strconv.Itoa(os.Getpid()), currentExe}, os.Args[1:]...)
	relaunch := exec.Command("/bin/sh", args...)
	relaunch.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := relaunch.Start(); err != nil {
		return fmt.Errorf("updated binary replaced cleanly, but relaunch failed: %w", err)
	}
	return nil
}
