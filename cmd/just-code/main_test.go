package main

import (
	"slices"
	"testing"
)

func TestResolveCommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
		cmd  string
		rest []string
	}{
		{"bare is code", nil, "code", nil},
		{"leading runtime flag is code", []string{"--tart"}, "code", []string{"--tart"}},
		{"leading docker flag is code", []string{"--docker", "--wait"}, "code", []string{"--docker", "--wait"}},
		{"explicit code", []string{"code", "--microsandbox"}, "code", []string{"--microsandbox"}},
		{"start", []string{"start", "--tart"}, "start", []string{"--tart"}},
		{"stop", []string{"stop"}, "stop", nil},
		{"check", []string{"check"}, "check", nil},
		{"logs", []string{"logs", "--tart"}, "logs", []string{"--tart"}},
		{"help", []string{"help"}, "help", nil},
		{"unknown word is treated as code args", []string{"--podman"}, "code", []string{"--podman"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd, rest := resolveCommand(c.args)
			if cmd != c.cmd {
				t.Errorf("cmd = %q, want %q", cmd, c.cmd)
			}
			if !slices.Equal(rest, c.rest) {
				t.Errorf("rest = %v, want %v", rest, c.rest)
			}
		})
	}
}
