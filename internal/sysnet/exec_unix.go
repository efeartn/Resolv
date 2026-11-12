// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 PulseDNS contributors

//go:build !windows

package sysnet

import (
	"os"
	"os/exec"
	"strings"
)

// prepareCmd forces untranslated tool output so it can be parsed reliably.
func prepareCmd(cmd *exec.Cmd) {
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
}

func isElevated() bool { return os.Geteuid() == 0 }

func resolvConf(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nameserver" {
			out = append(out, f[1])
		}
	}
	return out
}
