package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/mengdeerer/gdesk/internal/core"
)

func versionString() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func defaultConfigPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base != "" && !filepath.IsAbs(base) {
		return "", fmt.Errorf("XDG_CONFIG_HOME 必须是绝对路径")
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法定位用户配置目录，请用 --config 指定路径: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "gdesk", "config.json"), nil
}

// importConfig 先校验并完整暂存配置和标注，再一次性发布目录。
// 目标目录必须尚不存在；源文件保留，已有目标永不合并或覆盖。
func importConfig(source, destination string) error {
	if filepath.Base(destination) == "annotations.json" {
		return fmt.Errorf("配置文件不能与标注文件 annotations.json 同名")
	}
	if _, err := core.LoadConfig(source); err != nil {
		return fmt.Errorf("旧配置无效: %w", err)
	}
	config, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	annotation, err := os.ReadFile(filepath.Join(filepath.Dir(source), "annotations.json"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取旧标注失败: %w", err)
	}
	if err == nil {
		var entries map[string]core.Annotation
		if err := json.Unmarshal(annotation, &entries); err != nil {
			return fmt.Errorf("旧标注无效: %w", err)
		}
	}
	dir := filepath.Dir(destination)
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("目标目录已存在，未覆盖: %s；可用 --config 直接读取原配置", dir)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".gdesk-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, filepath.Base(destination)), config, 0600); err != nil {
		return err
	}
	if _, err := core.LoadConfig(filepath.Join(stage, filepath.Base(destination))); err != nil {
		return fmt.Errorf("导入期间配置变化或无效: %w", err)
	}
	if annotation != nil {
		if err := os.WriteFile(filepath.Join(stage, "annotations.json"), annotation, 0600); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, dir); err != nil {
		return fmt.Errorf("导入失败，源配置保留: %w", err)
	}
	return nil
}
