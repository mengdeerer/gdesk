"""标准元信息写入器。复制到实验目录即可使用；只依赖 Python 标准库。

默认上报失败只告警，不中断业务；strict=True 用于接入测试。
每个任务目录只允许一个控制器写入；同一实例可跨线程调用。远端 Linux 上运行。
"""
import datetime as dt
import json
import math
import os
from pathlib import Path
import tempfile
import sys
import threading
import time
import uuid


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def process_identity(pid):
    """读取真实进程身份，不接受只有 PID 的不完整绑定。"""
    with open("/proc/sys/kernel/random/boot_id") as f:
        boot = f.read().strip()
    with open("/proc/%d/stat" % pid) as f:
        ticks = f.read().rsplit(")", 1)[1].split()[19]
    return {"pid": pid, "boot_id": boot, "start_ticks": ticks}


def atomic_json(path, data):
    path = Path(path)
    serialized = json.dumps(data, ensure_ascii=False, indent=2, allow_nan=False)
    with tempfile.NamedTemporaryFile("w", encoding="utf-8", dir=path.parent, prefix=".status-", delete=False) as f:
        temporary = f.name
        try:
            f.write(serialized + "\n")
            f.flush()
            os.fsync(f.fileno())
        except BaseException:
            os.unlink(temporary)
            raise
    try:
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def json_copy(value):
    """隔离调用方的可变对象，并在写文件前验证 JSON 可序列化。"""
    return json.loads(json.dumps(value, ensure_ascii=False, allow_nan=False))


def count(value):
    return type(value) in (int, float) and math.isfinite(value) and value >= 0


class TaskWriter:
    """尽力上报；update/finish/flush 返回是否成功落盘。

    last_error 保留最近错误，error_count 累计错误次数，pending 表示有待写状态。
    普通 Exception 不会逃出默认模式；KeyboardInterrupt/SystemExit 保持原有语义。
    每次调用最多尝试一次写入，不循环重试，不自动启动线程。
    """

    def __init__(self, directory, task, pids=None, *, strict=False):
        self.strict = strict
        self.last_error = None
        self.error_count = 0
        self.pending = False
        self.enabled = False
        self.status = {}
        self._lock = threading.RLock()
        self._last_warning = None
        self._identities = None
        self._attempt(lambda: self._configure(directory, task, pids))

    def _configure(self, directory, task, pids):
        self.directory = Path(directory).expanduser()
        task = dict(task)
        task.setdefault("schema_version", 1)
        for key in ("task_id", "name", "project", "description"):
            if not isinstance(task.get(key), str) or not task[key].strip():
                raise ValueError("任务缺少 " + key)
        if type(task["schema_version"]) is not int or task["schema_version"] != 1:
            raise ValueError("只支持 schema_version=1")
        if not isinstance(task.get("parameters", {}), dict):
            raise ValueError("parameters 必须为对象")
        if not isinstance(task.get("provenance", {}), dict):
            raise ValueError("provenance 必须为对象")
        tags = task.get("tags", [])
        if not isinstance(tags, list) or any(not isinstance(tag, str) for tag in tags):
            raise ValueError("tags 必须为字符串数组")
        display = task.get("display", {})
        if not isinstance(display, dict):
            raise ValueError("display 必须为对象")
        fields = display.get("summary_fields", [])
        if not isinstance(fields, list) or any(not isinstance(field, str) for field in fields):
            raise ValueError("summary_fields 必须为字符串数组")
        if not isinstance(task.get("kind", ""), str):
            raise ValueError("kind 必须为字符串")
        self._validate_artifacts(task.get("artifacts", []))
        self.task = json_copy(task)
        self._pids = self._validate_pids(pids if pids is not None else [os.getpid()])
        self.status = {"schema_version": 1, "task_id": task["task_id"], "attempt_id": str(uuid.uuid4()),
                       "state": "running", "stage": "starting", "processes": [],
                       "started_at": now(), "updated_at": now()}
        self.enabled = True
        self.pending = True
        self._publish()

    @staticmethod
    def _validate_pids(pids):
        pids = list(pids)
        if any(type(pid) is not int or pid <= 0 for pid in pids):
            raise ValueError("pids 必须包含正整数 PID")
        return list(dict.fromkeys(pids))

    @staticmethod
    def _validate_artifacts(artifacts):
        if not isinstance(artifacts, list):
            raise ValueError("artifacts 必须为数组")
        for artifact in artifacts:
            if not isinstance(artifact, dict) or not isinstance(artifact.get("role"), str) or not artifact["role"] or not isinstance(artifact.get("path"), str) or not os.path.isabs(artifact["path"]):
                raise ValueError("artifacts 需要 role 和绝对路径")

    def _diagnostic(self, text):
        # 不使用 warnings.warn，避免用户的 -W error 把监控告警变成训练异常。
        try:
            print("[gpu-task] " + text, file=sys.stderr, flush=True)
        except Exception:
            pass  # stderr 也可能已关闭；last_error/error_count 仍可检查。

    def _attempt(self, operation):
        with self._lock:
            try:
                operation()
            except Exception as error:
                self.error_count += 1
                try:
                    self.last_error = type(error).__name__ + ": " + str(error)
                except Exception:
                    self.last_error = type(error).__name__
                at = time.monotonic()
                if self._last_warning is None or at - self._last_warning >= 60:
                    self._last_warning = at
                    self._diagnostic(("上报失败，严格模式抛出；" if self.strict else "上报失败，业务继续；") + self.last_error)
                if self.strict:
                    raise
                return False
            if self.last_error is not None:
                self._diagnostic("上报已恢复")
            self.last_error = None
            self._last_warning = None
            return True

    def _publish(self):
        if not self.enabled:
            raise ValueError("写入器初始化参数无效，请修正参数后重新创建")
        # 首次成功读取后固定进程身份，后续重试不因 PID 复用而绑定其他进程。
        if self._identities is None:
            self._identities = [process_identity(pid) for pid in self._pids]
        candidate = json_copy(dict(self.status, processes=self._identities))
        self.directory.mkdir(parents=True, exist_ok=True)
        path = self.directory / "task.json"
        if path.exists():
            if json.loads(path.read_text(encoding="utf-8")) != self.task:
                raise ValueError("任务目录定义冲突；请使用新目录，已有记录不会被覆盖")
        else:
            atomic_json(path, self.task)
        atomic_json(self.directory / "status.json", candidate)
        self.status = candidate
        self.pending = False

    def flush(self):
        """重试最新有效状态，包含写失败的结束状态；不伪造新心跳。"""
        return self._attempt(self._publish)

    def update(self, *, stage=None, completed=None, total=None, unit=None, scope="stage",
               message=None, metrics=None, pids=None, artifacts=None):
        def apply():
            if not self.enabled:
                raise ValueError("写入器初始化参数无效，请修正参数后重新创建")
            if self.status["state"] in ("succeeded", "failed", "cancelled"):
                raise ValueError("已结束的 attempt 不能继续更新；重试写盘请调用 flush()")
            candidate = json_copy(self.status)
            at = now()
            if stage is not None:
                if not isinstance(stage, str):
                    raise ValueError("stage 必须为字符串")
                if candidate.get("stage") != stage and candidate.get("progress", {}).get("scope") == "stage":
                    candidate.pop("progress", None)
                candidate["stage"] = stage
            if completed is not None:
                if not count(completed) or (total is not None and (not count(total) or total < completed)) or scope not in ("stage", "task") or not isinstance(unit, str) or not unit.strip():
                    raise ValueError("进度需要非负有限计数、total >= completed、有效 unit/scope")
                old = candidate.get("progress", {})
                progress = {"completed": completed, "unit": unit, "scope": scope}
                if total is not None:
                    progress["total"] = total
                changed = any(old.get(k) != v for k, v in progress.items()) or old.get("total") != total
                progress["updated_at"] = at if changed else old["updated_at"]
                candidate["progress"] = progress
            elif total is not None or unit is not None:
                raise ValueError("total/unit 需要与 completed 一起提供")
            if message is not None:
                if not isinstance(message, str):
                    raise ValueError("message 必须为字符串")
                candidate["message"] = message
            if metrics is not None:
                if not isinstance(metrics, dict):
                    raise ValueError("metrics 必须为对象")
                candidate["metrics"] = metrics
            if artifacts is not None:
                self._validate_artifacts(artifacts)
                candidate["artifacts"] = artifacts
            candidate["updated_at"] = at
            candidate = json_copy(candidate)
            identities = self._identities
            new_pids = self._pids
            if pids is not None:
                new_pids = self._validate_pids(pids)
                identities = [process_identity(pid) for pid in new_pids]
            # 参数校验失败不修改状态；写盘失败则保留最新有效状态供下次重试。
            self.status = candidate
            self._identities = identities
            self._pids = new_pids
            self.pending = True
            self._publish()
        return self._attempt(apply)

    def finish(self, exit_code):
        def apply():
            if not self.enabled:
                raise ValueError("写入器初始化参数无效，请修正参数后重新创建")
            if type(exit_code) is not int:
                raise ValueError("finish 需要实际整数退出码")
            if self.status["state"] in ("succeeded", "failed", "cancelled"):
                if self.status.get("exit_code") != exit_code:
                    raise ValueError("不能改写已结束的执行结果")
            else:
                state = "succeeded" if exit_code == 0 else "failed"
                if exit_code in (130, 143, -2, -15):
                    state = "cancelled"
                self.status = dict(self.status, state=state, exit_code=exit_code,
                                   finished_at=now(), updated_at=now())
                self.pending = True
            self._publish()
        return self._attempt(apply)
