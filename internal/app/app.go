package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mengdeerer/gdesk/internal/collect"
	"github.com/mengdeerer/gdesk/internal/core"
	"github.com/mengdeerer/gdesk/internal/ui"
)

// Version 可在构建时用 -ldflags 注入；go install 使用模块构建信息。
var Version = "dev"

// Run 将参数解析与进程退出分开，供两个命令入口共用。
func Run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("gdesk", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, "G-Desk · GPU 看板\n\n用法: gdesk [选项]\n首次启动输入 SSH 别名；? 查看快捷键。\n\n")
		flags.PrintDefaults()
	}
	demo := flags.Bool("demo", false, "使用虚拟数据演示")
	config := flags.String("config", "", "配置路径（默认 $XDG_CONFIG_HOME/gdesk/config.json 或 ~/.config/gdesk/config.json）")
	snapshot := flags.Bool("snapshot", false, "输出一帧演示界面 ANSI（需 --demo）")
	width := flags.Int("width", 124, "快照列数")
	height := flags.Int("height", 38, "快照行数")
	gpu := flags.Bool("gpus", true, "以 GPU 与任务合并总览启动；false 使用任务列表")
	check := flags.Bool("check", false, "采集一次并输出诊断")
	version := flags.Bool("version", false, "打印版本并退出")
	configPath := flags.Bool("config-path", false, "打印配置路径并退出")
	importPath := flags.String("import-config", "", "导入配置及标注（目标目录须不存在）")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("未知参数 %q；用 gdesk --help 查看用法", flags.Arg(0))
	}
	if *version {
		fmt.Fprintln(stdout, "G-Desk", versionString())
		return nil
	}
	if *config == "" && (!*demo || *configPath || *importPath != "") {
		path, err := defaultConfigPath()
		if err != nil {
			return err
		}
		*config = path
	}
	if *configPath {
		fmt.Fprintln(stdout, *config)
		return nil
	}
	if *importPath != "" {
		if *demo || *check || *snapshot {
			return fmt.Errorf("--import-config 不与 --demo、--check 或 --snapshot 混用")
		}
		if err := importConfig(*importPath, *config); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "已导入配置与已有标注:", *config)
		return nil
	}
	if *check && *demo {
		return fmt.Errorf("--check 仅用于真实服务器，不与 --demo 混用")
	}
	if *snapshot && !*demo {
		return fmt.Errorf("--snapshot 需与 --demo 一起使用")
	}
	if *width < 20 || *width > 500 || *height < 6 || *height > 200 {
		return fmt.Errorf("快照宽度范围 20–500，高度范围 6–200")
	}
	c := core.Config{Interval: 5, Timeout: 12}
	var client *collect.Client
	var err error
	if !*demo {
		if c, err = core.LoadConfig(*config); err != nil {
			if !os.IsNotExist(err) || *check {
				return fmt.Errorf("配置读取失败（%s）: %w；可直接运行 gdesk 添加服务器", *config, err)
			}
		}
		client, err = collect.New()
		if err != nil {
			return err
		}
		defer client.Close(c.Hosts)
	}
	if *check {
		if len(c.Hosts) == 0 {
			return fmt.Errorf("尚未添加服务器，请直接启动程序并输入 SSH 别名")
		}
		type result struct {
			Host      string   `json:"host"`
			GPUs      int      `json:"gpus"`
			Processes int      `json:"processes"`
			Tasks     int      `json:"registered_tasks"`
			Errors    []string `json:"errors,omitempty"`
		}
		results := make([]result, len(c.Hosts))
		var wg sync.WaitGroup
		for i, h := range c.Hosts {
			wg.Add(1)
			go func(i int, h core.HostConfig) {
				defer wg.Done()
				s, e := client.Poll(h, time.Duration(c.Timeout)*time.Second)
				r := result{Host: h.ID, GPUs: len(s.GPUs), Processes: len(s.Processes), Tasks: len(s.Records), Errors: s.Errors}
				if e != nil {
					r.Errors = append(r.Errors, e.Error())
				}
				results[i] = r
			}(i, h)
		}
		wg.Wait()
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err = encoder.Encode(results); err != nil {
			return err
		}
		for _, r := range results {
			if len(r.Errors) > 0 {
				return fmt.Errorf("部分采集失败，详情见上方诊断")
			}
		}
		return nil
	}
	annotation := ""
	if !*demo {
		annotation = filepath.Join(filepath.Dir(*config), "annotations.json")
	}
	m, err := ui.New(c, client, *demo, annotation)
	if err != nil {
		return err
	}
	m.Width = *width
	m.Height = *height
	m.ConfigPath = *config
	m.SetGPUView(*gpu)
	if *snapshot {
		fmt.Fprintln(stdout, m.Render())
		return nil
	}
	if !*demo {
		if err := os.MkdirAll(filepath.Dir(*config), 0700); err != nil {
			return fmt.Errorf("创建配置目录失败: %w", err)
		}
	}
	_, err = tea.NewProgram(m, tea.WithOutput(stdout)).Run()
	return err
}
func Main() {
	if err := Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "gdesk:", err)
		os.Exit(1)
	}
}
