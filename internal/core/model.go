package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type Config struct {
	Interval int          `json:"interval_seconds"`
	Timeout  int          `json:"timeout_seconds"`
	Hosts    []HostConfig `json:"hosts"`
}
type HostConfig struct {
	ID       string   `json:"id"`
	SSH      string   `json:"ssh"`
	User     string   `json:"user,omitempty"`
	TaskDirs []string `json:"task_dirs"`
}
type Task struct {
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"task_id"`
	Name          string         `json:"name"`
	Project       string         `json:"project"`
	Description   string         `json:"description"`
	Kind          string         `json:"kind,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	Parameters    map[string]any `json:"parameters,omitempty"`
	Artifacts     []Artifact     `json:"artifacts,omitempty"`
	Provenance    map[string]any `json:"provenance,omitempty"`
	Display       Display        `json:"display,omitempty"`
}
type Display struct {
	SummaryFields []string `json:"summary_fields"`
}
type Artifact struct {
	Role string `json:"role"`
	Path string `json:"path"`
}
type Identity struct {
	PID        int    `json:"pid"`
	BootID     string `json:"boot_id"`
	StartTicks string `json:"start_ticks"`
}

func (p Identity) Key() string { return fmt.Sprintf("%s/%d/%s", p.BootID, p.PID, p.StartTicks) }
func (p Identity) Valid() bool { return p.PID > 0 && p.BootID != "" && p.StartTicks != "" }

type Process struct {
	Identity
	ElapsedSeconds *float64            `json:"elapsed_seconds,omitempty"`
	User           string              `json:"user"`
	Command        string              `json:"command"`
	Cwd            string              `json:"cwd"`
	GPUs           []string            `json:"gpu_uuids"`
	Memory         map[string]*float64 `json:"memory_mib"`
	Error          string              `json:"error,omitempty"`
}
type Progress struct {
	Completed float64   `json:"completed"`
	Total     *float64  `json:"total,omitempty"`
	Unit      string    `json:"unit"`
	Scope     string    `json:"scope"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Status struct {
	SchemaVersion int            `json:"schema_version"`
	TaskID        string         `json:"task_id"`
	AttemptID     string         `json:"attempt_id"`
	State         string         `json:"state"`
	Stage         string         `json:"stage"`
	Message       string         `json:"message"`
	Processes     []Identity     `json:"processes"`
	Progress      *Progress      `json:"progress,omitempty"`
	Metrics       map[string]any `json:"metrics,omitempty"`
	Artifacts     []Artifact     `json:"artifacts,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	FinishedAt    time.Time      `json:"finished_at"`
	ExitCode      *int           `json:"exit_code,omitempty"`
}
type Record struct {
	Task    Task   `json:"task"`
	Status  Status `json:"status"`
	Source  string `json:"source"`
	Warning string `json:"warning,omitempty"`
}
type GPU struct {
	UUID        string   `json:"uuid"`
	Index       int      `json:"index"`
	Name        string   `json:"name"`
	Util        *float64 `json:"util"`
	Used        *float64 `json:"used_mib"`
	Total       *float64 `json:"total_mib"`
	Temperature *float64 `json:"temperature"`
}
type Snapshot struct {
	HostID      string    `json:"host_id"`
	User        string    `json:"user"`
	ObservedAt  time.Time `json:"observed_at"`
	GPUs        []GPU     `json:"gpus"`
	Processes   []Process `json:"processes"`
	Records     []Record  `json:"records"`
	Errors      []string  `json:"errors"`
	GPUOK       bool      `json:"gpu_ok"`
	ProcessesOK bool      `json:"processes_ok"`
}
type Host struct {
	Config     HostConfig
	Snapshot   Snapshot
	ReceivedAt time.Time
	Error      string
	Loading    bool
	Failures   int
	NextPoll   time.Time
	PollToken  uint64
}
type Row struct {
	Key       string
	HostID    string
	Task      Task
	Status    Status
	Processes []Process
	GPUs      []GPU
	Source    string
	Warning   string
	Stale     bool
	Inferred  bool
	Owner     string
}
type Annotation struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func LoadConfig(path string) (Config, error) {
	c := Config{Interval: 5, Timeout: 12}
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if !strings.HasPrefix(strings.TrimSpace(string(b)), "{") {
		return c, fmt.Errorf("配置必须为 JSON 对象")
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	if c.Interval < 2 || c.Timeout < 1 || c.Timeout > 120 {
		return c, fmt.Errorf("interval_seconds 必须 >= 2，timeout_seconds 必须为 1–120")
	}
	if len(c.Hosts) > 32 {
		return c, fmt.Errorf("最多配置 32 台服务器")
	}
	seen := map[string]bool{}
	for _, h := range c.Hosts {
		if h.ID == "" || h.SSH == "" || strings.HasPrefix(h.SSH, "-") || strings.ContainsAny(h.SSH, " \t\r\n") || seen[h.ID] {
			return c, fmt.Errorf("服务器 id/ssh 无效或重复: %q", h.ID)
		}
		seen[h.ID] = true
		if len(h.TaskDirs) > 128 {
			return c, fmt.Errorf("每台服务器最多配置 128 条任务来源")
		}
	}
	return c, nil
}

// Clean 阻止远端元信息中的控制字符改变终端状态；保留可打印 Unicode。
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
}
func Value(v any) string {
	if m, ok := v.(map[string]any); ok {
		if x, ok := m["value"]; ok {
			return fmt.Sprint(x) + " " + fmt.Sprint(m["unit"])
		}
	}
	return Clean(fmt.Sprint(v))
}
func Summary(t Task) string {
	fields := t.Display.SummaryFields
	if len(fields) == 0 {
		for k := range t.Parameters {
			fields = append(fields, "parameters."+k)
		}
		sort.Strings(fields)
	}
	parts := []string{Clean(t.Project)}
	for _, f := range fields {
		if len(parts) >= 5 {
			break
		}
		if !strings.HasPrefix(f, "parameters.") {
			continue
		}
		key := strings.TrimPrefix(f, "parameters.")
		if v, ok := t.Parameters[key]; ok {
			parts = append(parts, Clean(key)+"="+Value(v))
		}
	}
	return strings.Join(parts, " · ")
}
func Rows(hosts []Host, annotations map[string]Annotation, now time.Time) []Row {
	var out []Row
	for _, h := range hosts {
		stale := h.Error != "" || !h.Snapshot.GPUOK || !h.Snapshot.ProcessesOK || h.ReceivedAt.IsZero() || now.Sub(h.ReceivedAt) > 20*time.Second
		claimed := map[string]bool{}
		// 一个进程被多份记录声明时不武断关联，保留为未登记进程。
		claims := map[string]int{}
		for _, r := range h.Snapshot.Records {
			for _, id := range r.Status.Processes {
				if id.Valid() {
					claims[id.Key()]++
				}
			}
		}
		for _, record := range h.Snapshot.Records {
			r := Row{Key: h.Config.ID + "/task/" + record.Task.ID, HostID: h.Config.ID, Task: record.Task, Status: record.Status, Source: record.Source, Warning: record.Warning, Stale: stale}
			wanted := map[string]bool{}
			for _, id := range record.Status.Processes {
				if id.Valid() {
					wanted[id.Key()] = true
				}
			}
			for _, p := range h.Snapshot.Processes {
				if wanted[p.Key()] {
					if claims[p.Key()] > 1 {
						r.Warning += " 进程关联冲突"
						continue
					}
					r.Processes = append(r.Processes, p)
					claimed[p.Key()] = true
				}
			}
			r.GPUs = resolveGPUs(h.Snapshot.GPUs, r.Processes)
			if len(r.Processes) > 0 {
				r.Owner = r.Processes[0].User
			}
			terminal := r.Status.State == "succeeded" || r.Status.State == "failed" || r.Status.State == "cancelled"
			if r.Status.UpdatedAt.IsZero() || (!terminal && now.Sub(r.Status.UpdatedAt) > 60*time.Second) {
				r.Warning += " 元信息过期或更新时间未知"
			}
			if r.Status.UpdatedAt.After(now.Add(5 * time.Second)) {
				r.Warning += " 远端时间超前，需核对时钟"
			}
			if len(r.Processes) == 0 && r.Status.State == "running" {
				r.Warning += " 未观测到绑定的 GPU 进程，执行结果未知"
			}
			if r.Status.State != "running" && len(r.Processes) > 0 {
				r.Warning += " 状态记录与 GPU 进程观测不一致"
			}
			out = append(out, r)
		}
		for _, p := range h.Snapshot.Processes {
			if claimed[p.Key()] {
				continue
			}
			name := processName(p.Command)
			if name == "" {
				name = fmt.Sprintf("PID %d", p.PID)
			}
			r := Row{Key: h.Config.ID + "/process/" + p.Key(), HostID: h.Config.ID, Task: Task{Name: name, Project: "未登记", Description: "目的未知 · 按 e 添加本地说明"}, Status: Status{State: "observed"}, Processes: []Process{p}, Source: "nvidia-smi + /proc", Inferred: true, Stale: stale, Owner: p.User, Warning: p.Error}
			r.GPUs = resolveGPUs(h.Snapshot.GPUs, r.Processes)
			out = append(out, r)
		}
	}
	for i := range out {
		if a, ok := annotations[out[i].Key]; ok {
			if a.Name != "" {
				out[i].Task.Name = a.Name
			}
			if a.Description != "" {
				out[i].Task.Description = a.Description
			}
			out[i].Source += " + 本地标注"
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HostID != out[j].HostID {
			return out[i].HostID < out[j].HostID
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func processName(command string) string {
	fields := strings.Fields(command)
	for i, f := range fields {
		if f == "-m" && i+1 < len(fields) {
			return fields[i+1]
		}
		if strings.HasSuffix(f, ".py") {
			return filepath.Base(f)
		}
	}
	if len(fields) > 0 {
		return filepath.Base(fields[0])
	}
	return ""
}
func resolveGPUs(gpus []GPU, processes []Process) []GPU {
	set := map[string]bool{}
	for _, p := range processes {
		for _, id := range p.GPUs {
			set[id] = true
		}
	}
	var out []GPU
	for _, g := range gpus {
		if set[g.UUID] {
			out = append(out, g)
		}
	}
	return out
}
func GPUName(gpus []GPU) string {
	if len(gpus) == 0 {
		return "—"
	}
	var s []string
	for _, g := range gpus {
		s = append(s, fmt.Sprint(g.Index))
	}
	return strings.Join(s, ",")
}
func ProgressText(p *Progress) string {
	if p == nil {
		return "进度未知"
	}
	if p.Total == nil {
		return fmt.Sprintf("%.0f %s · 总数未知", p.Completed, Clean(p.Unit))
	}
	return fmt.Sprintf("%.0f / %.0f %s", p.Completed, *p.Total, Clean(p.Unit))
}
func Age(t, now time.Time) string {
	if t.IsZero() {
		return "未知"
	}
	d := now.Sub(t)
	if d < 0 {
		return "时钟偏差"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
func Number(v float64) *float64 { return &v }
