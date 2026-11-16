#!/usr/bin/env python3
"""Measure warm hook latency in an isolated profile without invoking Codex."""
import json
import math
import os
import pathlib
import statistics
import subprocess
import sys
import tempfile
import time


def measure(binary, count):
    with tempfile.TemporaryDirectory(prefix="vigil-hook-benchmark-") as work:
        base = pathlib.Path(work)
        config = base / "config.yaml"
        config.write_text("mode: observe\nstorage:\n  path: events.db\n")
        env = os.environ.copy()
        env.pop("VIGIL_ADVISOR_CHILD", None)
        result = {}
        for kind in ("PostToolUse", "Stop"):
            times = []
            for i in range(count + 1):
                payload = json.dumps({
                    "hook_event_name": kind,
                    "session_id": "benchmark",
                    "turn_id": f"turn-{i}",
                    "tool_use_id": f"tool-{i}",
                    "tool_name": "Bash",
                    "exit_code": 0,
                    "stop_hook_active": False,
                }).encode()
                start = time.perf_counter()
                process = subprocess.run(
                    [binary, "hook", "--config", str(config)], input=payload,
                    capture_output=True, check=True, timeout=5, env=env,
                )
                if process.stderr or process.stdout:
                    raise RuntimeError(f"unexpected output during {kind} measurement")
                if i:
                    times.append((time.perf_counter() - start) * 1000)
            result[kind] = {
                "count": count,
                "p50_ms": round(statistics.median(times), 2),
                "p95_ms": round(sorted(times)[math.ceil(0.95 * count) - 1], 2),
            }
        return result


if __name__ == "__main__":
    print(json.dumps(measure(str(pathlib.Path(sys.argv[1]).resolve()), 100), indent=2))
