#!/usr/bin/env python3
"""Fail closed unless rendered PortLoom services pin one exact release version."""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import shutil
import subprocess
import tempfile


def compose_services(path: pathlib.Path, *, interpolate: bool) -> dict[str, dict]:
    with tempfile.TemporaryDirectory(prefix="portloom-compose-contract-") as directory:
        workspace = pathlib.Path(directory)
        compose = workspace / "compose.yml"
        shutil.copyfile(path, compose)
        (workspace / ".env").write_text(
            "TM_ADMIN_TOKEN=release-contract-validation\n"
            "TM_PUBLIC_HOST=release-contract.invalid\n"
        )
        environment = {
            "HOME": os.environ.get("HOME", str(workspace)),
            "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
            "TM_ADMIN_TOKEN": "release-contract-validation",
            "TM_PUBLIC_HOST": "release-contract.invalid",
        }
        command = [
            "docker",
            "compose",
            "--project-directory",
            str(workspace),
            "-f",
            str(compose),
            "config",
            "--format",
            "json",
            "--no-path-resolution",
        ]
        if not interpolate:
            command.append("--no-interpolate")
        try:
            result = subprocess.run(
                command,
                cwd=workspace,
                env=environment,
                capture_output=True,
                text=True,
                check=False,
            )
        except OSError as error:
            raise SystemExit(f"cannot execute Docker Compose: {error}") from error
        if result.returncode != 0:
            raise SystemExit(
                f"Docker Compose rejected {path}: {result.stderr.strip() or result.stdout.strip()}"
            )
        try:
            rendered = json.loads(result.stdout)
            services = rendered["services"]
        except (json.JSONDecodeError, KeyError, TypeError) as error:
            raise SystemExit(f"invalid Docker Compose JSON for {path}: {error}") from error
        if not isinstance(services, dict):
            raise SystemExit(f"invalid Docker Compose services for {path}")
        return services


def verify_compose(path: pathlib.Path, version: str) -> None:
    expected = {
        "state-init": f"ghcr.io/lkhmm520/portloom-sshd:{version}",
        "sshd": f"ghcr.io/lkhmm520/portloom-sshd:{version}",
        "server": f"ghcr.io/lkhmm520/portloom-server:{version}",
    }
    checks = {
        "rendered": compose_services(path, interpolate=True),
        "literal": compose_services(path, interpolate=False),
    }
    for mode, services in checks.items():
        actual = {name: service.get("image") for name, service in services.items()}
        if actual != expected:
            raise SystemExit(f"{mode} Compose images {actual!r} != {expected!r}")
        builders = sorted(
            name for name, service in services.items() if service.get("build") is not None
        )
        if builders:
            raise SystemExit(f"release Compose must not build services locally: {builders!r}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("compose", type=pathlib.Path)
    args = parser.parse_args()
    verify_compose(args.compose.resolve(), args.version)


if __name__ == "__main__":
    main()
