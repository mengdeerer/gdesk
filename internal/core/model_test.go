package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIdentityPreventsPIDReuse(t *testing.T) {
	now := time.Now()
	hosts := Demo(now)
	original := hosts[0].Snapshot.Records[0].Status.Processes[0]
	for _, test := range []struct {
		name   string
		change func(*Identity)
	}{{"pid reused", func(i *Identity) { i.StartTicks = "999999" }}, {"host reboot", func(i *Identity) { i.BootID = "another-boot" }}} {
		t.Run(test.name, func(t *testing.T) {
			hosts[0].Snapshot.Records[0].Status.Processes[0] = original
			test.change(&hosts[0].Snapshot.Records[0].Status.Processes[0])
			rows := Rows(hosts, nil, now)
			var unregistered bool
			for _, r := range rows {
				if r.Task.ID == "01-resnet" && len(r.GPUs) > 0 {
					t.Fatal("stale identity attached to current GPU")
				}
				if r.Inferred && r.Processes[0].PID == original.PID {
					unregistered = true
				}
			}
			if !unregistered {
				t.Fatal("current process disappeared")
			}
		})
	}
}
func TestMultiGPUAndConflict(t *testing.T) {
	now := time.Now()
	hosts := Demo(now)
	rows := Rows(hosts, nil, now)
	for _, r := range rows {
		if r.Task.ID == "01-resnet" && len(r.GPUs) != 2 {
			t.Fatal("multi-GPU task did not merge")
		}
	}
	duplicate := hosts[0].Snapshot.Records[0]
	duplicate.Task.ID = "duplicate"
	hosts[0].Snapshot.Records = append(hosts[0].Snapshot.Records, duplicate)
	rows = Rows(hosts, nil, now)
	for _, r := range rows {
		if (r.Task.ID == "01-resnet" || r.Task.ID == "duplicate") && (!strings.Contains(r.Warning, "冲突") || len(r.GPUs) > 0) {
			t.Fatal("ambiguous claims must stay unbound")
		}
	}
}
func TestSharedGPUAndDisconnected(t *testing.T) {
	now := time.Now()
	hosts := Demo(now)
	p := hosts[0].Snapshot.Processes[0]
	p.PID = 999
	p.StartTicks = "999"
	hosts[0].Snapshot.Processes = append(hosts[0].Snapshot.Processes, p)
	hosts[0].Error = "offline"
	rows := Rows(hosts, nil, now)
	shared := 0
	for _, r := range rows {
		if r.HostID == hosts[0].Config.ID {
			if !r.Stale {
				t.Fatal("offline rows look live")
			}
			for _, g := range r.GPUs {
				if g.Index == 0 {
					shared++
				}
			}
		}
	}
	if shared != 2 {
		t.Fatalf("expected two tasks sharing GPU, got %d", shared)
	}
}
func TestUnknownProgressAndSanitization(t *testing.T) {
	if !strings.Contains(ProgressText(&Progress{Completed: 8, Unit: "files"}), "总数未知") {
		t.Fatal("unknown total fabricated")
	}
	if strings.ContainsAny(Clean("a\x1b[2J\n\u202Eb"), "\x1b\n\u202e") {
		t.Fatal("terminal control sequence not sanitized")
	}
}
func TestConfigValidation(t *testing.T) {
	for _, body := range []string{`null`, `{"hosts":[{"id":"x","ssh":"-oProxyCommand=bad"}]}`, `{"hosts":[{"id":"x","ssh":"ok"},{"id":"x","ssh":"ok2"}]}`, `{"interval_seconds":0,"hosts":[{"id":"x","ssh":"ok"}]}`} {
		p := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(p); err == nil {
			t.Fatalf("accepted invalid config: %s", body)
		}
	}
}
