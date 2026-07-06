// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"testing"

	"github.com/pijalu/goa/config"
)

func TestParseOrchestrateInput(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    OrchestrateInput
		wantErr bool
	}{
		{
			name: "bare orchestrate",
			args: nil,
			want: OrchestrateInput{},
		},
		{
			name: "new with all args",
			args: []string{"new", "topology=fanout", "name=custom.id", "objective=Build auth"},
			want: OrchestrateInput{
				Subcommand: "new",
				Topology:   "fanout",
				Name:       "custom.id",
				Objective:  "Build auth",
			},
		},
		{
			name: "new comma separated",
			args: []string{"new", "topology=fanout,name=custom.id,objective=Build auth"},
			want: OrchestrateInput{
				Subcommand: "new",
				Topology:   "fanout",
				Name:       "custom.id",
				Objective:  "Build auth",
			},
		},
		{
			name: "objective with commas",
			args: []string{"new", "objective=Build auth, add tests, deploy"},
			want: OrchestrateInput{
				Subcommand: "new",
				Objective:  "Build auth, add tests, deploy",
			},
		},
		{
			name: "delete with confirm",
			args: []string{"delete", "id=happy.hare", "confirm=true"},
			want: OrchestrateInput{
				Subcommand: "delete",
				ID:         "happy.hare",
				Confirm:    true,
			},
		},
		{
			name: "steer",
			args: []string{"steer", "id=coder-1", "message=fix the bug"},
			want: OrchestrateInput{
				Subcommand: "steer",
				ID:         "coder-1",
				Message:    "fix the bug",
			},
		},
		{
			name: "resume",
			args: []string{"resume", "id=happy.hare"},
			want: OrchestrateInput{
				Subcommand: "resume",
				ID:         "happy.hare",
			},
		},
		{
			name:    "unknown key",
			args:    []string{"new", "foo=bar"},
			wantErr: true,
		},
		{
			name:    "invalid confirm",
			args:    []string{"delete", "id=happy.hare", "confirm=maybe"},
			want:    OrchestrateInput{Subcommand: "delete", ID: "happy.hare"},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOrchestrateInput(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseOrchestrateInput(%v) err = %v, wantErr=%v", tc.args, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got != tc.want {
				t.Errorf("parseOrchestrateInput(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

func TestNormalizeTopology(t *testing.T) {
	cfg := &config.Config{
		Orchestrator: config.OrchestratorConfig{
			Defaults: config.OrchestratorDefaultsConfig{Topology: config.OrchestratorTopologyHub},
		},
	}
	got, err := normalizeTopology("", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hub" {
		t.Errorf("default topology = %q, want hub", got)
	}
	got, err = normalizeTopology("Fanout", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fanout" {
		t.Errorf("fanout topology = %q, want fanout", got)
	}
	_, err = normalizeTopology("star", cfg)
	if err == nil {
		t.Error("expected error for invalid topology")
	}
}
