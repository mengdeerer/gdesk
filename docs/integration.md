# 接入进度

GPU、用户和运行时长可自动显示。进度、指标和实验说明需要额外上报。

## 写入状态

将 [gpu_task.py](../integrations/gpu_task.py) 复制到训练或评测项目：

```python
from gpu_task import TaskWriter

writer = TaskWriter("/work/runs/demo-01", {
    "task_id": "demo-01",
    "name": "分类实验",
    "project": "demo",
    "description": "比较学习率",
    "kind": "train",
    "parameters": {"learning_rate": 0.0001},
})

# 在业务完成一步后上报实际进度。
writer.update(stage="train", completed=32, total=100, unit="epochs",
              metrics={"loss": 1.82})

# 长计算期间可单独更新心跳。
writer.update(message="保存 checkpoint")

# 业务确实结束后传入实际退出码。
# writer.finish(exit_code)
```

在对应服务器配置中加入任务目录：

```json
{"id": "atlas-gpu", "ssh": "atlas-gpu", "task_dirs": ["/work/runs/demo-01"]}
```

目录在远端，支持 `~/`，不递归扫描。每个目录包含 `task.json` 和 `status.json`。

## 上报约定

- 不知道总数时省略 `total`。计数使用实际完成量。
- 高频进度每 2–5 秒上报，长计算每 10–20 秒心跳，结束立即记录。
- 一个目录只由一个进程写入。分布式任务由控制器汇总进度，并用 `pids=worker_pids` 绑定实际 GPU worker。
- 同一实验重启生成新 attempt；实验配置改变时换用新 ID 和目录。

## 失败处理

`update()`、`finish()`、`flush()` 返回写入是否成功。默认模式隔离上报异常，不中断训练；不要对返回值使用 `assert`。`strict=True` 仅用于接入测试。

失败信息见 `last_error`、`error_count`；`pending=True` 表示等待重试。结束状态写入失败后可调用 `flush()`。初始化参数无效时 `enabled=False`，修正后需重新创建。

写入器不处理训练异常、KeyboardInterrupt 或 SystemExit。没有自动心跳、后台重试或 I/O 超时，建议使用本地可写目录。

[完整协议](implementation-plan.md) · [分布式接入说明](../skills/gpu-desk-integration/references/integration.md)
