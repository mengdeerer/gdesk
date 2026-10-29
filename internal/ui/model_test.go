package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mengdeerer/gdesk/internal/core"
)

func model(t *testing.T) Model {
	t.Helper()
	m, e := New(core.Config{Interval: 5, Timeout: 12}, nil, true, "")
	if e != nil {
		t.Fatal(e)
	}
	m.gpuView = false // 此辅助函数用于任务列表；合并总览另有独立测试。
	return m
}
func key(m Model, text string) Model {
	code := rune(0)
	if text == "tab" {
		code = tea.KeyTab
	}
	if text == "enter" {
		code = tea.KeyEnter
	}
	if text == "esc" {
		code = tea.KeyEscape
	}
	if code == 0 {
		for _, r := range text {
			code = r
			break
		}
	}
	v, _ := m.Update(tea.KeyPressMsg{Code: code, Text: func() string {
		if len([]rune(text)) == 1 {
			return text
		}
		return ""
	}()})
	return v.(Model)
}
func TestLayoutsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{124, 38}, {80, 24}, {55, 16}, {40, 10}, {180, 50}} {
		for _, panel := range []string{"", "details", "help", "log"} {
			m := model(t)
			m.Width = size[0]
			m.Height = size[1]
			m.panel = panel
			m.logText = strings.Repeat("中文日志123", 100)
			rendered := m.Render()
			lines := strings.Split(rendered, "\n")
			if len(lines) != m.Height {
				t.Fatalf("%v/%s: got %d lines", size, panel, len(lines))
			}
			for _, line := range lines {
				if ansi.StringWidth(line) != m.Width {
					t.Fatalf("%v/%s: width %d", size, panel, ansi.StringWidth(line))
				}
			}
		}
	}
}
func TestFilteringAndNavigation(t *testing.T) {
	m := model(t)
	if len(m.items()) != 4 {
		t.Fatal("demo tasks missing")
	}
	m = key(m, "m")
	if len(m.items()) != 3 {
		t.Fatal("mine filter ignored remote user")
	}
	m = key(m, "/")
	for _, s := range "清洗" {
		m = key(m, string(s))
	}
	m = key(m, "enter")
	if len(m.items()) != 1 {
		t.Fatalf("search failed %d", len(m.items()))
	}
	m = key(m, "esc")
	m = key(m, "tab")
	if !m.gpuView || len(m.items()) != 4 {
		t.Fatalf("mine GPU view wrong: %d", len(m.items()))
	}
}
func TestSelectionSurvivesRefresh(t *testing.T) {
	m := model(t)
	items := m.items()
	m.selected = items[2].key
	m.selection(items)
	s := m.Hosts[0].Snapshot
	next, _ := m.Update(pollMsg{hostID: m.Hosts[0].Config.ID, snapshot: s})
	if next.(Model).selected != items[2].key {
		t.Fatal("selection jumped during refresh")
	}
}
func TestConnectionFailureKeepsStaleData(t *testing.T) {
	m := model(t)
	m.Demo = false
	before := len(m.Hosts[0].Snapshot.GPUs)
	v, _ := m.Update(pollMsg{hostID: m.Hosts[0].Config.ID, err: errTest("timeout")})
	m = v.(Model)
	if len(m.Hosts[0].Snapshot.GPUs) != before || m.hostGood(m.Hosts[0]) {
		t.Fatal("failed host appears idle or live")
	}
	if !m.Hosts[0].NextPoll.After(m.Now) {
		t.Fatal("no retry backoff")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
func TestAnnotationAndLog(t *testing.T) {
	m := model(t)
	m = key(m, "e")
	m.input = "新的实验名"
	m = key(m, "enter")
	m.input = "验证不同的数据增强"
	m = key(m, "enter")
	it, ok := m.current()
	if !ok || it.row.Task.Name != "新的实验名" || it.row.Task.Description != "验证不同的数据增强" {
		t.Fatal("annotation not applied")
	}
	if !strings.Contains(ansi.Strip(m.Render()), "新的实验名") {
		t.Fatal("renamed task missing from overview")
	}
	m = key(m, "l")
	if m.panel != "log" || !strings.Contains(m.logText, "DEMO") {
		t.Fatal("demo log preview missing")
	}
	m.logSeq = 3
	v, _ := m.Update(logMsg{seq: 2, text: "wrong task"})
	if strings.Contains(v.(Model).logText, "wrong task") {
		t.Fatal("stale log response overwrote active task")
	}
}

func TestProgressOrRuntime(t *testing.T) {
	m := model(t)
	it, _ := m.current()
	render := func() string { return ansi.Strip(strings.Join(m.taskLines(it, 110, true), "\n")) }
	if got := render(); !strings.Contains(got, "%") || strings.Contains(got, "已运行") {
		t.Fatal("progress must take precedence: " + got)
	}
	it.row.Status.Progress = nil
	if got := render(); !strings.HasSuffix(strings.TrimSpace(strings.Split(got, "\n")[1]), "3h12m") || strings.Contains(got, "进度未提供") {
		t.Fatal("missing runtime fallback: " + got)
	}
	it.row.Status.StartedAt = time.Time{}
	it.row.Processes = nil
	it.row.Owner = "" // 用户和进度都缺失时不生成空的遥测行。
	if got := m.taskLines(it, 110, true); len(got) != 2 {
		t.Fatal("unknown runtime must not add a placeholder row")
	}
}

func TestTaskColumnsMatchGPUOverview(t *testing.T) {
	m := model(t)
	it, _ := m.current()
	for _, width := range []int{55, 80, 112, 180} {
		for _, name := range []string{"短名称", strings.Repeat("long_task_", 20)} {
			it.row.Task.Name, it.row.Owner = name, "alice"
			lines := m.taskLines(it, width, true)
			line := ansi.Strip(lines[1])
			ownerWidth, progressWidth := 12, 12
			if width-4 < 90 {
				ownerWidth, progressWidth = 8, 8
			}
			ownerAt := strings.Index(line, "alice")
			if ownerAt < 0 || ansi.StringWidth(line[:ownerAt]) != width-ownerWidth-progressWidth-4 {
				t.Fatalf("%d: 用户列漂移: %q", width, line)
			}
			if !strings.HasSuffix(strings.TrimSpace(line), "32%") || ansi.StringWidth(line) != width {
				t.Fatalf("%d: 进度未右对齐: %q", width, line)
			}
		}
	}
}

func TestTaskOwnerVisible(t *testing.T) {
	m := model(t)
	it, _ := m.current()
	for _, width := range []int{47, 72, 110} {
		lines := m.taskLines(it, width, true)
		if len(lines) < 2 || !strings.Contains(ansi.Strip(lines[1]), it.row.Owner) {
			t.Fatalf("owner hidden at width %d", width)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > width {
				t.Fatal("owner caused row overflow")
			}
		}
	}
	m = key(m, "enter")
	if !strings.Contains(ansi.Strip(strings.Join(m.panelLines(110), "\n")), "运行用户   "+it.row.Owner) {
		t.Fatal("details missing process owner")
	}
}
func TestWarningForStaleMetadata(t *testing.T) {
	m := model(t)
	m.Hosts[0].Snapshot.Records[0].Status.UpdatedAt = m.Now.Add(-time.Hour)
	if !strings.Contains(ansi.Strip(m.Render()), "△") || strings.Contains(ansi.Strip(m.Render()), "待核实") {
		t.Fatal("overview must show a compact warning symbol")
	}
	m = key(m, "enter")
	if !strings.Contains(ansi.Strip(strings.Join(m.panelLines(110), "\n")), "元信息过期") {
		t.Fatal("warning reason must remain available in details")
	}
}

func TestUnifiedGPUOverview(t *testing.T) {
	m, err := New(core.Config{Interval: 5, Timeout: 12}, nil, true, "")
	if err != nil || !m.gpuView {
		t.Fatal("default must be unified GPU overview", err)
	}
	// 采集返回乱序时仍按卡号排列，不改变原始快照。
	gpus := m.Hosts[0].Snapshot.GPUs
	gpus[0], gpus[7] = gpus[7], gpus[0]
	items := m.items()
	for i := 0; i < 8; i++ {
		if items[i].gpu.Index != i {
			t.Fatal("GPU indices not numerically ordered")
		}
	}
	if m.Hosts[0].Snapshot.GPUs[0].Index != 7 {
		t.Fatal("render sorting mutated snapshot")
	}
	m.Hosts[0].Snapshot.GPUs[7].Util = core.Number(6)
	it := m.items()[0]
	for _, width := range []int{47, 72, 112} {
		lines := m.gpuLines(it, width, true)
		text := ansi.Strip(strings.Join(lines, "\n"))
		if len(lines) != 3 || lines[2] != "" || ansi.StringWidth(lines[0]) != width || ansi.StringWidth(lines[1]) != width || !strings.Contains(text, "U 6%") || !strings.Contains(text, "alice") {
			t.Fatalf("unified row wrong at width %d: %s", width, text)
		}
		if width == 112 && (!strings.Contains(text, it.row.Task.Name) || !strings.Contains(text, "32%") || !strings.Contains(text, "20.9/32G")) {
			t.Fatal("task, memory or progress missing from GPU row")
		}
	}
	m.host = 0
	if lines := m.mainLines(112, 27); len(lines) > 27 || !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "GPU 7") {
		t.Fatal("one eight-GPU host should fit with spacing in a 38-line terminal")
	}
	m = key(m, "enter")
	m = key(m, "e")
	if m.editing != "name" {
		t.Fatal("unified GPU task details must support editing name")
	}
}

func TestLongAndRepeatedGPULabels(t *testing.T) {
	name := "train_demo_finetune_stage_02.py"
	tasks := []core.Row{{Key: "one", Task: core.Task{Name: name}}, {Key: "two", Task: core.Task{Name: name}}}
	if got := gpuTaskTitle(tasks, 72); got != name+" ×2" {
		t.Fatalf("duplicate labels should collapse: %s", got)
	}
	if len(tasks) != 2 || tasks[0].Key == tasks[1].Key {
		t.Fatal("display grouping must not merge task identities")
	}
	long := strings.Repeat("实验é_", 24) + "checkpoint_stage_02.py"
	tasks = append(tasks, core.Row{Key: "three", Task: core.Task{Name: long}})
	for _, width := range []int{1, 8, 16, 24, 42, 72} {
		for _, rows := range [][]core.Row{tasks, {{Task: core.Task{Name: long}}}} {
			got := gpuTaskTitle(rows, width)
			if ansi.StringWidth(got) > width {
				t.Fatalf("label overflow at %d: %s", width, got)
			}
		}
	}
	if got := gpuTaskTitle(tasks, 42); !strings.Contains(got, "×2") || !strings.Contains(got, "+1项") {
		t.Fatal("hidden records not indicated: " + got)
	}
	if got := shortName(long, 48); !strings.Contains(got, "…") || !strings.HasSuffix(got, ".py") {
		t.Fatal("long filename must preserve recognizable suffix: " + got)
	}
	m := model(t)
	m.gpuView = true
	// 两个真实独立的记录共享一张卡，详情必须保留两条完整名称。
	record := m.Hosts[0].Snapshot.Records[0]
	record.Task.Name = long
	m.Hosts[0].Snapshot.Records[0] = record
	record.Task.ID = "independent-record"
	m.Hosts[0].Snapshot.Records = append(m.Hosts[0].Snapshot.Records, record)
	// 冲突记录本身不会错误绑定；直接检查有明确关联的展示项及详情。
	it := m.items()[0]
	it.tasks = []core.Row{{Task: core.Task{Name: long}}, {Task: core.Task{Name: long}}}
	it.row = nil
	for _, width := range []int{47, 72, 112, 172} {
		lines := m.gpuLines(it, width, true)
		if !strings.Contains(ansi.Strip(lines[0]), "×2") {
			t.Fatal("count lost when shortened")
		}
		for _, line := range lines[:len(lines)-1] {
			if ansi.StringWidth(line) != width {
				t.Fatal("GPU label broke row width")
			}
		}
	}
	m = key(m, "tab")
	for _, task := range m.items() {
		if task.row.Task.Name == long {
			m.selected = task.key
			break
		}
	}
	m = key(m, "enter")
	details := ansi.Strip(strings.Join(m.panelLines(400), "\n"))
	if !strings.Contains(details, long) {
		t.Fatal("detail view lost original name")
	}
}

func TestWideGPUOverviewAndScrolling(t *testing.T) {
	for _, size := range [][2]int{{55, 16}, {80, 24}, {120, 38}, {180, 50}} {
		m := model(t)
		m.gpuView = true
		m.Width, m.Height = size[0], size[1]
		for _, it := range m.items() {
			m.selected = it.key
			m.selection(m.items())
			view := ansi.Strip(m.Render())
			lines := strings.Split(view, "\n")
			if len(lines) != m.Height {
				t.Fatal("wrong screen height")
			}
			found := false
			for _, line := range lines {
				if ansi.StringWidth(line) != m.Width {
					t.Fatal("row overflow")
				}
				if strings.Contains(line, "▎") && strings.Contains(line, fmt.Sprintf("GPU %d", it.gpu.Index)) {
					found = true
				}
			}
			if !found {
				t.Fatalf("selected GPU hidden at %v", size)
			}
		}
		if m.Width == 180 {
			for _, line := range strings.Split(ansi.Strip(m.Render()), "\n") {
				if strings.Contains(line, "G-Desk") && !strings.HasPrefix(line, "    G-Desk") {
					t.Fatal("wide terminal still constrained to centered narrow column")
				}
			}
		}
	}
}

func TestAllHostsFitCompactOverview(t *testing.T) {
	m := model(t)
	m.gpuView = true
	items := m.items()
	if len(items) != 20 {
		t.Fatalf("演示应有 20 张卡，实际 %d", len(items))
	}
	lines := m.mainLines(112, 27)
	text := ansi.Strip(strings.Join(lines, "\n"))
	if len(lines) > 27 || strings.Count(text, "GPU ") != len(items) {
		t.Fatalf("全部卡未同屏展示: %s", text)
	}
	for _, host := range m.Hosts {
		if !strings.Contains(text, host.Config.ID) {
			t.Fatal("遗漏服务器", host.Config.ID)
		}
	}
	if !strings.Contains(text, m.Hosts[2].Error) {
		t.Fatal("紧凑视图遗漏错误")
	}
	// 只选择一台服务器时，继续使用两行资源布局。
	m.host = 0
	text = ansi.Strip(strings.Join(m.mainLines(112, 27), "\n"))
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "GPU 0") && strings.Contains(line, "U 92%") {
			t.Fatal("单机视图不应被压缩")
		}
	}
}

func TestCompactGPUColumnsAndUnknownMetrics(t *testing.T) {
	m := model(t)
	m.gpuView = true
	it := m.items()[0]
	for _, width := range []int{70, 72, 100, 112, 172} {
		ownerColumn, utilizationColumn := -1, -1
		for i, owner := range []string{"alice", "用户乙", "bob", ""} {
			r := *it.row
			r.Owner, r.Task.Name = owner, strings.Repeat("长实验名称_", i+1)+"train.py"
			r.Status.Progress = nil
			r.Status.StartedAt = it.host.Snapshot.ObservedAt.Add(-10 * time.Minute)
			it.tasks = []core.Row{r}
			it.gpu.Used = core.Number([]float64{1024, 12288, 32768, 1024}[i])
			line := ansi.Strip(m.compactGPULine(it, width, true))
			if strings.Contains(line, "\n") || ansi.StringWidth(line) != width || !strings.HasSuffix(strings.TrimSpace(line), "10m") {
				t.Fatalf("%d: 行宽或时长错误: %q", width, line)
			}
			idx := strings.Index(line, "U 92%")
			if idx < 0 {
				t.Fatal("利用率被截断", line)
			}
			column := ansi.StringWidth(line[:idx])
			if utilizationColumn >= 0 && column != utilizationColumn {
				t.Fatal("利用率列漂移")
			}
			utilizationColumn = column
			if owner != "" {
				idx = strings.Index(line, owner)
				if idx < 0 {
					t.Fatal("用户被截断", line)
				}
				column = ansi.StringWidth(line[:idx])
				if ownerColumn >= 0 && column != ownerColumn {
					t.Fatal("用户列漂移")
				}
				ownerColumn = column
			}
		}
	}
	it.gpu.Used, it.gpu.Util = nil, nil
	line := ansi.Strip(m.compactGPULine(it, 112, false))
	if !strings.Contains(line, "????") || !strings.Contains(line, "U —") {
		t.Fatal("未知指标不得呈现为零", line)
	}
}

func TestGPUUserAndRuntimeColumnsStayAligned(t *testing.T) {
	m := model(t)
	it, _ := m.current()
	m.gpuView = true
	it = m.items()[0]
	for _, width := range []int{47, 72, 112, 172} {
		ownerColumn, utilizationColumn := -1, -1
		for i, owner := range []string{"alice", "用户乙", "bob", ""} {
			r := *it.row
			r.Owner = owner
			r.Task.Name = strings.Repeat("长任务", i+1)
			r.Status.Progress = nil
			r.Status.StartedAt = it.host.Snapshot.ObservedAt.Add(-time.Duration(10+i*70) * time.Minute)
			it.tasks = []core.Row{r}
			it.gpu.Used = core.Number([]float64{1024, 12288, 32768, 1024}[i])
			lines := m.gpuLines(it, width, false)
			metadataLine := 1
			if width-4 < 90 {
				metadataLine = 0
			}
			line := ansi.Strip(lines[metadataLine])
			runtime := m.shortProgress(r, it.host)
			if !strings.HasSuffix(strings.TrimRight(line, " "), runtime) || ansi.StringWidth(strings.TrimRight(line, " ")) != width-2 {
				t.Fatalf("runtime must end at right content edge at width %d: %q", width, line)
			}
			if owner != "" {
				at := strings.Index(line, owner)
				if at < 0 {
					t.Fatal("owner disappeared")
				}
				column := ansi.StringWidth(line[:at])
				if ownerColumn >= 0 && column != ownerColumn {
					t.Fatal("owner column moved with text length")
				}
				ownerColumn = column
			} else if strings.TrimSpace(ansi.Cut(line, ownerColumn, ownerColumn+8)) != "" {
				t.Fatal("missing owner collapsed its reserved column")
			}
			stats := ansi.Strip(lines[1])
			column := ansi.StringWidth(stats[:strings.Index(stats, "U ")])
			if utilizationColumn >= 0 && column != utilizationColumn {
				t.Fatal("utilization moved with memory value width")
			}
			utilizationColumn = column
		}
	}
}

func TestLatestStartSortAndSelection(t *testing.T) {
	m := model(t)
	m.gpuView = true
	m.host = 0
	m.Hosts[0].Snapshot.Records[1].Status.StartedAt = m.Now.Add(-time.Minute)
	m.selection(m.items())
	selected := m.selected
	m = key(m, "s")
	items := m.items()
	if !m.latestFirst || items[0].gpu.Index != 4 || items[1].gpu.Index != 0 || items[2].gpu.Index != 1 {
		t.Fatal("newest task must sort first, tied GPU indices must stay ordered")
	}
	if it, _ := m.current(); it.key != selected {
		t.Fatal("sorting changed selected task")
	}
	if items[3].gpu.Index != 2 {
		t.Fatal("idle cards must follow known tasks in GPU order")
	}
	m = key(m, "s")
	for i, it := range m.items() {
		if it.gpu.Index != i {
			t.Fatal("could not restore GPU index order")
		}
	}
	// 自动发现进程也可按实际运行时长排序；未知时间不冒充最新任务。
	m.Hosts[0].Snapshot.Records = nil
	m.Hosts[0].Snapshot.Processes[0].ElapsedSeconds = core.Number(100)
	m.Hosts[0].Snapshot.Processes[1].ElapsedSeconds = core.Number(10)
	m = key(m, "s")
	if m.items()[0].gpu.Index != 4 {
		t.Fatal("inferred task runtime ignored")
	}
	m.Hosts[0].Snapshot.Processes[1].ElapsedSeconds = nil
	if m.items()[0].gpu.Index != 0 {
		t.Fatal("unknown runtime sorted as newest")
	}
}

func TestMetadataLivesInDetailsOnly(t *testing.T) {
	m := model(t)
	main := ansi.Strip(m.Render())
	for _, record := range m.Hosts[0].Snapshot.Records {
		if strings.Contains(main, record.Task.Description) || strings.Contains(main, record.Task.Project) {
			t.Fatal("detailed metadata leaked into overview")
		}
	}
	m = key(m, "enter")
	if m.panel != "details" {
		t.Fatal("Enter did not open details")
	}
	details := ansi.Strip(strings.Join(m.panelLines(110), "\n"))
	for _, want := range []string{"比较较小学习率是否改善验证集精度", "image-classification", "lr = 0.0001", "实时关联进程"} {
		if !strings.Contains(details, want) {
			t.Fatalf("detail field missing: %s", want)
		}
	}
}

func TestSelectedTaskVisibleAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{55, 16}, {80, 24}, {112, 28}} {
		m := model(t)
		m.Width = size[0]
		m.Height = size[1]
		items := m.items()
		for _, it := range items {
			m.selected = it.key
			view := ansi.Strip(m.Render())
			if !strings.Contains(view, it.row.Task.Name) {
				t.Fatalf("selected task hidden at %v: %s", size, it.row.Task.Name)
			}
		}
	}
}

func TestLeftReturnsWithoutChangingHostOrSelection(t *testing.T) {
	for _, panel := range []string{"details", "log", "help"} {
		m := model(t)
		m.host = 1
		m.selection(m.items())
		selected := m.selected
		m.panel = panel
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		next := updated.(Model)
		if next.panel != "" || next.host != 1 || next.selected != selected {
			t.Fatalf("left navigation changed context in %s", panel)
		}
	}
}
func TestFirstRunHostInputAndSave(t *testing.T) {
	config := core.Config{Interval: 5, Timeout: 12}
	m, err := New(config, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	m.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	if len(m.Hosts) != 0 || m.editing != "host" {
		t.Fatal("first run must prompt for alias without predefined hosts")
	}
	m.input = "ssh atlas-gpu"
	m = key(m, "enter")
	if len(m.Hosts) != 0 || m.editing != "host" || !strings.Contains(ansi.Strip(m.Render()), "不要包含 ssh") {
		t.Fatal("invalid alias should remain editable with visible error")
	}
	m.input = "atlas-gpu"
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || len(m.Hosts) != 1 || m.Hosts[0].Config.SSH != "atlas-gpu" || !m.Hosts[0].Loading || m.editing != "" {
		t.Fatal("adding alias did not save and schedule initial poll")
	}
	m = key(m, "a")
	m.input = "nebula-gpu"
	m = key(m, "enter")
	disk, err := core.LoadConfig(m.ConfigPath)
	if err != nil || len(disk.Hosts) != 2 || disk.Hosts[1].SSH != "nebula-gpu" {
		t.Fatalf("aliases not persisted: %v", err)
	}
}
func TestAddHostWriteFailureDoesNotChangeSession(t *testing.T) {
	m, err := New(core.Config{Interval: 5, Timeout: 12}, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	m.ConfigPath = filepath.Join(t.TempDir(), "missing", "config.json")
	m.input = "nebula-gpu"
	m = key(m, "enter")
	if len(m.Hosts) != 0 || len(m.Config.Hosts) != 0 || m.editing != "host" || m.notice == "" {
		t.Fatal("failed save must leave hosts unchanged and show error")
	}
}

func TestEditNameAndDescriptionInsideDetailsPersists(t *testing.T) {
	m := model(t)
	m.Demo = false
	m.annotationPath = filepath.Join(t.TempDir(), "annotations.json")
	before, _ := m.current()
	m = key(m, "enter")
	m = key(m, "e")
	if m.editing != "name" || m.input != before.row.Task.Name {
		t.Fatal("details e must start with the existing name")
	}
	m.input = "新的任务名称"
	m = key(m, "enter")
	if m.editing != "description" || m.input != before.row.Task.Description {
		t.Fatal("name confirmation must prefill existing description")
	}
	m.input = "验证更低学习率的效果"
	m = key(m, "enter")
	after, _ := m.current()
	if m.panel != "details" || after.row.Task.Name != "新的任务名称" || after.row.Task.Description != "验证更低学习率的效果" {
		t.Fatal("edit lost detail context or saved fields")
	}
	if !strings.Contains(ansi.Strip(m.Render()), "已保存本地标注") {
		t.Fatal("save result hidden in details")
	}
	reloaded, err := New(m.Config, nil, true, m.annotationPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved := reloaded.annotations[before.row.Key]; saved.Name != "新的任务名称" || saved.Description != "验证更低学习率的效果" {
		t.Fatal("name and description did not survive reload")
	}
	m = key(m, "e")
	if m.input != "新的任务名称" {
		t.Fatal("editing again must prefill saved name")
	}
	m.input = "未保存的名称"
	m = key(m, "enter")
	if m.input != "验证更低学习率的效果" {
		t.Fatal("editing again must prefill saved description")
	}
	m.input = "未保存的修改"
	m = key(m, "esc")
	it, _ := m.current()
	if it.row.Task.Name != "新的任务名称" || it.row.Task.Description != "验证更低学习率的效果" {
		t.Fatal("cancel changed saved fields")
	}
	m = key(m, "esc")
	if !strings.Contains(ansi.Strip(m.Render()), "新的任务名称") {
		t.Fatal("overview did not display the name saved from details")
	}
}
func TestDescriptionSaveFailureKeepsDraftVisible(t *testing.T) {
	m := model(t)
	m.Demo = false
	m.annotationPath = filepath.Join(t.TempDir(), "missing", "annotations.json")
	m = key(m, "enter")
	m = key(m, "e")
	m = key(m, "enter")
	m.input = "保留这段说明"
	m = key(m, "enter")
	if m.editing != "description" || m.input != "保留这段说明" || !strings.Contains(ansi.Strip(m.Render()), "保存失败") {
		t.Fatal("failed save lost draft or hid error")
	}
}
func configuredModel(t *testing.T) Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	c := core.Config{Interval: 5, Timeout: 12}
	for _, alias := range []string{"atlas-gpu", "nebula-gpu"} {
		var err error
		c, err = core.AddHost(path, c, alias)
		if err != nil {
			t.Fatal(err)
		}
	}
	m, err := New(c, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	m.ConfigPath = path
	return m
}
func TestRemoveHostAndIgnoreInflightResponses(t *testing.T) {
	m := configuredModel(t)
	_ = m.refresh(false)
	first, second := m.Hosts[0].PollToken, m.Hosts[1].PollToken
	m.host = 0
	m = key(m, "x")
	if m.panel != "remove_host" || !strings.Contains(ansi.Strip(m.Render()), "atlas-gpu") {
		t.Fatal("removal target not shown")
	}
	m = key(m, "esc")
	if len(m.Hosts) != 2 {
		t.Fatal("cancel removed server")
	}
	m = key(m, "x")
	m = key(m, "enter")
	if len(m.Hosts) != 1 || m.Hosts[0].Config.ID != "nebula-gpu" {
		t.Fatal("wrong host removed")
	}
	disk, err := core.LoadConfig(m.ConfigPath)
	if err != nil || len(disk.Hosts) != 1 || disk.Hosts[0].ID != "nebula-gpu" {
		t.Fatal("removal not persisted")
	}
	bad := core.Snapshot{User: "wrong-host"}
	v, _ := m.Update(pollMsg{hostID: "atlas-gpu", token: first, snapshot: bad})
	m = v.(Model)
	if m.Hosts[0].Snapshot.User == "wrong-host" {
		t.Fatal("removed host response corrupted another host")
	}
	m.host = 0
	m = key(m, "x")
	m = key(m, "enter")
	if len(m.Hosts) != 0 {
		t.Fatal("last host removal failed")
	}
	m = key(m, "a")
	m.input = "nebula-gpu"
	m = key(m, "enter")
	v, _ = m.Update(pollMsg{hostID: "nebula-gpu", token: second, snapshot: bad})
	m = v.(Model)
	if m.Hosts[0].Snapshot.User == "wrong-host" || !m.Hosts[0].Loading {
		t.Fatal("old response accepted after re-adding alias")
	}
	token := m.Hosts[0].PollToken
	v, _ = m.Update(pollMsg{hostID: "nebula-gpu", token: token, snapshot: core.Snapshot{User: "correct-host", GPUOK: true, ProcessesOK: true}})
	if v.(Model).Hosts[0].Snapshot.User != "correct-host" {
		t.Fatal("fresh response rejected")
	}
}
func TestRemoveFailureKeepsHostAndShowsError(t *testing.T) {
	m := configuredModel(t)
	m.host = 0
	m = key(m, "x")
	if err := os.WriteFile(m.ConfigPath, []byte(`{"hosts":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	m = key(m, "enter")
	if len(m.Hosts) != 2 || m.panel != "remove_host" || !strings.Contains(ansi.Strip(m.Render()), "移除失败") {
		t.Fatal("failed removal changed host list or hid error")
	}
}
func TestFreeCardsVisibleEvenWithoutTasksAndIndependentOfFilters(t *testing.T) {
	m := model(t)
	before := ansi.Strip(m.hostTabs(112))
	m = key(m, "m")
	after := ansi.Strip(m.hostTabs(112))
	if before != after || !strings.Contains(after, "orion-gpu 空闲 5") || !strings.Contains(after, "nova-gpu 空闲未知") {
		t.Fatal("user filter changed availability or offline host shown free")
	}
	m.mine = false
	m.Hosts[0].Snapshot.Processes = nil
	m.Hosts[0].Snapshot.Records = nil
	for i := range m.Hosts[0].Snapshot.GPUs {
		m.Hosts[0].Snapshot.GPUs[i].Used = core.Number(0)
		m.Hosts[0].Snapshot.GPUs[i].Util = core.Number(0)
	}
	m.host = 0
	text := ansi.Strip(m.Render())
	if !strings.Contains(text, "空闲 8/8") || !strings.Contains(text, "卡 0,1,2,3,4,5,6,7") {
		t.Fatal("idle host vanished from task view")
	}
}
