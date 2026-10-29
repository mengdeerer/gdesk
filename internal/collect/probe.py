"""只读远端探针：仅 Python 标准库，不安装软件、不写服务器文件。"""
import base64
import csv
import datetime as dt
import io
import json
import os
import pwd
import stat
import subprocess

MAX_JSON = 1024 * 1024
STATES = {"queued", "running", "paused", "succeeded", "failed", "cancelled", "unknown"}


def read_regular(path, limit=MAX_JSON, tail=False):
    path = os.path.expanduser(path)
    fd = os.open(path, os.O_RDONLY | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as f:
        info = os.fstat(f.fileno())
        if not stat.S_ISREG(info.st_mode):
            raise ValueError("不是普通文件: " + path)
        if tail:
            f.seek(max(0, info.st_size - limit))
        elif info.st_size > limit:
            raise ValueError("文件超过大小限制: " + path)
        data = f.read(limit + 1)
        if len(data) > limit:
            raise ValueError("文件超过大小限制: " + path)
        return data


def read_json(path):
    def reject_constant(value):
        raise ValueError("JSON 不允许非有限数值: " + value)
    return json.loads(read_regular(path), parse_constant=reject_constant)


def timestamp(value):
    if not isinstance(value, str):
        raise ValueError("时间必须为 RFC3339 字符串")
    result = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if result.tzinfo is None:
        raise ValueError("时间必须包含时区")
    return value


def validate_record(task, status):
    if task.get("schema_version") != 1 or status.get("schema_version") != 1:
        raise ValueError("不支持的 schema_version")
    for key in ("task_id", "name", "project", "description"):
        if not isinstance(task.get(key), str) or not task[key].strip():
            raise ValueError("任务缺少 " + key)
    if status.get("task_id") != task["task_id"]:
        raise ValueError("task.json 与 status.json 的 task_id 不一致")
    if not status.get("attempt_id") or status.get("state") not in STATES:
        raise ValueError("attempt_id 或 state 无效")
    timestamp(status["updated_at"])
    for key in ("started_at", "finished_at"):
        if status.get(key):
            timestamp(status[key])
    for proc in status.get("processes", []):
        if not isinstance(proc.get("pid"), int) or proc["pid"] <= 0 or not proc.get("boot_id") or not str(proc.get("start_ticks", "")).isdigit():
            raise ValueError("进程身份需要 pid、boot_id、start_ticks")
        proc["start_ticks"] = str(proc["start_ticks"])
    progress = status.get("progress")
    if progress is not None:
        done, total = progress.get("completed"), progress.get("total")
        if not isinstance(done, (int, float)) or done < 0:
            raise ValueError("progress.completed 无效")
        if total is not None and (not isinstance(total, (int, float)) or total < done):
            raise ValueError("progress.total 必须 >= completed")
        if progress.get("scope") not in ("task", "stage") or not progress.get("unit"):
            raise ValueError("progress 需要 scope 和 unit")
        timestamp(progress["updated_at"])
    for section in (task, status):
        for artifact in section.get("artifacts", []):
            if not artifact.get("role") or not os.path.isabs(artifact.get("path", "")):
                raise ValueError("artifact 需要 role 和远端绝对路径")
    # Go 类型解析放在每条记录内，避免一条错误记录破坏整台主机。
    if not isinstance(task.get("parameters", {}), dict) or not isinstance(status.get("metrics", {}), dict):
        raise ValueError("parameters/metrics 必须为对象")
    return {"task": task, "status": status, "source": "standard v1"}


def load_standard(directory):
    directory = os.path.expanduser(directory)
    task = read_json(os.path.join(directory, "task.json"))
    status = read_json(os.path.join(directory, "status.json"))
    return validate_record(task, status)


def records(request, errors):
    result = []
    seen = set()
    sources = [(load_standard, p) for p in request.get("task_dirs") or []]
    for loader, source in sources[:128]:
        try:
            record = loader(source)
            identity = record["task"]["task_id"]
            if identity in seen:
                raise ValueError("重复 task_id: " + identity)
            seen.add(identity)
            result.append(record)
        except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
            errors.append("元信息 " + str(source) + ": " + str(error))
    return result


def number(value):
    try:
        return float(value.strip())
    except ValueError:
        return None


def query(option, columns):
    result = subprocess.run(["nvidia-smi", option + "=" + columns, "--format=csv,noheader,nounits"],
                            capture_output=True, text=True, timeout=5, check=True)
    return list(csv.reader(io.StringIO(result.stdout), skipinitialspace=True))


def read_process(pid, boot):
    proc = {"pid": pid, "boot_id": boot, "start_ticks": "", "user": "未知", "command": "", "cwd": "", "gpu_uuids": [], "memory_mib": {}}
    try:
        with open("/proc/%d/stat" % pid) as f:
            fields = f.read().rsplit(")", 1)[1].split()
        proc["start_ticks"] = fields[19]
        # 从远端启动计数计算，避免本地与服务器时钟偏差影响运行时长。
        try:
            with open("/proc/uptime") as f:
                uptime = float(f.read().split()[0])
            elapsed = uptime - int(fields[19]) / os.sysconf("SC_CLK_TCK")
            if elapsed >= 0:
                proc["elapsed_seconds"] = elapsed
        except (OSError, ValueError):
            pass  # 时长可选；读取失败时不展示，不影响其他进程信息。
        proc["user"] = pwd.getpwuid(os.stat("/proc/%d" % pid).st_uid).pw_name
        with open("/proc/%d/cmdline" % pid, "rb") as f:
            proc["command"] = f.read(8192).replace(b"\0", b" ").decode(errors="replace").strip()
        proc["cwd"] = os.readlink("/proc/%d/cwd" % pid)
        with open("/proc/%d/stat" % pid) as f:
            after = f.read().rsplit(")", 1)[1].split()[19]
        if after != proc["start_ticks"]:
            raise ValueError("采集中 PID 已被复用")
    except (OSError, KeyError, ValueError) as error:
        proc["error"] = "进程读取受限或已退出: " + str(error)
        # 不完整或不一致的身份不得用于自动关联。
        if isinstance(error, ValueError):
            proc["start_ticks"] = ""
            proc.pop("elapsed_seconds", None)
    return proc


def snapshot(request):
    result = {"observed_at": dt.datetime.now(dt.timezone.utc).isoformat(), "user": pwd.getpwuid(os.getuid()).pw_name,
              "gpus": [], "processes": [], "records": [], "errors": [], "gpu_ok": False, "processes_ok": False}
    try:
        rows = query("--query-gpu", "index,uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu")
        for row in rows:
            index, uuid, name, util, used, total, temperature = row
            result["gpus"].append(dict(index=int(index), uuid=uuid, name=name, util=number(util), used_mib=number(used), total_mib=number(total), temperature=number(temperature)))
        result["gpu_ok"] = True
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        result["errors"].append("GPU 查询失败: " + str(error))
    try:
        with open("/proc/sys/kernel/random/boot_id") as f:
            boot = f.read().strip()
        grouped = {}
        for row in query("--query-compute-apps", "gpu_uuid,pid,used_gpu_memory"):
            gpu, raw_pid, memory = row
            pid = int(raw_pid)
            if pid not in grouped:
                grouped[pid] = read_process(pid, boot)
            grouped[pid]["gpu_uuids"].append(gpu)
            grouped[pid]["memory_mib"][gpu] = number(memory)
        result["processes"] = list(grouped.values())
        result["processes_ok"] = True
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        result["errors"].append("计算进程查询失败: " + str(error))
    result["records"] = records(request, result["errors"])
    return result


def log_tail(request):
    errors = []
    known = records(request, errors)
    allowed = {os.path.realpath(a["path"]) for r in known for section in (r["task"], r["status"])
               for a in section.get("artifacts", []) if a["role"] == "log"}
    path = os.path.realpath(request["path"])
    if path not in allowed:
        raise ValueError("日志路径未在当前任务元信息中登记")
    data = read_regular(path, limit=64 * 1024, tail=True)
    return {"text": "\n".join(data.decode(errors="replace").splitlines()[-160:])}


def main(request):
    try:
        result = log_tail(request) if request.get("op") == "log" else snapshot(request)
    except (OSError, ValueError, KeyError, TypeError) as error:
        result = {"error": str(error)}
    print(json.dumps(result, ensure_ascii=False, allow_nan=False))
