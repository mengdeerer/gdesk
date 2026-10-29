package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

var sshAlias = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// AddHost 只添加一个 SSH 别名，原子保存且保留既有配置中的未知字段。
func AddHost(path string, current Config, input string) (Config, error) {
	alias := strings.TrimSpace(input)
	if !sshAlias.MatchString(alias) {
		return current, fmt.Errorf("只输入 SSH 别名，例如 atlas-gpu 或 nebula-gpu，不要包含 ssh、空格或参数")
	}
	if len(current.Hosts) >= 32 {
		return current, fmt.Errorf("最多添加 32 台服务器")
	}
	for _, host := range current.Hosts {
		if host.ID == alias || host.SSH == alias {
			return current, fmt.Errorf("服务器 %s 已添加", alias)
		}
	}
	host := HostConfig{ID: alias, SSH: alias, TaskDirs: []string{}}
	raw, original, exists, err := readConfigForEdit(path, current)
	if err != nil {
		return current, err
	}
	var hosts []json.RawMessage
	if len(raw["hosts"]) > 0 {
		if err = json.Unmarshal(raw["hosts"], &hosts); err != nil {
			return current, err
		}
	}
	entry, _ := json.Marshal(host)
	hosts = append(hosts, entry)
	raw["hosts"], _ = json.Marshal(hosts)
	if err = writeConfigEdit(path, raw, original, exists); err != nil {
		return current, err
	}
	next := current
	next.Hosts = append(append([]HostConfig{}, current.Hosts...), host)
	return next, nil
}

// RemoveHost 仅从本地监控配置移除，不操作远端进程或文件。
func RemoveHost(path string, current Config, id string) (Config, error) {
	index := -1
	for i, h := range current.Hosts {
		if h.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return current, fmt.Errorf("服务器 %s 不在配置中", id)
	}
	raw, original, exists, err := readConfigForEdit(path, current)
	if err != nil {
		return current, err
	}
	var hosts []json.RawMessage
	if err = json.Unmarshal(raw["hosts"], &hosts); err != nil {
		return current, err
	}
	remaining := make([]json.RawMessage, 0, len(hosts)-1)
	remaining = append(remaining, hosts[:index]...)
	remaining = append(remaining, hosts[index+1:]...)
	raw["hosts"], _ = json.Marshal(remaining)
	if err = writeConfigEdit(path, raw, original, exists); err != nil {
		return current, err
	}
	next := current
	next.Hosts = make([]HostConfig, 0, len(current.Hosts)-1)
	next.Hosts = append(next.Hosts, current.Hosts[:index]...)
	next.Hosts = append(next.Hosts, current.Hosts[index+1:]...)
	return next, nil
}

func readConfigForEdit(path string, current Config) (map[string]json.RawMessage, []byte, bool, error) {
	if path == "" {
		return nil, nil, false, fmt.Errorf("未指定保存服务器的配置路径")
	}
	original, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, false, err
	}
	var raw map[string]json.RawMessage
	if exists {
		disk, err := LoadConfig(path)
		if err != nil {
			return nil, nil, false, err
		}
		// 既有文件在界面打开后被修改时，不覆盖外部改动。
		if !reflect.DeepEqual(disk, current) {
			return nil, nil, false, fmt.Errorf("配置文件已被外部修改，请重启后再修改")
		}
		if err = json.Unmarshal(original, &raw); err != nil {
			return nil, nil, false, err
		}
	} else {
		if len(current.Hosts) > 0 {
			return nil, nil, false, fmt.Errorf("原配置文件已消失，未覆盖当前配置")
		}
		raw = map[string]json.RawMessage{}
		raw["interval_seconds"], _ = json.Marshal(current.Interval)
		raw["timeout_seconds"], _ = json.Marshal(current.Timeout)
	}
	return raw, original, exists, nil
}

func writeConfigEdit(path string, raw map[string]json.RawMessage, original []byte, exists bool) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".gpu-desk-config-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	latest, readErr := os.ReadFile(path)
	if (exists && (readErr != nil || !bytes.Equal(latest, original))) || (!exists && !os.IsNotExist(readErr)) {
		return fmt.Errorf("保存前配置发生变化，请重试")
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	return nil
}
