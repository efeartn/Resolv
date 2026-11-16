// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

//go:build windows

package sysnet

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWinStringSliceUnmarshalJSON(t *testing.T) {
	payload := `[
		{"IPv4":"192.168.0.30","IPv6":{},"DNS":null},
		{"IPv4":["10.0.0.2","10.0.0.3"],"IPv6":"","DNS":["9.9.9.9","149.112.112.112"]}
	]`
	var rows []struct {
		IPv4 winStringSlice `json:"IPv4"`
		IPv6 winStringSlice `json:"IPv6"`
		DNS  winStringSlice `json:"DNS"`
	}
	if err := json.Unmarshal([]byte(payload), &rows); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if !reflect.DeepEqual([]string(rows[0].IPv4), []string{"192.168.0.30"}) {
		t.Fatalf("scalar IPv4 mismatch: %+v", rows[0].IPv4)
	}
	if len(rows[0].IPv6) != 0 || len(rows[0].DNS) != 0 {
		t.Fatalf("expected empty IPv6/DNS for {} / null, got ipv6=%v dns=%v", rows[0].IPv6, rows[0].DNS)
	}
	if !reflect.DeepEqual([]string(rows[1].IPv4), []string{"10.0.0.2", "10.0.0.3"}) {
		t.Fatalf("array IPv4 mismatch: %+v", rows[1].IPv4)
	}
	if len(rows[1].IPv6) != 0 {
		t.Fatalf("expected empty slice for empty string, got %+v", rows[1].IPv6)
	}
	if !reflect.DeepEqual([]string(rows[1].DNS), []string{"9.9.9.9", "149.112.112.112"}) {
		t.Fatalf("array DNS mismatch: %+v", rows[1].DNS)
	}
}
