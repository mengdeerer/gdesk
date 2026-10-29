package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	path, err := defaultConfigPath()
	if err != nil || path != filepath.Join(home, ".config", "gdesk", "config.json") {
		t.Fatalf("默认路径: %q, %v", path, err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "custom"))
	path, err = defaultConfigPath()
	if err != nil || path != filepath.Join(home, "custom", "gdesk", "config.json") {
		t.Fatalf("XDG 路径: %q, %v", path, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := defaultConfigPath(); err == nil {
		t.Fatal("相对 XDG 路径可能随工作目录改变，不应接受")
	}
}

func TestCLI(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	for _, tc := range []struct {
		name string
		args []string
		want string
		fail bool
	}{
		{"帮助", []string{"--help"}, "用法: gdesk", false},
		{"版本", []string{"--version"}, "G-Desk", false},
		{"默认路径", []string{"--config-path"}, filepath.Join(base, "gdesk", "config.json"), false},
		{"指定路径", []string{"--config", "/tmp/custom.json", "--config-path"}, "/tmp/custom.json", false},
		{"未知命令", []string{"not-a-command"}, "未知参数", true},
		{"无配置诊断", []string{"--check"}, "配置读取失败", true},
		{"冲突参数", []string{"--demo", "--check"}, "不与", true},
		{"非法快照", []string{"--snapshot"}, "需与", true},
		{"宽度越界", []string{"--demo", "--width", "1"}, "范围", true},
		{"演示快照", []string{"--demo", "--snapshot"}, "DEMO", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := Run(tc.args, &out, &out)
			if (err != nil) != tc.fail {
				t.Fatalf("错误: %v; 输出: %s", err, out.String())
			}
			text := out.String()
			if err != nil {
				text += err.Error()
			}
			if !strings.Contains(text, tc.want) {
				t.Fatalf("缺少 %q: %s", tc.want, text)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(base, "gdesk")); !os.IsNotExist(err) {
		t.Fatal("帮助、诊断、演示不应创建真实配置目录")
	}
}

func TestImportConfig(t *testing.T) {
	for _, notes := range []string{"", `{"task:123":{"name":"实验新名称","description":"备注"}}`, `{bad json`} {
		t.Run(notes, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "legacy.json")
			data := []byte(`{"interval_seconds":5,"timeout_seconds":12,"hosts":[],"extension":{"keep":true}}`)
			if err := os.WriteFile(source, data, 0600); err != nil {
				t.Fatal(err)
			}
			if notes != "" {
				if err := os.WriteFile(filepath.Join(root, "annotations.json"), []byte(notes), 0600); err != nil {
					t.Fatal(err)
				}
			}
			dest := filepath.Join(root, "user", "gdesk", "config.json")
			err := importConfig(source, dest)
			if notes == `{bad json` {
				if err == nil {
					t.Fatal("不能导入损坏标注")
				}
				if _, err := os.Stat(filepath.Dir(dest)); !os.IsNotExist(err) {
					t.Fatal("失败不应留下部分配置")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(dest)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("配置内容变化: %s %v", got, err)
			}
			original, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(original, data) {
				t.Fatal("原文件变化")
			}
			got, err = os.ReadFile(filepath.Join(filepath.Dir(dest), "annotations.json"))
			if notes == "" && !os.IsNotExist(err) {
				t.Fatal("不应生成虚构标注")
			}
			if notes != "" && (err != nil || string(got) != notes) {
				t.Fatal("标注未保留")
			}
			if err := importConfig(source, dest); err == nil {
				t.Fatal("重复导入应拒绝覆盖")
			}
		})
	}
}
