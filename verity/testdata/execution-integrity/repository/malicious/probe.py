"""Opt-in probe for a disposable no-network, read-only isolation runtime."""

import json
import os
import socket
from pathlib import Path
import sys


def main() -> None:
    outcomes = {}
    try:
        Path("/tmp/verity-fixture-write-probe").write_text("probe", encoding="utf-8")
        outcomes["tmp_write"] = "allowed"
    except OSError:
        outcomes["tmp_write"] = "blocked"
    try:
        Path("/etc/verity-fixture-write-probe").write_text("probe", encoding="utf-8")
        outcomes["host_path_write"] = "allowed"
    except OSError:
        outcomes["host_path_write"] = "blocked"
    try:
        escape = Path("/workspace/verity-fixture-symlink-probe")
        escape.symlink_to("/etc", target_is_directory=True)
        (escape / "verity-fixture-write-probe").write_text("probe", encoding="utf-8")
        outcomes["symlink_escape"] = "allowed"
    except OSError:
        outcomes["symlink_escape"] = "blocked"
    finally:
        Path("/workspace/verity-fixture-symlink-probe").unlink(missing_ok=True)
    try:
        listener = socket.socket()
        listener.bind(("127.0.0.1", 0))
        listener.listen(1)
        with socket.create_connection(listener.getsockname(), timeout=0.5):
            outcomes["loopback"] = "reachable"
        listener.close()
    except OSError:
        outcomes["loopback"] = "blocked"
    try:
        with socket.create_connection(("1.1.1.1", 443), timeout=0.5):
            outcomes["external_egress"] = "reachable"
    except OSError:
        outcomes["external_egress"] = "blocked"
    outcomes["interfaces"] = sorted(path.name for path in Path("/sys/class/net").iterdir())
    sensitive_names = ("TOKEN", "SECRET", "PASSWORD", "PASSWD", "API_KEY", "PRIVATE_KEY")
    outcomes["credential_env"] = "present" if any(
        any(marker in key.upper() for marker in sensitive_names) for key in os.environ
    ) else "absent"
    if len(sys.argv) > 1:
        try:
            Path(sys.argv[1]).read_text(encoding="utf-8")
            outcomes["host_home_secret"] = "visible"
        except OSError:
            outcomes["host_home_secret"] = "absent"
    outcomes["docker_socket"] = "present" if Path("/var/run/docker.sock").exists() else "absent"
    if hasattr(os, "fork"):
        read_fd, write_fd = os.pipe()
        children = []
        try:
            for _ in range(200):
                try:
                    pid = os.fork()
                except OSError:
                    break
                if pid == 0:
                    os.close(write_fd)
                    os.read(read_fd, 1)
                    os._exit(0)
                children.append(pid)
            outcomes["pid_spawned"] = len(children)
        finally:
            if children:
                os.write(write_fd, b"x" * len(children))
                for pid in children:
                    try:
                        os.waitpid(pid, 0)
                    except ChildProcessError:
                        pass
            os.close(read_fd)
            os.close(write_fd)
    print(json.dumps(outcomes, sort_keys=True))
    if outcomes["host_path_write"] != "blocked" or outcomes["symlink_escape"] != "blocked":
        sys.exit(1)
    if outcomes["external_egress"] != "blocked":
        sys.exit(1)
    if outcomes["interfaces"] != ["lo"] or outcomes["loopback"] != "reachable":
        sys.exit(1)
    if outcomes["credential_env"] != "absent" or outcomes["docker_socket"] != "absent":
        sys.exit(1)
    if "pid_spawned" in outcomes and outcomes["pid_spawned"] >= 200:
        sys.exit(1)


if __name__ == "__main__":
    main()
