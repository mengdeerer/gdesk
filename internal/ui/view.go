package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mengdeerer/gdesk/internal/core"
)

const (
	bg         = "#282A36"
	fg         = "#F8F8F2"
	muted      = "#6272A4"
	cyan       = "#8BE9FD"
	yellow     = "#F1FA8C"
	purple     = "#BD93F9"
	line       = "#44475A"
	selectedBG = "#44475A"
	green      = "#50FA7B"
	red        = "#FF5555"
)

func paint(s, color string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(s)
}
func bold(s, color string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Render(s)
}
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = ansi.Truncate(s, width, "…")
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}

func fitRight(s string, width int) string {
	s = ansi.Truncate(s, max(0, width), "…")
	return strings.Repeat(" ", max(0, width-ansi.StringWidth(s))) + s
}

// 尾部两列的位置只取决于窗口宽度，不随名称、计数或缺失字段移动。
func gpuColumns(left, owner, progress string, width int) string {
	ownerWidth, progressWidth := 12, 12
	if width < 90 {
		ownerWidth, progressWidth = 8, 8
	}
	leftWidth := max(0, width-ownerWidth-progressWidth-4)
	return fit(left, leftWidth) + "  " + fit(paint(owner, purple), ownerWidth) + "  " + fitRight(paint(progress, cyan), progressWidth)
}

// 省略中部，保留任务名前缀及可辨认的脚本后缀；按终端显示宽度切分。
func shortName(name string, width int) string {
	name = core.Clean(name)
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(name) <= width {
		return name
	}
	if width < 16 {
		return ansi.Truncate(name, width, "…")
	}
	tailWidth := min(16, width/3)
	return ansi.Truncate(name, width-tailWidth-1, "") + "…" + ansi.Cut(name, ansi.StringWidth(name)-tailWidth, ansi.StringWidth(name))
}

// 只折叠显示标签，不合并任务身份、进程或状态。×N 表示同名记录数。
func gpuTaskTitle(tasks []core.Row, width int) string {
	if len(tasks) == 0 {
		return ""
	}
	names, counts := []string{}, map[string]int{}
	for _, task := range tasks {
		name := core.Clean(task.Task.Name)
		if counts[name] == 0 {
			names = append(names, name)
		}
		counts[name]++
	}
	labels := make([]string, len(names))
	for i, name := range names {
		labels[i] = name
		if counts[name] > 1 {
			labels[i] += fmt.Sprintf(" ×%d", counts[name])
		}
	}
	if joined := strings.Join(labels, " / "); ansi.StringWidth(joined) <= width {
		return joined
	}
	suffix := ""
	if counts[names[0]] > 1 {
		suffix = fmt.Sprintf(" ×%d", counts[names[0]])
	}
	if len(names) > 1 {
		suffix += fmt.Sprintf(" +%d项", len(names)-1)
	}
	return shortName(names[0], max(0, width-ansi.StringWidth(suffix))) + ansi.Truncate(suffix, width, "…")
}
func pair(left, right string, width int) string {
	rw := ansi.StringWidth(right)
	if rw > width/2 {
		return fit(left, width)
	}
	return fit(left, max(0, width-rw)) + right
}
func rule(width int) string { return paint(strings.Repeat("─", max(0, width)), line) }
func bar(value float64, width int) string {
	n := max(0, min(width, int(value*float64(width))))
	return paint(strings.Repeat("━", n), cyan) + paint(strings.Repeat("━", width-n), line)
}
func memory(g core.GPU) string {
	if g.Used == nil || g.Total == nil {
		return "显存未知"
	}
	return fmt.Sprintf("%.1f/%.0fG", *g.Used/1024, *g.Total/1024)
}
func util(g core.GPU) string {
	if g.Util == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", *g.Util)
}
func (m Model) hostGood(h core.Host) bool { return core.FreshHost(h, m.Now) }
func (m Model) state(r core.Row) string {
	if r.Stale {
		return paint("◌ 观测过期", yellow)
	}
	if r.Warning != "" {
		return paint("△ 待核实", yellow)
	}
	switch r.Status.State {
	case "running":
		return paint("●", green) + paint(" 运行中", muted)
	case "succeeded":
		return paint("✓ 已完成", muted)
	case "failed":
		return paint("× 失败", red)
	case "observed":
		return paint("○ 未登记", yellow)
	default:
		return paint("○ "+strings.ToUpper(core.Clean(r.Status.State)), muted)
	}
}

// surface 为整行设置背景，嵌套的前景色 reset 不打断选中背景。
func surface(text string, width int, color string) string {
	var r, g, b int
	fmt.Sscanf(color, "#%02x%02x%02x", &r, &g, &b)
	seq := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
	text = fit(text, width)
	text = strings.ReplaceAll(text, "\x1b[m", "\x1b[0m")
	text = strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+seq)
	text = strings.ReplaceAll(text, "\x1b[49m", seq)
	return seq + text + "\x1b[0m"
}
func (m Model) Render() string {
	w, h := max(20, m.Width), max(6, m.Height)
	if w < 55 || h < 16 {
		return m.frame([]string{"", bold("  G-Desk", fg), "", paint("  窗口过小，请扩大至 55 × 16", muted), "  q 退出"}, w, h)
	}
	// 随终端展开，仅保留固定侧边距。
	width := w - 8
	margin := (w - width) / 2
	pad := strings.Repeat(" ", margin)
	online, total, free, unknown := 0, 0, 0, 0
	for _, host := range m.Hosts {
		if m.hostGood(host) {
			online++
			total += len(host.Snapshot.GPUs)
			available := core.GPUAvailability(host, m.Now)
			free += len(available.Free)
			unknown += len(available.Unknown)
		}
	}
	viewName := "GPU 总览"
	if !m.gpuView {
		viewName = "任务列表"
	}
	brand := bold("G-Desk", purple) + paint("  /  "+viewName, muted)
	if m.Demo {
		brand += paint("  [DEMO]", yellow)
	}
	sortLabel := "卡号 ↑"
	if !m.gpuView {
		sortLabel = "默认顺序"
	}
	if m.latestFirst {
		sortLabel = "最近启动 ↓"
	}
	summary := paint(fmt.Sprintf("%d/%d 在线   空闲 %d/%d GPU", online, len(m.Hosts), free, total), muted)
	if unknown > 0 {
		summary += paint(fmt.Sprintf(" · %d 张未知", unknown), yellow)
	}
	sortHint := paint("s  "+sortLabel, muted)
	lines := []string{"", pad + pair(brand, summary, width), "", pad + pair(m.hostTabs(max(20, width-22)), sortHint, width), pad + rule(width), ""}

	available := h - 11
	var body []string
	if m.editing == "host" {
		body = []string{bold("  添加服务器", fg), "", paint("  输入 SSH 别名，不需要输入 ssh。", muted), paint("  例如 atlas-gpu 或 nebula-gpu", muted)}
		if m.notice != "" {
			body = append(body, paint("  "+core.Clean(m.notice), yellow))
		}
	} else if m.panel != "" {
		body = m.panelLines(width - 4)
		offset := max(0, min(m.panelOffset, max(0, len(body)-available)))
		body = body[offset:min(len(body), offset+available)]
		for i := range body {
			body[i] = "  " + body[i]
		}
	} else {
		body = m.mainLines(width, available)
	}
	for i := 0; i < available; i++ {
		text := ""
		if i < len(body) {
			text = body[i]
		}
		lines = append(lines, pad+fit(text, width))
	}
	input := paint(" /  ", cyan) + paint("搜索任务、项目、参数…", muted)
	context := "全部任务"
	if m.gpuView {
		context = "↑↓ 滚动查看"
	}
	if m.mine {
		context = "仅我的任务"
	}
	if m.query != "" {
		input = paint(" /  ", cyan) + core.Clean(m.query)
		context = "Esc 清除"
	}
	input = pair(input, paint(context+"  ", muted), width)
	hints := pair(paint("↑↓ 选择  tab 视图  s 排序  ←→ 主机  enter 详情  a 添加  x 移除", muted), paint("? 帮助  q 退出", muted), width)
	if m.editing != "" {
		label := map[string]string{"search": "搜索", "name": "任务名称", "description": "任务目的", "host": "SSH 别名"}[m.editing]
		input = paint(" ›  ", cyan) + core.Clean(m.input) + paint("▏", cyan)
		hints = paint(label+"   enter 确认   ctrl+u 清空   esc 取消", muted)
		if m.notice != "" {
			hints = paint(core.Clean(m.notice), yellow)
		}
	} else if m.panel != "" {
		input = paint("  "+map[string]string{"help": "快捷键", "details": "任务详情", "log": "日志预览", "remove_host": "移除服务器"}[m.panel], muted)
		hints = paint("↑↓ / PgUp PgDn 滚动   r 重读日志   ← / esc 返回", muted)
		if m.panel == "details" {
			hints = paint("↑↓ 滚动   e 编辑名称/说明   l 日志   ← / esc 返回", muted)
		}
		if m.panel == "remove_host" {
			hints = paint("enter 确认移除   ← / esc 取消", muted)
		}
		if m.notice != "" {
			input = paint("  "+core.Clean(m.notice), yellow)
		}
	} else if m.notice != "" {
		input = paint("  "+core.Clean(m.notice), yellow)
		hints = paint("esc 清除提示", muted)
	}
	if m.editing != "" {
		input = surface(input, width, selectedBG)
	}
	lines = append(lines, "", pad+input, "", pad+hints, "")
	return m.frame(lines, w, h)
}
func (m Model) frame(lines []string, w, h int) string {
	out := []string{}
	for _, s := range lines[:min(len(lines), h)] {
		out = append(out, surface(s, w, bg))
	}
	for len(out) < h {
		out = append(out, surface("", w, bg))
	}
	return strings.Join(out, "\n")
}
func (m Model) availabilityLabel(h core.Host, detail bool) string {
	available := core.GPUAvailability(h, m.Now)
	if !available.Known {
		return paint("空闲未知", yellow)
	}
	text := fmt.Sprintf("空闲 %d", len(available.Free))
	if detail {
		text += fmt.Sprintf("/%d", len(h.Snapshot.GPUs))
		if len(available.Free) > 0 {
			ids := []string{}
			for _, id := range available.Free {
				ids = append(ids, fmt.Sprint(id))
			}
			text += " · 卡 " + strings.Join(ids, ",")
		}
	}
	if len(available.Unknown) > 0 {
		text += fmt.Sprintf(" · 未知 %d", len(available.Unknown))
	}
	color := muted
	if len(available.Free) > 0 {
		color = green
	}
	return paint(text, color)
}
func (m Model) hostTabs(width int) string {
	all := paint(" 全部 ", muted)
	if m.host < 0 {
		all = surface(bold(" 全部 ", fg), 6, selectedBG)
	}
	tabs := []string{all}
	for i, h := range m.Hosts {
		label := core.Clean(h.Config.ID) + " " + m.availabilityLabel(h, false)
		if i == m.host {
			label = surface(" "+bold(core.Clean(h.Config.ID), fg)+" "+m.availabilityLabel(h, false)+" ", ansi.StringWidth(label)+2, selectedBG)
		}
		tabs = append(tabs, label)
	}
	if width < 70 {
		if m.host >= 0 {
			return fit(core.Clean(m.Hosts[m.host].Config.ID)+"  "+m.availabilityLabel(m.Hosts[m.host], true), width)
		}
		return paint("全部服务器   ←→ 选择主机查看空闲卡", muted)
	}
	start := 0
	selected := m.host + 1
	for start < selected && ansi.StringWidth(strings.Join(tabs[start:selected+1], "   ")) > width-2 {
		start++
	}
	prefix := ""
	if start > 0 {
		prefix = paint("… ", muted)
	}
	return fit(prefix+strings.Join(tabs[start:], "   "), width)
}
func (m Model) hostHeading(h core.Host, width int) string {
	return pair("  "+bold(core.Clean(h.Config.ID), fg), m.availabilityLabel(h, true)+"  ", width)
}
func (m Model) mainLines(width, height int) []string {
	// 连接与元信息错误始终占有可见位置，即使列表正好填满。
	errors := []string{}
	for hi, h := range m.Hosts {
		if m.host >= 0 && hi != m.host {
			continue
		}
		problem := h.Error
		if problem == "" && len(h.Snapshot.Errors) > 0 {
			problem = strings.Join(h.Snapshot.Errors, "; ")
		}
		if problem != "" {
			errors = append(errors, "  "+paint("○ "+core.Clean(h.Config.ID)+"  ", yellow)+paint(core.Clean(problem), muted))
		}
	}
	listHeight := height
	if len(errors) > 0 && height > 1 {
		listHeight--
	}
	items := m.items()
	m.selection(items)
	dense := m.gpuView && m.host < 0 && width >= 70
	compact := height < 10 || dense
	itemLines := func(it item, selected bool) []string {
		if m.gpuView {
			if it.gpu != nil {
				if dense {
					return []string{m.compactGPULine(it, width, selected)}
				}
				return m.gpuLines(it, width, selected)
			}
			return m.taskLines(it, width, selected)[:1]
		}
		return m.taskLines(it, width, selected)
	}
	// 滚动始终保留选中项及其主机标题。
	start := 0
	for start < m.index {
		used, last := 0, ""
		for i := start; i <= m.index; i++ {
			it := items[i]
			if it.host.Config.ID != last {
				used++
				if !compact || (dense && last != "") {
					used++
				}
				last = it.host.Config.ID
			}
			used += len(itemLines(it, i == m.index))
		}
		if used <= listHeight {
			break
		}
		start++
	}
	out := []string{}
	last := ""
	for i := start; i < len(items); i++ {
		it := items[i]
		header := []string{}
		if last != it.host.Config.ID {
			if dense && last != "" {
				header = append(header, "")
			}
			header = append(header, m.hostHeading(it.host, width))
			if !compact {
				header = append(header, "")
			}
		}
		content := itemLines(it, i == m.index)
		if len(out)+len(header)+len(content) > listHeight {
			break
		}
		out = append(out, header...)
		out = append(out, content...)
		last = it.host.Config.ID
	}
	if len(items) == 0 {
		out = []string{"", paint("  没有匹配的任务", fg), paint("  等待采集，或按 tab 查看 GPU。", muted), ""}
		if len(m.Hosts) == 0 {
			out = []string{"", paint("  尚未添加服务器", fg), paint("  按 a 输入 SSH 别名，例如 atlas-gpu。", muted)}
		}
	}
	if !m.gpuView && m.query == "" && !m.mine && len(m.Hosts) > 0 {
		if len(items) == 0 {
			out = []string{}
		}
		withTasks := map[string]bool{}
		for _, it := range items {
			withTasks[it.host.Config.ID] = true
		}
		for hi, h := range m.Hosts {
			if (m.host >= 0 && hi != m.host) || withTasks[h.Config.ID] {
				continue
			}
			if len(out)+2 <= listHeight {
				out = append(out, m.hostHeading(h, width), "")
			}
		}
	}
	if len(errors) > 0 {
		if len(out) < height {
			out = append(out, errors[0])
		} else if len(out) > 0 {
			out[len(out)-1] = errors[0]
		}
	}
	return out
}
func (m Model) taskLines(it item, width int, selected bool) []string {
	r := *it.row
	inner := width - 4
	gpuLabel := paint("GPU "+core.GPUName(r.GPUs), muted)
	titleWidth := max(1, inner-ansi.StringWidth(gpuLabel)-4)
	heading := pair(m.stateSymbol(r)+" "+bold(shortName(r.Task.Name, min(72, titleWidth)), fg), gpuLabel, inner)
	progress := m.shortProgress(r, it.host)
	stage := ""
	if p := r.Status.Progress; p != nil {
		if p.Total != nil && *p.Total > 0 {
			stage = bar(p.Completed / *p.Total, 10) + "  "
		}
		stage += paint(core.Clean(r.Status.Stage), muted)
	}
	rows := []string{heading}
	if progress != "" || r.Owner != "" {
		rows = append(rows, gpuColumns(stage, core.Clean(r.Owner), progress, inner))
	}
	for i, s := range rows {
		marker := "  "
		if selected {
			marker = paint("▎", purple) + " "
		}
		rows[i] = marker + fit(s, inner) + "  "
		if selected {
			rows[i] = surface(rows[i], width, selectedBG)
		}
	}
	return append(rows, "")
}

// stateSymbol 仅用于总览，完整状态与原因保留在详情中。
func (m Model) stateSymbol(r core.Row) string {
	if r.Stale {
		return paint("◌", yellow)
	}
	if r.Warning != "" {
		return paint("△", yellow)
	}
	switch r.Status.State {
	case "running":
		return paint("●", green)
	case "observed":
		return paint("○", muted)
	case "succeeded":
		return paint("✓", muted)
	case "failed":
		return paint("×", red)
	default:
		return paint("·", muted)
	}
}

func (m Model) shortProgress(r core.Row, h core.Host) string {
	if p := r.Status.Progress; p != nil {
		if p.Total != nil && *p.Total > 0 {
			return fmt.Sprintf("%.0f%%", 100*p.Completed / *p.Total)
		}
		return fmt.Sprintf("%.0f %s", p.Completed, core.Clean(p.Unit))
	}
	text := core.RuntimeText(r, h, m.Now)
	text = strings.TrimPrefix(strings.TrimPrefix(text, "已运行 "), "运行耗时 ")
	text = strings.TrimSuffix(text, " · 上次观测")
	return strings.NewReplacer(" 小时 ", "h", " 分钟", "m", " 天 ", "d", " 小时", "h", "不足 1m", "<1m").Replace(text)
}

func (m Model) gpuStatus(it item) (string, string, string) {
	users, progress := []string{}, ""
	seen := map[string]bool{}
	symbol := paint("·", muted)
	if !m.hostGood(it.host) {
		symbol = paint("◌", yellow)
	}
	for _, r := range it.tasks {
		if r.Owner != "" && !seen[r.Owner] {
			users = append(users, core.Clean(r.Owner))
			seen[r.Owner] = true
		}
	}
	if len(it.tasks) > 0 {
		symbol = m.stateSymbol(it.tasks[0])
		for _, r := range it.tasks {
			if r.Stale || r.Warning != "" || r.Status.State == "failed" {
				symbol = m.stateSymbol(r)
				break
			}
		}
		if len(it.tasks) == 1 {
			progress = m.shortProgress(it.tasks[0], it.host)
		}
	}
	return symbol, strings.Join(users, ","), progress
}

func (m Model) gpuTitle(it item, width int) string {
	title := gpuTaskTitle(it.tasks, width)
	if title == "" {
		title = "—"
		if available := core.GPUAvailability(it.host, m.Now); available.Known {
			for _, index := range available.Free {
				if index == it.gpu.Index {
					title = "空闲"
					break
				}
			}
		}
	}
	return title
}

func (m Model) gpuLines(it item, width int, selected bool) []string {
	g := *it.gpu
	inner := width - 4
	memoryBar := paint("????????????", muted)
	if g.Used != nil && g.Total != nil && *g.Total > 0 && *g.Used >= 0 && *g.Used <= *g.Total {
		memoryBar = bar(*g.Used / *g.Total, 12)
	}
	symbol, owner, progress := m.gpuStatus(it)
	// 标题与资源分行；用户和进度/时长使用固定尾列。
	identity := paint(fmt.Sprintf("GPU %-2d", g.Index), muted) + "  " + symbol + " "
	titleWidth := min(72, max(1, inner-ansi.StringWidth(identity)))
	title := m.gpuTitle(it, titleWidth)
	heading := identity + bold(title, fg)
	resources := paint("VRAM ", muted) + memoryBar + "  " + fit(paint(memory(g), fg), 11) + "   " + paint("U "+util(g), fg)
	stats := gpuColumns("          "+resources, owner, progress, inner)
	if inner < 90 {
		// 小终端将相同的固定尾列放到标题行，资源行保持完整。
		titleWidth = max(1, inner-20-ansi.StringWidth(identity))
		heading = gpuColumns(identity+bold(gpuTaskTitle(it.tasks, titleWidth), fg), owner, progress, inner)
		if len(it.tasks) == 0 {
			heading = identity + paint(title, muted)
		}
		stats = "  " + paint("VRAM ", muted) + barMemoryCompact(g) + " " + fit(paint(memory(g), fg), 11) + "   " + paint("U "+util(g), fg)
	}
	marker := "  "
	if selected {
		marker = paint("▎", purple) + " "
	}
	rows := []string{heading, stats}
	if len(it.tasks) == 0 && inner >= 90 {
		rows = []string{fit(heading, 23) + resources}
	}
	for i, row := range rows {
		rows[i] = marker + fit(row, inner) + "  "
		if selected {
			rows[i] = surface(rows[i], width, selectedBG)
		}
	}
	return append(rows, "")
}

// 全部服务器的每张卡只占一行；列宽与任务名长度、缺失字段无关。
func (m Model) compactGPULine(it item, width int, selected bool) string {
	inner := width - 4
	barWidth, ownerWidth, progressWidth, gap := 6, 12, 12, "  "
	if inner < 100 {
		barWidth, ownerWidth, progressWidth, gap = 4, 8, 8, " "
	}
	memoryWidth, utilizationWidth := barWidth+1+11, 6
	nameWidth := inner - memoryWidth - utilizationWidth - ownerWidth - progressWidth - 4*len(gap)
	symbol, owner, progress := m.gpuStatus(it)
	identity := paint(fmt.Sprintf("GPU %-2d", it.gpu.Index), muted) + " " + symbol + " "
	title := identity + bold(m.gpuTitle(it, max(1, nameWidth-ansi.StringWidth(identity))), fg)
	g := *it.gpu
	memoryBar := paint(strings.Repeat("?", barWidth), muted)
	if g.Used != nil && g.Total != nil && *g.Total > 0 && *g.Used >= 0 && *g.Used <= *g.Total {
		memoryBar = bar(*g.Used / *g.Total, barWidth)
	}
	resource := memoryBar + " " + fit(paint(memory(g), fg), 11)
	row := fit(title, nameWidth) + gap + resource + gap +
		fit(paint("U "+util(g), fg), utilizationWidth) + gap +
		fit(paint(owner, purple), ownerWidth) + gap +
		fitRight(paint(progress, cyan), progressWidth)
	marker := "  "
	if selected {
		marker = paint("▎", purple) + " "
	}
	row = marker + fit(row, inner) + "  "
	if selected {
		return surface(row, width, selectedBG)
	}
	return row
}

func barMemoryCompact(g core.GPU) string {
	if g.Used == nil || g.Total == nil || *g.Total <= 0 || *g.Used < 0 || *g.Used > *g.Total {
		return paint("????", muted)
	}
	return bar(*g.Used / *g.Total, 4)
}
func (m Model) panelLines(width int) []string {
	if m.panel == "remove_host" {
		return []string{bold("移除服务器 "+core.Clean(m.removeHostID)+"？", fg), "", "仅从本地监控列表移除，服务器上的任务继续运行。", "以后可以按 a 再次添加。"}
	}

	if m.panel == "help" {
		return []string{
			bold("快捷键", cyan), "",
			"↑↓ / j k    选择", "←→           切换服务器", "Tab          GPU / 任务视图",
			"s            卡号 / 最近启动排序", "/            搜索", "m            只看我的任务",
			"Enter        查看详情", "e            改名，再编辑说明", "a / x        添加 / 移除服务器",
			"l / r        日志 / 刷新", "← / Esc      返回", "q / Ctrl+C   退出", "",
			bold("状态", cyan), "● 运行   ○ 自动发现   △ 待核实   ◌ 过期   ✓ 完成   × 失败", "",
			"显存条：显存占用比例。U：GPU 利用率。", "有进度时显示进度，否则显示运行时长。",
			"空闲候选：观测新鲜、无计算进程、显存 ≤512 MiB、利用率 ≤5%。",
		}
	}

	if m.panel == "log" {
		lines := []string{bold("日志预览 · 只读快照", cyan), paint(core.Clean(m.logTitle), muted), ""}
		for _, s := range strings.Split(m.logText, "\n") {
			lines = append(lines, core.Clean(s))
		}
		return wrapLines(lines, width)
	}
	it, ok := m.current()
	if !ok {
		return []string{"没有选中项"}
	}
	lines := []string{bold("任务详情", cyan), ""}
	tasks := it.tasks
	if it.row != nil {
		tasks = []core.Row{*it.row}
	}
	if it.gpu != nil {
		g := it.gpu
		lines = append(lines, fmt.Sprintf("GPU %d · %s · %s", g.Index, core.Clean(g.Name), core.Clean(g.UUID)), "显存 "+memory(*g)+" · 卡利用率 "+util(*g), "")
	}
	for _, r := range tasks {
		if r.Owner != "" {
			lines = append(lines, "运行用户   "+core.Clean(r.Owner))
		}
		lines = append(lines, bold(core.Clean(r.Task.Name), fg), "目的       "+core.Clean(r.Task.Description), "项目       "+core.Clean(r.Task.Project), "状态       "+ansi.Strip(m.state(r))+" · 原始记录 "+core.Clean(r.Status.State), "任务 ID    "+core.Clean(r.Task.ID), "执行 ID    "+core.Clean(r.Status.AttemptID), "阶段       "+core.Clean(r.Status.Stage), "当前活动   "+core.Clean(r.Status.Message))
		if p := r.Status.Progress; p != nil {
			lines = append(lines, "进度       "+core.ProgressText(p), "计数范围   "+core.Clean(p.Scope)+" · 最近进展 "+core.Age(p.UpdatedAt, m.Now))
		} else if runtime := core.RuntimeText(r, it.host, m.Now); runtime != "" {
			lines = append(lines, runtime)
		}
		lines = append(lines, "元信息     "+core.Age(r.Status.UpdatedAt, m.Now)+" 前更新 · "+core.Clean(r.Source), "观测       "+core.Age(it.host.ReceivedAt, m.Now)+" 前收到 · "+core.Clean(it.host.Config.ID))
		if r.Warning != "" {
			lines = append(lines, paint("提示       "+core.Clean(r.Warning), yellow))
		}
		for _, group := range []struct {
			name   string
			values map[string]any
		}{{"参数", r.Task.Parameters}, {"指标", r.Status.Metrics}, {"复现信息", r.Task.Provenance}} {
			lines = append(lines, "", bold(group.name, purple))
			keys := []string{}
			for k := range group.values {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				value := core.Value(group.values[k])
				if _, ok := group.values[k].([]any); ok {
					b, _ := json.Marshal(group.values[k])
					value = core.Clean(string(b))
				}
				lines = append(lines, "  "+core.Clean(k)+" = "+value)
			}
		}
		lines = append(lines, "", bold("文件入口", purple))
		for _, a := range append(append([]core.Artifact{}, r.Task.Artifacts...), r.Status.Artifacts...) {
			lines = append(lines, "  "+core.Clean(a.Role)+"  "+core.Clean(a.Path))
		}
		lines = append(lines, "", bold("实时关联进程", purple))
		for _, p := range r.Processes {
			lines = append(lines, fmt.Sprintf("  PID %d · %s · ticks %s", p.PID, core.Clean(p.User), core.Clean(p.StartTicks)), "  "+core.Clean(p.Command), "  cwd: "+core.Clean(p.Cwd))
			if p.Error != "" {
				lines = append(lines, paint("  "+core.Clean(p.Error), yellow))
			}
		}
		lines = append(lines, "", rule(width), "")
	}
	return wrapLines(lines, width)
}
func wrapLines(lines []string, width int) []string {
	out := []string{}
	for _, s := range lines {
		out = append(out, strings.Split(ansi.Hardwrap(s, max(1, width), true), "\n")...)
	}
	return out
}
