package main

import (
	"slices"
	"testing"
)

// The bug this covers: `g9s doctor -offline` ran the network checks anyway.
// Go's flag package stops at the first non-flag argument, so the subcommand
// has to come out of the list before parsing or every flag after it is
// silently dropped — silently, which is the part that makes it a bug rather
// than an inconvenience.
func TestStripSubcommandKeepsFlagsOnEitherSide(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
		got  bool
	}{
		{
			name: "subcommand first, flags after",
			args: []string{"doctor", "-offline"},
			want: []string{"-offline"},
			got:  true,
		},
		{
			name: "flags first, subcommand after",
			args: []string{"-config", "/tmp/c.yaml", "doctor"},
			want: []string{"-config", "/tmp/c.yaml"},
			got:  true,
		},
		{
			name: "subcommand between flags",
			args: []string{"-config", "/tmp/c.yaml", "doctor", "-offline"},
			want: []string{"-config", "/tmp/c.yaml", "-offline"},
			got:  true,
		},
		{
			name: "no subcommand",
			args: []string{"-config", "/tmp/c.yaml"},
			want: []string{"-config", "/tmp/c.yaml"},
			got:  false,
		},
		{
			// A file genuinely named doctor is the value of -config, not a
			// subcommand, and stripping it would silently check the wrong path.
			name: "config value that looks like the subcommand",
			args: []string{"-config", "doctor"},
			want: []string{"-config", "doctor"},
			got:  false,
		},
		{
			name: "config with equals still parses the subcommand",
			args: []string{"-config=/tmp/c.yaml", "doctor"},
			want: []string{"-config=/tmp/c.yaml"},
			got:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, found := stripSubcommand(tt.args, "doctor")
			if found != tt.got {
				t.Errorf("found = %v, want %v", found, tt.got)
			}
			if !slices.Equal(args, tt.want) {
				t.Errorf("args = %v, want %v", args, tt.want)
			}
		})
	}
}
