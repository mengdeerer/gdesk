import copy
import importlib.util
import json
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


probe = load("probe", "internal/collect/probe.py")
writer = load("writer", "integrations/gpu_task.py")


class ProbeTests(unittest.TestCase):
    def test_process_elapsed_from_remote_uptime(self):
        stat = "42 (python worker) " + " ".join(["S"] + ["0"] * 18 + ["10000"])
        def read(path, *args):
            if path.endswith("/stat"):
                return io.StringIO(stat)
            if path == "/proc/uptime":
                return io.StringIO("3700.00 100.00")
            raise PermissionError(path)
        with patch("builtins.open", side_effect=read), patch.object(probe.os, "sysconf", return_value=100), patch.object(probe.os, "stat", side_effect=PermissionError()):
            result = probe.read_process(42, "boot")
        self.assertEqual(result["elapsed_seconds"], 3600)
        self.assertEqual(result["start_ticks"], "10000")
        self.assertIn("error", result)

    def test_unavailable_process_time_is_omitted(self):
        with patch("builtins.open", side_effect=PermissionError()):
            result = probe.read_process(42, "boot")
        self.assertNotIn("elapsed_seconds", result)

    def setUp(self):
        self.task = json.loads((ROOT / "examples/task/task.json").read_text())
        self.status = json.loads((ROOT / "examples/task/status.json").read_text())

    def test_schema_and_unknown_total(self):
        self.status["progress"].pop("total")
        self.assertEqual(probe.validate_record(self.task, self.status)["status"]["progress"]["completed"], 32)
        for change in (lambda s: s.update(task_id="other"), lambda s: s.update(schema_version=2),
                       lambda s: s["progress"].update(total=1), lambda s: s.update(updated_at="2026-10-29T01:00:00"),
                       lambda s: s.update(processes=[{"pid": 42}])):
            with self.subTest(change=change):
                bad = copy.deepcopy(self.status)
                change(bad)
                with self.assertRaises(ValueError):
                    probe.validate_record(self.task, bad)

    def test_failed_query_is_not_empty_success(self):
        with patch.object(probe, "query", side_effect=FileNotFoundError("nvidia-smi")):
            result = probe.snapshot({})
        self.assertFalse(result["gpu_ok"])
        self.assertFalse(result["processes_ok"])
        self.assertTrue(result["errors"])

    def test_partial_metadata_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            (path / "task.json").write_text(json.dumps(self.task))
            (path / "status.json").write_text(json.dumps(self.status))
            errors = []
            records = probe.records({"task_dirs": [directory, directory + "/missing"]}, errors)
            self.assertEqual(len(records), 1)
            self.assertEqual(len(errors), 1)

    def test_log_allowlist_and_bounded_tail(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            log = path / "train.log"
            log.write_text("line\n" * 20000)
            self.status["artifacts"] = [{"role": "log", "path": str(log)}]
            (path / "task.json").write_text(json.dumps(self.task))
            (path / "status.json").write_text(json.dumps(self.status))
            request = {"task_dirs": [directory], "path": str(log)}
            self.assertEqual(len(probe.log_tail(request)["text"].splitlines()), 160)
            request["path"] = "/etc/passwd"
            with self.assertRaises(ValueError):
                probe.log_tail(request)

    def test_unversioned_state_is_rejected(self):
        with self.assertRaises(ValueError):
            probe.validate_record(self.task, {"state": "running", "pid": 42})

    def test_writer_heartbeat_stage_and_immutable_definition(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(writer, "process_identity", return_value={"pid": 42, "boot_id": "boot", "start_ticks": "123"}):
            w = writer.TaskWriter(directory, self.task, strict=True)
            w.update(stage="train", completed=1, total=100, unit="epochs")
            changed = w.status["progress"]["updated_at"]
            w.update(stage="train", completed=1, total=100, unit="epochs")
            self.assertEqual(w.status["progress"]["updated_at"], changed)
            w.update(stage="validate")
            self.assertNotIn("progress", w.status)
            w.finish(1)
            self.assertEqual(w.status["state"], "failed")
            self.assertEqual(probe.load_standard(directory)["status"]["exit_code"], 1)
            with self.assertRaises(ValueError):
                w.update(completed=2, unit="epochs")
            with self.assertRaises(ValueError):
                writer.TaskWriter(directory, dict(self.task, name="changed"), strict=True)


if __name__ == "__main__":
    unittest.main()
