# 接口与接入方式

## 最小接入

复制 skill 的 `assets/gpu_task.py` 后，按现有实验代码改写以下钩子；不要引入一个虚假的训练循环代替原实现：

```python
from gpu_task import TaskWriter

writer = TaskWriter(run_dir, {
    "task_id": experiment_id,
    "name": experiment_name,
    "project": project_name,
    "description": experiment_purpose,
})

# 实际完成 step 后；step 应含断点续跑前已完成的计数。
writer.update(stage="训练", completed=global_step, total=max_steps,
              unit="steps", metrics={"loss": existing_python_loss})

# 定时心跳不更改进度的 updated_at。
writer.update()

# 阶段切换：清除旧的阶段进度，下一次再上报新计数。
writer.update(stage="评测")
writer.update(completed=finished_questions, total=total_questions,
              unit="questions")

# 控制器已获得真实子进程退出码时：
writer.finish(process.returncode)
```

每个 task_id 对应一个目录，task.json 定义固定；新执行生成新 attempt_id。目录示例 `~/experiments/run-001/gpu-desk`，配置登记这个目录本身。

## 原地运行与退出

对直接运行的训练入口，保留原始异常：

```python
try:
    run_existing_training()
except KeyboardInterrupt:
    writer.finish(130)
    raise
except SystemExit as error:
    code = error.code
    writer.finish(0 if code is None else code if isinstance(code, int) else 1)
    raise
except Exception:
    writer.finish(1)
    raise
else:
    writer.finish(0)
```

这是对现有入口的示意，不新增信号处理或改变原有退出策略。若项目已经集中处理退出码，就在那里调用 finish。不要在 finally 无条件 finish(0)。SIGKILL 无法补写结束状态，保持未知／过期，不伪造失败或成功。

## 进度口径

- 训练：明确 batch、optimizer step 或 epoch；梯度累积时不要混用。总量与恢复点采用同一单位。
- 评测：结果真正完成并按原有流程保存后计数；重试不重复计数。恢复时包含已完成结果；总量只含本次计划范围。
- `scope="stage"` 是默认值，切换 stage 清除旧阶段进度；`scope="task"` 仅用于真实可比较的整体计数。
- 未知总数：`writer.update(completed=n, unit="questions")`，看板显示完成数量；不要给假分母。
- metrics 需要普通 JSON 值。NaN、Infinity、Tensor 或自定义对象会让整次 update 返回 False；不要把非有限 loss 改成 0 来掩盖训练问题。可省略无效指标并上报有效计数。
- 高频循环在完成回调中按 monotonic 时间节流至每 2–5 秒一次，最终计数、阶段变化和 finish 不节流。writer 每次调用都写文件，没有内置后台线程。

## 长操作的心跳

优先利用项目现有定时回调，每 10–20 秒调用 `writer.update()`。如果单个计算单元本身很久没有回调，可由控制器的 daemon 线程发送心跳；使用同一个 writer（方法有锁），退出时用 Event 停止线程，不改变业务异常。不要在 fork 前启动心跳线程，不要让所有 rank 各写一次。finish 后的 update 会返回 False，保持结束状态；停止心跳再 finish 可避免无意义告警。

## 多进程与日志

`writer.update(pids=worker_pids)` 读取 PID、boot_id、start_ticks，防止同一 PID 的其他进程被误认。默认绑定当前进程；未读到完整身份时不生成假绑定，上报返回 False。身份读取首次失败后可重试；成功后固定身份，已有 worker 更换时由控制器显式更新 PID 列表。

进度写入者只能有一个。分布式进度优先利用已有同步／结果汇总，不为了看板增加所有 rank 必须参与的新 barrier 或 collective。rank 0 的本地完成数未必等于全局完成数。

日志和产物使用远端绝对路径：

```python
writer.update(artifacts=[{"role": "log", "path": "/work/runs/exp/train.log"}])
```

不读取／转储模型 Tensor 来生成指标，不上传额外数据，不写入密钥或环境变量快照。

## 失败与恢复契约

- 默认隔离初始化、校验、进程读取、序列化和写盘的普通 Exception，返回 False；stderr 告警最多每分钟一次，恢复时提示一次。stderr 不可写时仍保留 last_error/error_count。
- 初始化参数无效：enabled=False，修正参数并重新创建实例。初始化 I/O 或 /proc 失败：enabled=True，可由后续 update/flush 重试。
- 校验失败：本次更新不生效，旧状态保留。写盘失败：有效新状态保存在内存，pending=True；下一次 update 合并该状态再尝试写盘。
- finish 写失败：终态保留在内存；调用 flush() 或同退出码 finish() 重试。不能改写结果，也不能继续 update。进程退出前未写成功的数据不会自动跨进程恢复。
- flush() 不推进心跳时间；每次调用只尝试一次，不循环重试。由既有上报节奏触发恢复，结束阶段可有限补写，不无限阻塞业务退出。
- strict=True 会重新抛出错误，仅用于调试／测试；不建议生产训练开启。

## 验证清单

在临时目录 mock process_identity，在 Linux 上再验证真实 /proc 与 GPU worker 匹配。至少检查一次写盘失败后业务循环继续、进度更新恢复、终态补写、旧文件没有被无效指标污染，以及训练异常没有被 finish 的故障覆盖。运行项目既有测试；无需为此启动完整训练。
