#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
import uuid


def run(args: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(args, capture_output=True, text=True, check=False)


def main() -> int:
    parser = argparse.ArgumentParser(description="Run the disposable PostgreSQL DB upgrade smoke harness.")
    parser.add_argument("--image", default="public.ecr.aws/docker/library/postgres:16-bookworm")
    args = parser.parse_args()

    container_name = f"vectra-db-upgrade-smoke-{uuid.uuid4().hex[:12]}"
    database_name = "vectra"
    database_user = "vectra"
    database_password = "vectra-test-password"
    started = False

    try:
        completed = run([
            "docker",
            "run",
            "-d",
            "--rm",
            "--name",
            container_name,
            "-e",
            f"POSTGRES_DB={database_name}",
            "-e",
            f"POSTGRES_USER={database_user}",
            "-e",
            f"POSTGRES_PASSWORD={database_password}",
            "-p",
            "127.0.0.1::5432",
            args.image,
        ])
        container_id = completed.stdout.strip().splitlines()[0] if completed.stdout.strip() else ""
        if completed.returncode != 0 or not container_id:
            raise RuntimeError(f"Failed to start disposable PostgreSQL container. Ensure Docker is running. Docker output: {(completed.stdout + completed.stderr).strip()}")
        started = True

        import time

        deadline = time.time() + 90
        ready = False
        while time.time() < deadline:
            time.sleep(2)
            probe = run(["docker", "exec", container_name, "pg_isready", "-U", database_user, "-d", database_name])
            if probe.returncode == 0:
                ready = True
                break
        if not ready:
            raise RuntimeError("Disposable PostgreSQL did not become ready before timeout.")

        port_probe = run(["docker", "port", container_name, "5432/tcp"])
        match = re.search(r":(\d+)$", port_probe.stdout.strip())
        if port_probe.returncode != 0 or not match:
            raise RuntimeError("Failed to resolve disposable PostgreSQL host port.")
        host_port = match.group(1)

        env = os.environ.copy()
        env["DATABASE_URL"] = f"postgresql://{database_user}:{database_password}@127.0.0.1:{host_port}/{database_name}"
        env["VECTRA_DB_UPGRADE_TEST_ALLOW_RESET"] = "1"
        verify = subprocess.run(["node", "./apps/web/scripts/verify-db-upgrade-path.mjs", "--reset-schema"], env=env, check=False)
        if verify.returncode != 0:
            raise RuntimeError(f"verify-db-upgrade-path.mjs failed with exit code {verify.returncode}")
        return 0
    finally:
        if started:
            subprocess.run(["docker", "rm", "-f", container_name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
