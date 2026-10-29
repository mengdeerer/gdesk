package core

import (
	"fmt"
	"time"
)

// Demo 独立构造演示快照，绝不作为真实连接失败后的回退。
func Demo(now time.Time) []Host {
	hosts := []Host{}
	for _, name := range []string{"atlas-gpu", "orion-gpu", "nova-gpu"} {
		h := Host{Config: HostConfig{ID: name, SSH: name, User: "alice"}, ReceivedAt: now, Snapshot: Snapshot{HostID: name, User: "alice", ObservedAt: now, GPUOK: true, ProcessesOK: true}}
		n, total, model := 8, 32768.0, "NVIDIA RTX 5090"
		if name == "orion-gpu" {
			total = 24576
			model = "NVIDIA RTX 4090"
		}
		if name == "nova-gpu" {
			n = 4
			total = 81920
			model = "NVIDIA A100"
		}
		for i := 0; i < n; i++ {
			h.Snapshot.GPUs = append(h.Snapshot.GPUs, GPU{UUID: fmt.Sprintf("%s-gpu-%d", name, i), Index: i, Name: model, Util: Number(0), Used: Number(12), Total: Number(total), Temperature: Number(35)})
		}
		hosts = append(hosts, h)
	}
	add := func(hi int, id, name, project, desc, stage string, params map[string]any, fields []string, curr, total float64, unit string, indices []int, util, mem float64, user string) {
		h := &hosts[hi]
		p := Process{Identity: Identity{PID: 42000 + len(h.Snapshot.Processes), BootID: "demo-boot", StartTicks: fmt.Sprint(100 + len(h.Snapshot.Processes))}, User: user, Command: "python train.py --config configs/experiment.yaml", Cwd: "/work/" + project, Memory: map[string]*float64{}}
		for _, i := range indices {
			g := &h.Snapshot.GPUs[i]
			g.Util = Number(util)
			g.Used = Number(mem)
			g.Temperature = Number(62)
			p.GPUs = append(p.GPUs, g.UUID)
			p.Memory[g.UUID] = Number(mem - 12)
		}
		t := Task{SchemaVersion: 1, ID: id, Name: name, Project: project, Description: desc, Kind: "eval", Parameters: params, Display: Display{SummaryFields: fields}, Artifacts: []Artifact{{Role: "config", Path: "/work/" + project + "/runs/" + id + "/config.yaml"}, {Role: "output", Path: "/work/" + project + "/runs/" + id}}, Provenance: map[string]any{"git_commit": "a13c9e7", "config_snapshot": "config.yaml"}}
		s := Status{SchemaVersion: 1, TaskID: id, AttemptID: "attempt-02", State: "running", Stage: stage, Processes: []Identity{p.Identity}, StartedAt: now.Add(-3*time.Hour - 12*time.Minute), UpdatedAt: now.Add(-4 * time.Second), Progress: &Progress{Completed: curr, Total: Number(total), Unit: unit, Scope: "stage", UpdatedAt: now.Add(-18 * time.Second)}, Metrics: map[string]any{}, Artifacts: []Artifact{{Role: "log", Path: "/work/" + project + "/runs/" + id + "/run.log"}}}
		h.Snapshot.Processes = append(h.Snapshot.Processes, p)
		h.Snapshot.Records = append(h.Snapshot.Records, Record{Task: t, Status: s, Source: "standard v1"})
	}
	add(0, "01-resnet", "ResNet50 · 学习率对比", "image-classification", "比较较小学习率是否改善验证集精度", "训练", map[string]any{"model": "resnet50", "dataset": "ImageNet", "lr": 0.0001}, []string{"parameters.model", "parameters.dataset", "parameters.lr"}, 32, 100, "epochs", []int{0, 1}, 92, 21400, "alice")
	hosts[0].Snapshot.Records[0].Status.Metrics = map[string]any{"loss": 1.82}
	add(0, "02-vision", "VisionBench · 验证集评测", "vision-eval", "比较两个虚构实验的验证集精度，所有指标均为演示样本", "评测", map[string]any{"method": "demo-baseline", "budget": map[string]any{"value": 20, "unit": "tok/frame"}, "fps": 0.5}, []string{"parameters.method", "parameters.budget", "parameters.fps"}, 750, 1000, "questions", []int{4}, 18, 19360, "alice")
	add(1, "01-corpus", "视频语料 · 清洗与索引", "video-corpus", "剔除损坏视频，生成统一格式的训练索引", "清洗", map[string]any{"source": "batch-03", "workers": 16}, []string{"parameters.source", "parameters.workers"}, 8120, 24000, "files", []int{2}, 67, 10240, "alice")
	add(1, "02-llm", "Qwen3 · 推理吞吐测试", "serving-bench", "测量不同并发数下的吞吐与首 token 延迟", "benchmark", map[string]any{"model": "Qwen3-8B", "concurrency": 32, "precision": "bf16"}, []string{"parameters.model", "parameters.concurrency", "parameters.precision"}, 640, 1000, "requests", []int{5, 6}, 96, 22000, "bob")
	hosts[1].Snapshot.Records[1].Status.Metrics = map[string]any{"throughput": map[string]any{"value": 186, "unit": "tok/s"}}
	hosts[2].Error = "连接超时 · 演示断线状态"
	hosts[2].ReceivedAt = now.Add(-47 * time.Second)
	hosts[2].Snapshot.ObservedAt = hosts[2].ReceivedAt
	return hosts
}
