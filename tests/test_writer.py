"""验证遥测故障不传播到业务，以及恢复后记录仍符合探针协议。"""
import concurrent.futures
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from test_probe import load, probe

writer = load("safe_writer", "integrations/gpu_task.py")
TASK = {"task_id": "test", "name": "测试", "project": "test", "description": "验证安全上报"}
IDENTITY = {"pid": 42, "boot_id": "boot", "start_ticks": "123"}


class SafeWriterTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name) / "run"
        identity = patch.object(writer, "process_identity", return_value=IDENTITY)
        identity.start()
        self.addCleanup(identity.stop)
        self.stderr = io.StringIO()
        stderr = patch.object(writer.sys, "stderr", self.stderr)
        stderr.start()
        self.addCleanup(stderr.stop)

    def make(self, **kwargs):
        return writer.TaskWriter(self.directory, TASK, **kwargs)

    def test_constructor_io_failure_then_recovery(self):
        with patch.object(writer, "atomic_json", side_effect=PermissionError("denied")):
            w = self.make()
        self.assertTrue(w.enabled)
        self.assertTrue(w.pending)
        self.assertIn("PermissionError", w.last_error)
        self.assertTrue(w.update(completed=3, total=10, unit="steps"))
        self.assertIsNone(w.last_error)
        self.assertFalse(w.pending)
        self.assertEqual(probe.load_standard(self.directory)["status"]["progress"]["completed"], 3)
        self.assertIn("上报已恢复", self.stderr.getvalue())

    def test_failed_update_keeps_latest_valid_state_and_original_disk(self):
        w = self.make()
        old = (self.directory / "status.json").read_bytes()
        with patch.object(writer.os, "replace", side_effect=OSError("disk full")):
            self.assertFalse(w.update(completed=7, total=10, unit="steps"))
        self.assertEqual((self.directory / "status.json").read_bytes(), old)
        self.assertEqual(list(self.directory.glob(".status-*")), [])
        self.assertEqual(w.status["progress"]["completed"], 7)
        self.assertTrue(w.pending)
        self.assertTrue(w.flush())
        self.assertEqual(probe.load_standard(self.directory)["status"]["progress"]["completed"], 7)

    def test_final_state_survives_failure_and_cannot_be_rewritten(self):
        w = self.make()
        with patch.object(writer, "atomic_json", side_effect=OSError("disk full")):
            self.assertFalse(w.finish(1))
        finished = w.status["finished_at"]
        self.assertFalse(w.update(message="heartbeat"))
        self.assertTrue(w.finish(1))
        self.assertEqual(w.status["finished_at"], finished)
        self.assertFalse(w.finish(0))
        self.assertEqual(probe.load_standard(self.directory)["status"]["state"], "failed")

    def test_bad_inputs_do_not_stop_training_or_corrupt_state(self):
        w = self.make()
        old = json.loads(json.dumps(w.status))
        for arguments in ({"completed": -1, "unit": "steps"}, {"completed": float("nan"), "unit": "steps"},
                          {"completed": 2, "total": 1, "unit": "steps"}, {"metrics": {"loss": float("inf")}},
                          {"metrics": {"tensor": object()}}, {"artifacts": [{"role": "log", "path": "relative"}]},
                          {"stage": 42}, {"pids": [0]}):
            with self.subTest(arguments=arguments):
                self.assertFalse(w.update(**arguments))
                self.assertEqual(w.status, old)
        self.assertFalse(w.finish(True))
        self.assertTrue(w.update(completed=1, total=3, unit="steps"))

    def test_invalid_constructor_is_disabled_and_conflict_preserves_files(self):
        w = writer.TaskWriter(self.directory, {"name": "bad"})
        self.assertFalse(w.enabled)
        self.assertFalse(w.update())
        self.assertFalse(w.finish(0))
        good = self.make()
        before = (self.directory / "status.json").read_bytes()
        conflict = writer.TaskWriter(self.directory, dict(TASK, name="different"))
        self.assertIn("冲突", conflict.last_error)
        self.assertFalse(conflict.flush())
        self.assertEqual((self.directory / "status.json").read_bytes(), before)
        self.assertTrue(good.update())

    def test_log_rate_limit_and_broken_stderr(self):
        w = self.make()
        with patch.object(writer.time, "monotonic", return_value=10):
            for _ in range(10):
                self.assertFalse(w.update(metrics={"bad": object()}))
        self.assertEqual(self.stderr.getvalue().count("上报失败"), 1)
        self.assertEqual(w.error_count, 10)
        with patch.object(writer.time, "monotonic", return_value=80), patch.object(writer, "print", side_effect=OSError("closed"), create=True):
            self.assertFalse(w.finish("bad"))
        self.assertIsNotNone(w.last_error)

    def test_strict_and_interrupts(self):
        w = self.make(strict=True)
        with self.assertRaises(ValueError):
            w.update(completed=-1, unit="steps")
        safe = self.make()
        for exception in (KeyboardInterrupt, SystemExit):
            with patch.object(writer, "atomic_json", side_effect=exception):
                with self.assertRaises(exception):
                    safe.update()

    def test_real_training_error_is_preserved(self):
        w = self.make()
        with self.assertRaisesRegex(RuntimeError, "training failed"):
            try:
                raise RuntimeError("training failed")
            except Exception:
                with patch.object(writer, "atomic_json", side_effect=OSError("telemetry failed")):
                    w.finish(1)
                raise

    def test_heartbeat_thread_and_progress_share_one_writer(self):
        w = self.make()
        w.update(completed=1, total=10, unit="steps")
        progress_time = w.status["progress"]["updated_at"]
        metrics = {"loss": 2.5}
        w.update(metrics=metrics)
        metrics["loss"] = 99
        self.assertEqual(w.status["metrics"]["loss"], 2.5)
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
            self.assertTrue(all(executor.map(lambda _: w.update(), range(12))))
        self.assertEqual(w.status["progress"]["updated_at"], progress_time)
        self.assertEqual(probe.load_standard(self.directory)["status"]["progress"]["completed"], 1)

    def test_process_identity_failure_is_visible_and_retryable(self):
        with patch.object(writer, "process_identity", side_effect=PermissionError("proc denied")):
            w = self.make()
        self.assertIsNotNone(w.last_error)
        self.assertFalse((self.directory / "status.json").exists())
        self.assertTrue(w.flush())
        with patch.object(writer, "process_identity", side_effect=AssertionError("must not rebind PID")):
            self.assertTrue(w.update())
        self.assertEqual(w.status["processes"], [IDENTITY])


if __name__ == "__main__":
    unittest.main()
