// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 PulseDNS contributors

//go:build windows

package sysnet

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// prepareCmd prevents console windows from flashing when the GUI spawns
// PowerShell or ipconfig.
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
