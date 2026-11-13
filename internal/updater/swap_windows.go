// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

//go:build windows

package updater

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

const createNoWindow = 0x08000000

func checkSelfUpdateTarget() (target string, writable bool, note string) {
	exe, err := os.Executable()
	if err != nil {
		return "", false, "Unable to determine executable path."
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	if !isDirWritable(dir) {
		return exe, false, fmt.Sprintf("Resolv is located in a protected directory (%s). Download the update manually from GitHub Releases.", dir)
	}
	return exe, true, ""
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func encodePS(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func hotSwapAndRelaunch(newBinary, currentExe string) error {
	pid := os.Getpid()
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$src = %s
$dst = %s
Wait-Process -Id %d -Timeout 15 -ErrorAction SilentlyContinue
$moved = $false
for ($i = 0; $i -lt 24; $i++) {
  try {
    Move-Item -LiteralPath $src -Destination $dst -Force
    $moved = $true
    break
  } catch {
    Start-Sleep -Milliseconds 250
  }
}
if ($moved) {
  Start-Process -FilePath $dst
}
`, psQuote(newBinary), psQuote(currentExe), pid)

	cmd := exec.Command(
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-WindowStyle", "Hidden",
		"-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodePS(script),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start update helper: %w", err)
	}
	return nil
}
