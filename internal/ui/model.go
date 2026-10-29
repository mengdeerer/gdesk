package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mengdeerer/gdesk/internal/collect"
	"github.com/mengdeerer/gdesk/internal/core"
)

type Model struct {
	Config                    core.Config
	ConfigPath                string
	Hosts                     []core.Host
	Demo                      bool
	Client                    *collect.Client
	Width, Height             int
	Now                       time.Time
	gpuView, mine             bool
	latestFirst               bool
	host                      int
	selected                  string
	index                     int
	query                     string
	editing                   string
	input, nameDraft, editKey string
	descriptionDraft          string
	removeHostID              string
	pollSequence              uint64
	panel                     string
	panelOffset               int
	logText, logTitle         string
	logSeq                    int
	notice                    string
	annotations               map[string]core.Annotation
	annotationPath            string
}
type tickMsg time.Time
type pollMsg struct {
	hostID   string
	token    uint64
	snapshot core.Snapshot
	err      error
}
type logMsg struct {
	seq  int
	text string
	err  error
}
type item struct {
	key   string
	host  core.Host
	row   *core.Row
	gpu   *core.GPU
	tasks []core.Row
}

func New(config core.Config, client *collect.Client, demo bool, annotations string) (Model, error) {
	m := Model{Config: config, Client: client, Demo: demo, Width: 120, Height: 38, Now: time.Now(), host: -1, gpuView: true, annotations: map[string]core.Annotation{}, annotationPath: annotations}
	if demo {
		m.Hosts = core.Demo(m.Now)
	} else {
		for _, h := range config.Hosts {
			m.Hosts = append(m.Hosts, core.Host{Config: h})
		}
		if len(config.Hosts) == 0 {
			m.editing = "host"
		}
	}
	if annotations != "" {
		b, e := os.ReadFile(annotations)
		if e == nil {
			if e = json.Unmarshal(b, &m.annotations); e != nil {
				return m, fmt.Errorf("本地标注损坏: %w", e)
			}
		} else if !os.IsNotExist(e) {
			return m, e
		}
	}
	return m, nil
}
func tick() tea.Cmd           { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (m Model) Init() tea.Cmd { return tick() }
func (m Model) poll(i int) tea.Cmd {
	h := m.Hosts[i].Config
	token := m.Hosts[i].PollToken
	timeout := time.Duration(m.Config.Timeout) * time.Second
	return func() tea.Msg {
		s, e := m.Client.Poll(h, timeout)
		return pollMsg{hostID: h.ID, token: token, snapshot: s, err: e}
	}
}
func (m *Model) refresh(force bool) tea.Cmd {
	if m.Demo {
		return nil
	}
	var cmds []tea.Cmd
	for i := range m.Hosts {
		h := &m.Hosts[i]
		if !h.Loading && (force || !m.Now.Before(h.NextPoll)) {
			h.Loading = true
			m.pollSequence++
			h.PollToken = m.pollSequence
			cmds = append(cmds, m.poll(i))
		}
	}
	return tea.Batch(cmds...)
}
func (m Model) items() []item {
	rows := core.Rows(m.Hosts, m.annotations, m.Now)
	out := []item{}
	match := func(r core.Row, h core.Host) bool {
		user := h.Config.User
		if user == "" {
			user = h.Snapshot.User
		}
		if m.mine && (r.Owner == "" || r.Owner != user) {
			return false
		}
		text := r.Task.Name + " " + r.Task.Description + " " + core.Summary(r.Task) + " " + strings.Join(r.Task.Tags, " ") + " " + r.HostID + " " + r.Owner
		return strings.Contains(strings.ToLower(text), strings.ToLower(m.query))
	}
	for hi, h := range m.Hosts {
		if m.host >= 0 && m.host != hi {
			continue
		}
		hostStart := len(out)
		if m.gpuView {
			gpus := append([]core.GPU(nil), h.Snapshot.GPUs...)
			sort.SliceStable(gpus, func(i, j int) bool { return gpus[i].Index < gpus[j].Index })
			for _, g := range gpus {
				var tasks []core.Row
				for _, r := range rows {
					if r.HostID != h.Config.ID || !match(r, h) {
						continue
					}
					for _, rg := range r.GPUs {
						if rg.UUID == g.UUID {
							tasks = append(tasks, r)
							break
						}
					}
				}
				if len(tasks) == 0 && (m.mine || m.query != "") {
					continue
				}
				it := item{key: h.Config.ID + "/gpu/" + g.UUID, host: h, gpu: &g, tasks: tasks}
				if len(tasks) == 1 {
					r := tasks[0]
					it.row = &r
				}
				out = append(out, it)
			}
			// 没有关联 GPU 的任务仍可见，避免已结束或待关联的记录消失。
			for _, r := range rows {
				if r.HostID == h.Config.ID && len(r.GPUs) == 0 && match(r, h) {
					copy := r
					out = append(out, item{key: r.Key, host: h, row: &copy})
				}
			}
		} else {
			for _, r := range rows {
				if r.HostID == h.Config.ID && match(r, h) {
					copy := r
					out = append(out, item{key: r.Key, host: h, row: &copy})
				}
			}
		}
		if m.latestFirst {
			sort.SliceStable(out[hostStart:], func(i, j int) bool {
				a, ak := itemAge(out[hostStart+i])
				b, bk := itemAge(out[hostStart+j])
				if ak != bk {
					return ak
				}
				return ak && a < b
			})
		}
	}
	return out
}

func itemAge(it item) (float64, bool) {
	rows := it.tasks
	if it.row != nil {
		rows = []core.Row{*it.row}
	}
	best, known := 0.0, false
	for _, row := range rows {
		if age, ok := core.StartAge(row, it.host); ok && (!known || age < best) {
			best, known = age, true
		}
	}
	return best, known
}
func (m *Model) selection(items []item) {
	if len(items) == 0 {
		m.index = 0
		m.selected = ""
		return
	}
	for i, it := range items {
		if it.key == m.selected {
			m.index = i
			return
		}
	}
	m.index = max(0, min(m.index, len(items)-1))
	m.selected = items[m.index].key
}
func (m Model) current() (item, bool) {
	items := m.items()
	if len(items) == 0 {
		return item{}, false
	}
	for _, it := range items {
		if it.key == m.selected {
			return it, true
		}
	}
	return items[min(m.index, len(items)-1)], true
}
func (m *Model) saveAnnotation() error {
	next := make(map[string]core.Annotation, len(m.annotations)+1)
	for k, v := range m.annotations {
		next[k] = v
	}
	next[m.editKey] = core.Annotation{Name: m.nameDraft, Description: m.input}
	if m.Demo {
		m.annotations = next
		m.notice = "演示标注已更新（仅本次会话）"
		return nil
	}
	data, e := json.MarshalIndent(next, "", "  ")
	if e != nil {
		return e
	}
	dir := filepath.Dir(m.annotationPath)
	f, e := os.CreateTemp(dir, ".gpu-desk-annotations-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Rename(f.Name(), m.annotationPath); e != nil {
		return e
	}
	m.annotations = next
	m.notice = "已保存本地标注"
	return nil
}
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
	case tickMsg:
		if m.Demo {
			return m, tick()
		}
		m.Now = time.Time(msg)
		return m, tea.Batch(tick(), m.refresh(false))
	case pollMsg:
		index := -1
		for i, h := range m.Hosts {
			if h.Config.ID == msg.hostID && h.PollToken == msg.token {
				index = i
				break
			}
		}
		if index < 0 {
			return m, nil
		} // 已移除或重新添加的主机：丢弃旧采集响应。
		h := &m.Hosts[index]
		h.Loading = false
		if msg.err != nil {
			h.Error = msg.err.Error()
			h.Failures++
			delay := min(60, m.Config.Interval*(1<<min(h.Failures, 4)))
			h.NextPoll = m.Now.Add(time.Duration(delay) * time.Second)
		} else {
			if !msg.snapshot.GPUOK {
				msg.snapshot.GPUs = h.Snapshot.GPUs
			}
			if !msg.snapshot.ProcessesOK {
				msg.snapshot.Processes = h.Snapshot.Processes
			}
			h.Snapshot = msg.snapshot
			h.ReceivedAt = m.Now
			h.Error = ""
			h.Failures = 0
			h.NextPoll = m.Now.Add(time.Duration(m.Config.Interval) * time.Second)
		}
		m.selection(m.items())
	case logMsg:
		if msg.seq == m.logSeq {
			if msg.err != nil {
				m.logText = "读取失败: " + msg.err.Error()
			} else {
				m.logText = msg.text
			}
			m.panelOffset = 0
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.editing != "" {
			switch key {
			case "esc":
				if m.editing == "search" {
					m.query = ""
				}
				m.editing = ""
				m.input = ""
			case "enter":
				switch m.editing {
				case "host":
					config, e := core.AddHost(m.ConfigPath, m.Config, m.input)
					if e != nil {
						m.notice = e.Error()
						return m, nil
					}
					m.Config = config
					m.Hosts = append(m.Hosts, core.Host{Config: config.Hosts[len(config.Hosts)-1]})
					m.host = len(m.Hosts) - 1
					m.index = 0
					m.selected = ""
					m.query = ""
					m.editing = ""
					m.input = ""
					m.notice = ""
					return m, m.refresh(false)
				case "search":
					m.query = m.input
					m.editing = ""
				case "name":
					m.nameDraft = m.input
					m.input = m.descriptionDraft
					m.editing = "description"
				case "description":
					if e := m.saveAnnotation(); e != nil {
						m.notice = "保存失败: " + e.Error()
						return m, nil
					}
					m.editing = ""
					m.query = ""
					m.panelOffset = 0
				}
			case "backspace":
				r := []rune(m.input)
				if len(r) > 0 {
					m.input = string(r[:len(r)-1])
				}
			case "ctrl+u":
				m.input = ""
			default:
				if msg.Text != "" && len([]rune(m.input)) < 500 {
					m.input += core.Clean(msg.Text)
				}
			}
			if m.editing == "search" {
				m.query = m.input
				m.index = 0
				m.selected = ""
			}
			m.selection(m.items())
			return m, nil
		}
		if m.panel != "" {
			if m.panel == "remove_host" {
				switch key {
				case "esc", "left", "q", "n":
					m.panel = ""
					m.removeHostID = ""
					m.notice = ""
				case "enter", "y":
					if err := m.removeHost(); err != nil {
						m.notice = "移除失败: " + err.Error()
					}
				}
				return m, nil
			}
			switch key {
			case "e":
				if m.panel == "details" {
					m.beginAnnotation()
				}
			case "l":
				if m.panel == "details" {
					return m, m.openLog()
				}
			case "esc", "left", "q", "enter":
				m.panel = ""
			case "up", "k":
				m.panelOffset = max(0, m.panelOffset-1)
			case "down", "j":
				m.panelOffset++
			case "pgdown":
				m.panelOffset += max(1, m.Height-8)
			case "pgup":
				m.panelOffset = max(0, m.panelOffset-max(1, m.Height-8))
			case "r":
				if m.panel == "log" {
					return m, m.openLog()
				}
			}
			return m, nil
		}
		switch key {
		case "s":
			m.latestFirst = !m.latestFirst
		case "x":
			if m.host < 0 || m.host >= len(m.Hosts) {
				m.notice = "先用 ←→ 选择要移除的服务器，再按 x"
				break
			}
			m.removeHostID = m.Hosts[m.host].Config.ID
			m.panel = "remove_host"
			m.panelOffset = 0
			m.notice = ""
		case "a":
			if m.Demo {
				m.notice = "演示模式。运行 gdesk 添加服务器"
				break
			}
			m.editing = "host"
			m.input = ""
			m.notice = ""
		case "q":
			return m, tea.Quit
		case "tab":
			m.gpuView = !m.gpuView
			m.index = 0
			m.selected = ""
		case "m":
			m.mine = !m.mine
			m.index = 0
			m.selected = ""
		case "/":
			m.editing = "search"
			m.input = m.query
		case "esc":
			m.query = ""
			m.notice = ""
		case "h", "left":
			m.host--
			if m.host < -1 {
				m.host = len(m.Hosts) - 1
			}
			m.selected = ""
			m.index = 0
		case "right":
			m.host++
			if m.host >= len(m.Hosts) {
				m.host = -1
			}
			m.selected = ""
			m.index = 0
		case "up", "k":
			m.selection(m.items())
			m.index = max(0, m.index-1)
			m.selected = ""
		case "down", "j":
			m.selection(m.items())
			m.index++
			m.selected = ""
		case "enter":
			if _, ok := m.current(); ok {
				m.panel = "details"
				m.panelOffset = 0
			}
		case "?":
			m.panel = "help"
			m.panelOffset = 0
		case "l":
			return m, m.openLog()
		case "e":
			m.beginAnnotation()
		case "r":
			m.notice = "正在刷新"
			return m, m.refresh(true)
		}
		m.selection(m.items())
	}
	return m, nil
}

func (m *Model) beginAnnotation() {
	it, ok := m.current()
	if !ok || it.row == nil {
		m.notice = "请切到任务视图选择单个任务后编辑名称和说明"
		return
	}
	r := it.row
	if r.Inferred && (len(r.Processes) == 0 || !r.Processes[0].Identity.Valid()) {
		m.notice = "进程身份读取受限，暂不能持久化标注"
		return
	}
	m.editKey = r.Key
	m.nameDraft = r.Task.Name
	m.descriptionDraft = r.Task.Description
	m.notice = ""
	if r.Inferred && m.annotations[r.Key].Description == "" {
		m.descriptionDraft = ""
	}
	m.editing = "name"
	m.input = m.nameDraft
}

func (m *Model) removeHost() error {
	index := -1
	for i, h := range m.Hosts {
		if h.Config.ID == m.removeHostID {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("服务器已经不在列表中")
	}
	if !m.Demo {
		config, err := core.RemoveHost(m.ConfigPath, m.Config, m.removeHostID)
		if err != nil {
			return err
		}
		m.Config = config
	}
	remaining := make([]core.Host, 0, len(m.Hosts)-1)
	remaining = append(remaining, m.Hosts[:index]...)
	remaining = append(remaining, m.Hosts[index+1:]...)
	m.Hosts = remaining
	m.host = -1
	m.selected = ""
	m.index = 0
	m.panel = ""
	m.panelOffset = 0
	m.logSeq++
	m.notice = "已从监控列表移除 " + m.removeHostID
	m.removeHostID = ""
	m.selection(m.items())
	return nil
}
func (m *Model) openLog() tea.Cmd {
	it, ok := m.current()
	if !ok || it.row == nil {
		m.notice = "请在任务视图选择单个任务"
		return nil
	}
	path := ""
	for _, a := range append(append([]core.Artifact{}, it.row.Task.Artifacts...), it.row.Status.Artifacts...) {
		if a.Role == "log" {
			path = a.Path
		}
	}
	if path == "" {
		m.notice = "此任务尚未登记日志路径"
		return nil
	}
	m.panel = "log"
	m.logTitle = path
	m.panelOffset = 0
	m.logText = "正在读取末尾 64 KiB / 最多 160 行…"
	m.logSeq++
	seq := m.logSeq
	if m.Demo {
		m.logText = "[DEMO] 演示日志，非服务器数据\n\n15:10:02  INFO  Loaded immutable config snapshot\n15:10:03  INFO  Resuming attempt-02\n15:10:05  INFO  Dataset ready · preprocessing complete\n15:10:08  INFO  Processing batch 32 / 100\n15:10:09  INFO  loss=1.82   throughput=186 samples/s\n15:10:10  INFO  Status written atomically\n\n按 r 重读日志末尾 · Esc 返回"
		return nil
	}
	client := m.Client
	timeout := time.Duration(m.Config.Timeout) * time.Second
	return func() tea.Msg { text, e := client.Log(it.host.Config, path, timeout); return logMsg{seq, text, e} }
}
func (m Model) View() tea.View           { v := tea.NewView(m.Render()); v.AltScreen = true; return v }
func (m *Model) SetGPUView(enabled bool) { m.gpuView = enabled }
