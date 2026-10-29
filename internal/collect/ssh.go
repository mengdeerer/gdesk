package collect

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/mengdeerer/gdesk/internal/core"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed probe.py
var probe string

type Client struct {
	socketDir string
	sem       chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
	closed    bool
	hosts     map[string]core.HostConfig
}

func New() (*Client, error) {
	// macOS 的 TMPDIR 很长，而 Unix socket 路径最多约 104 字节。
	base := os.TempDir()
	if len(base) > 32 {
		base = "/tmp"
	}
	d, e := os.MkdirTemp(base, "gds-")
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{socketDir: d, sem: make(chan struct{}, 4), ctx: ctx, cancel: cancel, hosts: map[string]core.HostConfig{}}, e
}
func (c *Client) Close(hosts []core.HostConfig) {
	c.mu.Lock()
	c.closed = true
	c.cancel()
	all := map[string]core.HostConfig{}
	for _, h := range hosts {
		all[h.SSH] = h
	}
	for key, h := range c.hosts {
		all[key] = h
	}
	c.mu.Unlock()
	c.wg.Wait()
	for _, h := range all {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = exec.CommandContext(ctx, "ssh", "-S", filepath.Join(c.socketDir, "%C"), "-O", "exit", h.SSH).Run()
		cancel()
	}
	_ = os.RemoveAll(c.socketDir)
}

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, fmt.Errorf("远端输出超过 %d 字节限制", b.max)
	}
	return b.Buffer.Write(p)
}
func (c *Client) run(h core.HostConfig, request any, timeout time.Duration) ([]byte, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, context.Canceled
	}
	c.wg.Add(1)
	c.hosts[h.SSH] = h
	c.mu.Unlock()
	defer c.wg.Done()
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	data, e := json.Marshal(request)
	if e != nil {
		return nil, e
	}
	code := probe + "\nmain(json.loads(base64.b64decode('" + base64.StdEncoding.EncodeToString(data) + "')))\n"
	args := []string{"-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=1", "-o", "ControlMaster=auto", "-o", "ControlPersist=30", "-o", "ControlPath=" + filepath.Join(c.socketDir, "%C"), h.SSH, "python3", "-"}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(code)
	stdout := &limitedBuffer{max: 4 << 20}
	stderr := &limitedBuffer{max: 16 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if e = cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("SSH 采集超时或已取消: %w", ctx.Err())
		}
		return nil, fmt.Errorf("SSH: %w · %s", e, core.Clean(stderr.String()))
	}
	return stdout.Bytes(), nil
}
func (c *Client) Poll(h core.HostConfig, timeout time.Duration) (core.Snapshot, error) {
	var s core.Snapshot
	b, e := c.run(h, map[string]any{"op": "snapshot", "task_dirs": h.TaskDirs}, timeout)
	if e != nil {
		return s, e
	}
	envelope := struct {
		*core.Snapshot
		Records []json.RawMessage `json:"records"`
	}{Snapshot: &s}
	if e = json.Unmarshal(b, &envelope); e != nil {
		return s, fmt.Errorf("探针 JSON 无效: %w", e)
	}
	for i, raw := range envelope.Records {
		var record core.Record
		if e = json.Unmarshal(raw, &record); e != nil {
			s.Errors = append(s.Errors, fmt.Sprintf("元信息记录 %d 类型无效: %v", i, e))
			continue
		}
		s.Records = append(s.Records, record)
	}
	if s.ObservedAt.IsZero() {
		return s, fmt.Errorf("探针未返回有效观测: %s", core.Clean(string(b)))
	}
	s.HostID = h.ID
	return s, nil
}
func (c *Client) Log(h core.HostConfig, path string, timeout time.Duration) (string, error) {
	b, e := c.run(h, map[string]any{"op": "log", "path": path, "task_dirs": h.TaskDirs}, timeout)
	if e != nil {
		return "", e
	}
	var r struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return "", e
	}
	if r.Error != "" {
		return "", fmt.Errorf("%s", r.Error)
	}
	return r.Text, nil
}
