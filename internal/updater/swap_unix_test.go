// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

//go:build !windows

package updater

import "testing"

func TestMacAppBundleRoot(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/Applications/Resolv.app/Contents/MacOS/Resolv", "/Applications/Resolv.app"},
		{"/Users/alice/Downloads/Resolv.app/Contents/MacOS/resolv", "/Users/alice/Downloads/Resolv.app"},
		{"/usr/local/bin/resolv", ""},
	}
	for _, tc := range cases {
		if got := macAppBundleRoot(tc.in); got != tc.want {
			t.Errorf("macAppBundleRoot(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
