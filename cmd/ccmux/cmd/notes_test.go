package cmd

import (
	"testing"

	"github.com/skzv/ccmux/internal/config"
)

// TestResolveNotesAddr_DefaultsLocal — no --host targets the local
// device (addr empty, local true).
func TestResolveNotesAddr_DefaultsLocal(t *testing.T) {
	addr, local, err := resolveNotesAddr(config.Config{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !local || addr != "" {
		t.Errorf("addr=%q local=%v, want \"\",true", addr, local)
	}
}

// TestResolveNotesAddr_KnownHost — a configured host resolves to
// "<address>:<port>", honoring the per-host port, then
// config.Daemon.TailnetPort, then the 7474 default.
func TestResolveNotesAddr_KnownHost(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{
			name: "explicit host port",
			cfg: config.Config{Hosts: []config.Host{
				{Name: "laptop", Address: "100.64.0.2", Port: 9000},
			}},
			want: "100.64.0.2:9000",
		},
		{
			name: "falls back to TailnetPort",
			cfg: config.Config{
				Hosts:  []config.Host{{Name: "laptop", Address: "100.64.0.2"}},
				Daemon: config.DaemonConfig{TailnetPort: 8080},
			},
			want: "100.64.0.2:8080",
		},
		{
			name: "falls back to default 7474",
			cfg: config.Config{Hosts: []config.Host{
				{Name: "laptop", Address: "100.64.0.2"},
			}},
			want: "100.64.0.2:7474",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, local, err := resolveNotesAddr(tc.cfg, "laptop")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if local {
				t.Error("a named host must not resolve as local")
			}
			if addr != tc.want {
				t.Errorf("addr = %q, want %q", addr, tc.want)
			}
		})
	}
}

// TestResolveNotesAddr_UnknownHost — a typo'd host name is an error, not
// a silent fall-through to the local device.
func TestResolveNotesAddr_UnknownHost(t *testing.T) {
	cfg := config.Config{Hosts: []config.Host{{Name: "laptop", Address: "x"}}}
	if _, _, err := resolveNotesAddr(cfg, "desktop"); err == nil {
		t.Fatal("expected an error for an unknown host name")
	}
}
