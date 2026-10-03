"""Black-box controller/worker smoke test for the real Nodren runtime."""

from __future__ import annotations

import base64
from concurrent.futures import ThreadPoolExecutor
import json
import os
import socket
import struct
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from urllib.error import URLError
from urllib.request import Request, urlopen


ROOT = Path(__file__).resolve().parents[2]
CONTROLLER_DIR = ROOT / "Backend" / "Controller-Go"
WORKER_DIR = ROOT / "Backend" / "Worker-Rust"


def free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def request(method: str, url: str, payload: dict | None = None) -> dict:
    data = None
    headers = {}
    if payload is not None:
        data = json.dumps(payload).encode()
        headers["Content-Type"] = "application/json"
    req = Request(url, data=data, headers=headers, method=method)
    with urlopen(req, timeout=2) as response:
        return json.load(response)


def wait_for(predicate, timeout: float, description: str):
    deadline = time.monotonic() + timeout
    last_error = None
    while time.monotonic() < deadline:
        try:
            value = predicate()
            if value:
                return value
        except (OSError, URLError, ValueError) as error:
            last_error = error
        time.sleep(0.1)
    detail = f": {last_error}" if last_error else ""
    raise RuntimeError(f"timed out waiting for {description}{detail}")


def build_binaries(output_dir: Path) -> tuple[Path, Path]:
    controller = output_dir / ("nodren.exe" if os.name == "nt" else "nodren")
    subprocess.run(
        ["go", "build", "-o", str(controller), "."],
        cwd=CONTROLLER_DIR,
        check=True,
    )
    subprocess.run(["cargo", "build", "--bin", "nodren-worker"], cwd=WORKER_DIR, check=True)
    worker_name = "nodren-worker.exe" if os.name == "nt" else "nodren-worker"
    worker = WORKER_DIR / "target" / "debug" / worker_name
    if not worker.exists():
        raise RuntimeError(f"worker binary was not produced: {worker}")
    return controller, worker


def run_cli(cli: Path, environment: dict[str, str], *arguments: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [str(cli), *arguments],
        cwd=CONTROLLER_DIR,
        env=environment,
        capture_output=True,
        text=True,
        timeout=30,
    )


def submit_and_wait(
    base_url: str,
    command: str,
    payload: bytes,
    description: str,
    requirements: dict | None = None,
) -> dict:
    if requirements is None:
        requirements = {"cpu_cores": 1, "ram_gb": 1, "gpu_required": False}
    job = submit_job(base_url, command, payload, requirements)

    def completed_job():
        state = request("GET", f"{base_url}/v1/jobs/{job['id']}")
        return state if state["status"] in {"COMPLETED", "FAILED"} else None

    return wait_for(completed_job, 15, description)


def submit_job(base_url: str, command: str, payload: bytes, requirements: dict) -> dict:
    return request(
        "POST",
        f"{base_url}/v1/jobs",
        {
            "command": command,
            "priority": 50,
            "requirements": requirements,
            "payload_base64": base64.b64encode(payload).decode(),
        },
    )


def submit_task(base_url: str, task: dict) -> dict:
    return request("POST", f"{base_url}/v1/tasks", {"task": task})


def wait_for_job(base_url: str, job_id: str, description: str, timeout: float = 20) -> dict:
    last_state = None

    def terminal_job():
        nonlocal last_state
        state = request("GET", f"{base_url}/v1/jobs/{job_id}")
        last_state = state
        return state if state["status"] in {"COMPLETED", "FAILED"} else None

    try:
        return wait_for(terminal_job, timeout, description)
    except RuntimeError as error:
        state = last_state or {}
        try:
            nodes = [
                {
                    "id": node["info"]["id"],
                    "state": node["state"],
                    "allocated_cpu_cores": node.get("allocated_cpu_cores"),
                    "allocated_ram_gb": node.get("allocated_ram_gb"),
                }
                for node in request("GET", f"{base_url}/v1/nodes")
            ]
        except (KeyError, OSError, URLError, ValueError):
            nodes = []
        raise RuntimeError(
            f"{error}; latest job status={state.get('status')} "
            f"distribution={state.get('distribution')} partitions={state.get('partitions')} "
            f"workers={nodes}"
        ) from error


def node_by_id(base_url: str, node_id: str) -> dict:
    for node in request("GET", f"{base_url}/v1/nodes"):
        if node["info"]["id"] == node_id:
            return node
    raise RuntimeError(f"node not found: {node_id}")


def main() -> int:
    tcp_port = free_port()
    http_port = free_port()
    base_url = f"http://127.0.0.1:{http_port}"
    processes: list[subprocess.Popen] = []
    temporary_directory = tempfile.TemporaryDirectory(prefix="nodren-integration-")

    try:
        controller_binary, worker_binary = build_binaries(Path(temporary_directory.name))
        controller_env = os.environ.copy()
        controller_env.update(
            NODREN_AUTH_MODE="development",
            NODREN_NODE_ADDR=f"127.0.0.1:{tcp_port}",
            NODREN_HTTP_ADDR=f"127.0.0.1:{http_port}",
            NODREN_STATE_FILE=str(Path(temporary_directory.name) / "controller-state.json"),
        )
        worker_env = os.environ.copy()
        worker_env["NODREN_AUTH_MODE"] = "development"
        controller_process = subprocess.Popen(
                [str(controller_binary)],
                cwd=CONTROLLER_DIR,
                env=controller_env,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
        )
        processes.append(controller_process)
        wait_for(lambda: request("GET", f"{base_url}/health")["status"] == "ok", 15, "controller health")

        worker_a = subprocess.Popen(
            [
                str(worker_binary),
                f"--controller=127.0.0.1:{tcp_port}",
                "--id=INTEGRATION-A",
                "--cpu-cores=2",
                "--ram-gb=4",
            ],
            cwd=WORKER_DIR,
            env=worker_env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        processes.append(worker_a)
        worker_b = subprocess.Popen(
            [
                str(worker_binary),
                f"--controller=127.0.0.1:{tcp_port}",
                "--id=INTEGRATION-B",
                "--cpu-cores=8",
                "--ram-gb=16",
            ],
            cwd=WORKER_DIR,
            env=worker_env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        processes.append(worker_b)
        wait_for(
            lambda: {
                node["info"]["id"]: node
                for node in request("GET", f"{base_url}/v1/nodes")
            }
            if all(
                node["info"]["id"] in {"INTEGRATION-A", "INTEGRATION-B"}
                and node["state"] == "READY"
                for node in request("GET", f"{base_url}/v1/nodes")
            )
            and len(request("GET", f"{base_url}/v1/nodes")) == 2
            else None,
            15,
            "two worker registrations",
        )
        nodes = request("GET", f"{base_url}/v1/nodes")
        resources = {node["info"]["id"]: node["info"] for node in nodes}
        if (
            resources["INTEGRATION-A"]["cpu_cores"] != 2
            or resources["INTEGRATION-A"]["ram_gb"] != 4
            or resources["INTEGRATION-B"]["cpu_cores"] != 8
            or resources["INTEGRATION-B"]["ram_gb"] != 16
        ):
            raise RuntimeError(f"resource discovery mismatch: {resources}")

        initial_heartbeat = node_by_id(base_url, "INTEGRATION-A")["last_heartbeat"]
        wait_for(
            lambda: node_by_id(base_url, "INTEGRATION-A")["last_heartbeat"] != initial_heartbeat,
            8,
            "worker heartbeat acknowledgement",
        )

        # A queued job must survive a Controller restart and be reconciled
        # when the existing workers reconnect.
        request("POST", f"{base_url}/v1/nodes/INTEGRATION-A/pause")
        request("POST", f"{base_url}/v1/nodes/INTEGRATION-B/pause")
        recovery_job = submit_job(
            base_url,
            "sum",
            bytes([9, 10, 11]),
            {"cpu_cores": 1, "ram_gb": 1, "gpu_required": False},
        )
        if recovery_job["status"] != "QUEUED":
            raise RuntimeError(f"expected restart test job to start queued: {recovery_job}")
        controller_process.terminate()
        controller_process.wait(timeout=5)
        processes.remove(controller_process)
        controller_process = subprocess.Popen(
            [str(controller_binary)],
            cwd=CONTROLLER_DIR,
            env=controller_env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        processes.append(controller_process)
        wait_for(lambda: request("GET", f"{base_url}/health")["status"] == "ok", 15, "Controller restart")
        recovered_job = wait_for_job(base_url, recovery_job["id"], "persisted job recovery")
        if recovered_job["status"] != "COMPLETED" or recovered_job["result"]["value"] != 30:
            raise RuntimeError(f"persisted job did not recover: {recovered_job}")

        cli_environment = os.environ.copy()
        cli_environment["NODREN_HTTP_ADDR"] = f"127.0.0.1:{http_port}"
        cli_status = run_cli(controller_binary, cli_environment, "status")
        if (
            cli_status.returncode != 0
            or "Controller     ONLINE" not in cli_status.stdout
            or "Workers        2" not in cli_status.stdout
        ):
            raise RuntimeError(f"CLI status failed: {cli_status.returncode} {cli_status.stdout} {cli_status.stderr}")

        cli_devices = run_cli(controller_binary, cli_environment, "devices")
        if (
            cli_devices.returncode != 0
            or "INTEGRATION-A" not in cli_devices.stdout
            or "CPU=0/2" not in cli_devices.stdout
            or "RAM=0/4GB" not in cli_devices.stdout
        ):
            raise RuntimeError(f"CLI devices failed: {cli_devices.returncode} {cli_devices.stdout} {cli_devices.stderr}")

        cli_info = run_cli(controller_binary, cli_environment, "info", "INTEGRATION-A")
        if cli_info.returncode != 0 or "State        READY" not in cli_info.stdout:
            raise RuntimeError(f"CLI info failed: {cli_info.returncode} {cli_info.stdout} {cli_info.stderr}")

        cli_ping = run_cli(controller_binary, cli_environment, "ping", "INTEGRATION-A")
        if (
            cli_ping.returncode != 0
            or "Reachability REACHABLE" not in cli_ping.stdout
            or "API latency" not in cli_ping.stdout
        ):
            raise RuntimeError(f"CLI ping failed: {cli_ping.returncode} {cli_ping.stdout} {cli_ping.stderr}")

        cli_sum = run_cli(controller_binary, cli_environment, "run", "sum", "1", "2", "3", "4", "5")
        if cli_sum.returncode != 0 or "State        COMPLETED" not in cli_sum.stdout or "Result       15" not in cli_sum.stdout:
            raise RuntimeError(f"CLI sum failed: {cli_sum.returncode} {cli_sum.stdout} {cli_sum.stderr}")

        cli_dot = run_cli(controller_binary, cli_environment, "run", "dot_product", "1,2,3", "4,5,6")
        if cli_dot.returncode != 0 or "Result       32" not in cli_dot.stdout:
            raise RuntimeError(f"CLI dot_product failed: {cli_dot.returncode} {cli_dot.stdout} {cli_dot.stderr}")

        small_requirements = {"cpu_cores": 1, "ram_gb": 1, "gpu_required": False}
        process_executable, process_arguments = (
            ("cmd.exe", ["/C", "echo %NODREN_TEST_VALUE%"]) if os.name == "nt"
            else ("sh", ["-c", "printf %s \"$NODREN_TEST_VALUE\""])
        )
        process_task = submit_task(
            base_url,
            {
                "type": "PROCESS",
                "version": "1",
                "executable": process_executable,
                "arguments": process_arguments,
                "environment": {"NODREN_TEST_VALUE": "process-ok"},
                "requirements": small_requirements,
                "stdout_limit_bytes": 4096,
                "stderr_limit_bytes": 4096,
            },
        )
        process_result = wait_for(
            lambda: (
                state
                if (state := request("GET", f"{base_url}/v1/tasks/{process_task['id']}"))["status"]
                in {"COMPLETED", "FAILED", "TIMED_OUT"}
                else None
            ),
            15,
            "general process task completion",
        )
        if (
            process_result["status"] != "COMPLETED"
            or base64.b64decode(process_result["execution_result"]["stdout_base64"]).decode().strip() != "process-ok"
            or process_result["execution_result"].get("exit_code") != 0
        ):
            raise RuntimeError(f"general process execution failed: {process_result}")

        cli_unknown = run_cli(controller_binary, cli_environment, "run", "unknown_workload", "payload")
        if (
            cli_unknown.returncode == 0
            or "unsupported_workload" not in cli_unknown.stderr
            or "unsupported workload" not in cli_unknown.stderr
        ):
            raise RuntimeError(f"CLI unknown workload failed: {cli_unknown.returncode} {cli_unknown.stdout} {cli_unknown.stderr}")

        cli_after_failure = run_cli(controller_binary, cli_environment, "run", "xor", "1", "2", "3")
        if cli_after_failure.returncode != 0 or "Result       0" not in cli_after_failure.stdout:
            raise RuntimeError(f"CLI recovery workload failed: {cli_after_failure.returncode} {cli_after_failure.stdout} {cli_after_failure.stderr}")

        cli_malformed = run_cli(controller_binary, cli_environment, "run", "dot_product", "1,2", "3")
        if cli_malformed.returncode == 0 or "equal length" not in cli_malformed.stderr:
            raise RuntimeError(f"CLI malformed workload failed: {cli_malformed.returncode} {cli_malformed.stdout} {cli_malformed.stderr}")

        unavailable_environment = cli_environment.copy()
        unavailable_environment["NODREN_CONTROLLER_URL"] = f"http://127.0.0.1:{free_port()}"
        cli_unavailable = run_cli(controller_binary, unavailable_environment, "status")
        if cli_unavailable.returncode == 0 or "Controller unreachable" not in cli_unavailable.stderr:
            raise RuntimeError(f"CLI unavailable-controller handling failed: {cli_unavailable.returncode} {cli_unavailable.stdout} {cli_unavailable.stderr}")

        concurrent_requirements = {"cpu_cores": 3, "ram_gb": 2, "gpu_required": False}
        concurrent_payload = bytes([1]) * 3_000_000
        with ThreadPoolExecutor(max_workers=2) as pool:
            concurrent_futures = [
                pool.submit(submit_job, base_url, "sum", concurrent_payload, concurrent_requirements)
                for _ in range(2)
            ]
            concurrent_jobs = [future.result() for future in concurrent_futures]
        concurrent_results = [
            wait_for_job(base_url, job["id"], "concurrent workload completion", timeout=45)
            for job in concurrent_jobs
        ]
        if any(
            result["status"] != "COMPLETED"
            or result["result"]["value"] != len(concurrent_payload)
            or result.get("node_id") != "INTEGRATION-B"
            for result in concurrent_results
        ):
            raise RuntimeError(f"unexpected concurrent workload results: {concurrent_results}")

        completed = submit_and_wait(
            base_url,
            "sum",
            bytes([1, 2, 3, 4, 5]),
            "small sum workload",
            small_requirements,
        )
        if (
            completed["status"] != "COMPLETED"
            or completed["result"]["value"] != 15
            or completed.get("node_id") != "INTEGRATION-A"
            or completed["result"].get("node_id") != "INTEGRATION-A"
        ):
            raise RuntimeError(f"unexpected job result: {completed}")

        adaptive = submit_and_wait(
            base_url,
            "sum",
            bytes([1]) * 1024,
            "capacity-aware distributed sum",
            small_requirements,
        )
        units_by_worker = {}
        for partition in adaptive.get("partitions", []):
            if partition["state"] != "COMPLETED":
                raise RuntimeError(f"distributed partition did not complete: {adaptive}")
            units_by_worker[partition.get("node_id", "")] = units_by_worker.get(partition.get("node_id", ""), 0) + partition["units"]
        if (
            adaptive["status"] != "COMPLETED"
            or adaptive["result"]["value"] != 1024
            or adaptive["distribution"]["total_partitions"] <= 1
            or units_by_worker.get("INTEGRATION-B", 0) <= units_by_worker.get("INTEGRATION-A", 0)
            or set(adaptive.get("node_ids", [])) != {"INTEGRATION-A", "INTEGRATION-B"}
        ):
            raise RuntimeError(f"capacity-aware distribution was not reflected in job state: {adaptive}")

        xor_payload = bytes((index % 11) for index in range(1024))
        expected_xor = 0
        for value in xor_payload:
            expected_xor ^= value
        distributed_xor = submit_and_wait(
            base_url,
            "xor",
            xor_payload,
            "distributed xor workload",
            small_requirements,
        )
        if (
            distributed_xor["status"] != "COMPLETED"
            or distributed_xor["result"]["value"] != expected_xor
            or len(distributed_xor.get("node_ids", [])) != 2
            or distributed_xor["distribution"]["total_partitions"] <= 1
        ):
            raise RuntimeError(f"distributed xor failed: {distributed_xor}")

        vector_count = 128
        left_vector = list(range(1, vector_count + 1))
        right_vector = [2] * vector_count
        dot_large_payload = (
            struct.pack("<I", vector_count)
            + struct.pack("<" + "i" * vector_count, *left_vector)
            + struct.pack("<" + "i" * vector_count, *right_vector)
        )
        distributed_dot = submit_and_wait(
            base_url,
            "dot_product",
            dot_large_payload,
            "distributed dot_product workload",
            small_requirements,
        )
        if (
            distributed_dot["status"] != "COMPLETED"
            or distributed_dot["result"]["value"] != sum(left * right for left, right in zip(left_vector, right_vector))
            or len(distributed_dot.get("node_ids", [])) != 2
            or distributed_dot["distribution"]["total_partitions"] <= 1
        ):
            raise RuntimeError(f"distributed dot_product failed: {distributed_dot}")

        manual_job = request(
            "POST",
            f"{base_url}/v1/jobs",
            {
                "command": "sum",
                "priority": 50,
                "requirements": small_requirements,
                "payload_base64": base64.b64encode(bytes([1]) * 512).decode(),
                "distribution_mode": "manual",
                "manual_allocations": {"INTEGRATION-A": 100},
            },
        )
        manual_result = wait_for_job(base_url, manual_job["id"], "manual distribution completion")
        if (
            manual_result["status"] != "COMPLETED"
            or manual_result["result"]["value"] != 512
            or manual_result.get("node_ids") != ["INTEGRATION-A"]
            or manual_result["distribution"]["mode"] != "manual"
        ):
            raise RuntimeError(f"manual distribution failed: {manual_result}")

        dot_payload = struct.pack("<I6i", 3, 1, 2, 3, 4, 5, 6)
        high_requirements = {"cpu_cores": 6, "ram_gb": 2, "gpu_required": False}
        dot_result = submit_and_wait(
            base_url,
            "dot_product",
            dot_payload,
            "high-resource dot_product workload",
            high_requirements,
        )
        if (
            dot_result["status"] != "COMPLETED"
            or dot_result["result"]["value"] != 32
            or dot_result.get("node_id") != "INTEGRATION-B"
            or dot_result["result"].get("node_id") != "INTEGRATION-B"
        ):
            raise RuntimeError(f"unexpected dot_product result: {dot_result}")

        xor_result = submit_and_wait(
            base_url,
            "xor",
            bytes([1, 2, 3]),
            "xor workload",
            small_requirements,
        )
        if xor_result["status"] != "COMPLETED" or xor_result["result"]["value"] != 0:
            raise RuntimeError(f"unexpected xor result: {xor_result}")

        # Verify non-partitionable override executes as single task and completes successfully
        non_part_job = request(
            "POST",
            f"{base_url}/v1/jobs",
            {
                "command": "sum",
                "priority": 50,
                "requirements": small_requirements,
                "payload_base64": base64.b64encode(bytes([10, 20, 30])).decode(),
                "partitionable": False,
            },
        )
        non_part_result = wait_for_job(base_url, non_part_job["id"], "non-partitionable workload completion")
        if (
            non_part_result["status"] != "COMPLETED"
            or non_part_result["result"]["value"] != 60
            or non_part_result["distribution"]["partitionable"] is not False
            or non_part_result["distribution"]["total_partitions"] != 1
        ):
            raise RuntimeError(f"non-partitionable workload failed: {non_part_result}")

        # Verify controller resource accounting returns to zero across all workers
        for node in request("GET", f"{base_url}/v1/nodes"):
            if node["allocated_cpu_cores"] != 0 or node["allocated_ram_gb"] != 0:
                raise RuntimeError(f"worker resources were not completely released: {node}")

        impossible = submit_and_wait(
            base_url,
            "sum",
            bytes([1]),
            "impossible resource workload",
            {"cpu_cores": 128, "ram_gb": 1, "gpu_required": False},
        )
        if (
            impossible["status"] != "FAILED"
            or impossible.get("node_id")
            or impossible.get("result", {}).get("error_code") != "resource_requirements"
            or impossible.get("result", {}).get("node_id")
        ):
            raise RuntimeError(f"unexpected impossible-resource result: {impossible}")
        if any(node["state"] != "READY" for node in request("GET", f"{base_url}/v1/nodes")):
            raise RuntimeError("workers became unhealthy after impossible job")

        recovered = submit_and_wait(
            base_url,
            "sum",
            bytes([10, 20, 30]),
            "workload after failed submissions",
            high_requirements,
        )
        if (
            recovered["status"] != "COMPLETED"
            or recovered["result"]["value"] != 60
            or recovered.get("node_id") != "INTEGRATION-B"
        ):
            raise RuntimeError(f"worker did not recover after failed workload: {recovered}")

        worker_a.terminate()
        worker_a.wait(timeout=5)
        wait_for(
            lambda: node_by_id(base_url, "INTEGRATION-A")["state"] == "LOST",
            10,
            "controller worker disconnect detection",
        )

        queued_after_loss = request(
            "POST",
            f"{base_url}/v1/jobs",
            {
                "command": "sum",
                "priority": 50,
                "requirements": {"cpu_cores": 1, "ram_gb": 1, "gpu_required": False},
                "payload_base64": base64.b64encode(bytes([4, 5])).decode(),
            },
        )

        remaining_worker = submit_and_wait(
            base_url,
            "sum",
            bytes([7, 8]),
            "remaining worker after loss",
            high_requirements,
        )
        if (
            remaining_worker["status"] != "COMPLETED"
            or remaining_worker["result"]["value"] != 15
            or remaining_worker.get("node_id") != "INTEGRATION-B"
        ):
            raise RuntimeError(f"remaining worker did not execute workload: {remaining_worker}")
        queued_state = wait_for_job(base_url, queued_after_loss["id"], "job after worker loss")
        if queued_state["status"] != "COMPLETED" or queued_state.get("node_id") == "INTEGRATION-A":
            raise RuntimeError(f"lost worker retained a new assignment: {queued_state}")

        reconnected_worker = subprocess.Popen(
            [
                str(worker_binary),
                f"--controller=127.0.0.1:{tcp_port}",
                "--id=INTEGRATION-A",
                "--cpu-cores=2",
                "--ram-gb=4",
            ],
            cwd=WORKER_DIR,
            env=worker_env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        processes.append(reconnected_worker)
        nodes = wait_for(
            lambda: (
                nodes
                if len(nodes := request("GET", f"{base_url}/v1/nodes")) == 2
                and {node["info"]["id"] for node in nodes}
                == {"INTEGRATION-A", "INTEGRATION-B"}
                and all(node["state"] == "READY" for node in nodes)
                else None
            ),
            15,
            "worker reconnect and duplicate identity prevention",
        )

        post_reconnect = submit_and_wait(
            base_url,
            "sum",
            bytes([7, 8]),
            "post-reconnect workload",
            small_requirements,
        )
        if (
            post_reconnect["status"] != "COMPLETED"
            or post_reconnect["result"]["value"] != 15
            or post_reconnect.get("node_id") != "INTEGRATION-A"
        ):
            raise RuntimeError(f"unexpected post-reconnect result: {post_reconnect}")

        print(
            "PASS: two-worker resource discovery, concurrent capacity assignments, "
            "deterministic placement, sum=15, xor=0, dot_product=32, "
            "unsupported/malformed failures, "
            "impossible-job handling, worker loss, remaining-worker recovery, same-ID "
            f"reconnect, and post-reconnect placement on {post_reconnect['node_id']}"
        )
        return 0
    except Exception as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.terminate()
        for process in reversed(processes):
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        temporary_directory.cleanup()


if __name__ == "__main__":
    raise SystemExit(main())
