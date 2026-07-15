#!/usr/bin/env python3
"""Build tgfctl and forward-test all profiles as external module consumers."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


CASES = (
    ("single", "none", "tcp", "local"),
    ("distributed", "redis", "all", "compose"),
    ("http-rest", "none", "none", "local"),
    ("http-rpc", "none", "none", "k8s"),
)


def run(command: list[str], cwd: Path, env: dict[str, str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(command, cwd=cwd, env=env, check=True, text=True, capture_output=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--tgf-version", default="latest")
    parser.add_argument("--go", default=shutil.which("go") or "go")
    args = parser.parse_args()

    repo = args.repo.resolve()
    env = os.environ.copy()
    env.pop("GOWORK", None)
    with tempfile.TemporaryDirectory(prefix="tgf-forward-") as raw:
        root = Path(raw).resolve()
        suffix = ".exe" if os.name == "nt" else ""
        binary = root / f"tgfctl{suffix}"
        run([args.go, "build", "-o", str(binary), "./cmd/tgfctl"], repo, env)

        results: list[dict[str, str]] = []
        for profile, data, protocol, deploy in CASES:
            destination = root / profile
            command = [
                str(binary), "init",
                "--module", f"github.com/tgf-forward/{profile}",
                "--dir", str(destination),
                "--profile", profile,
                "--data", data,
                "--protocol", protocol,
                "--deploy", deploy,
                "--security", "external",
                "--tgf-version", args.tgf_version,
                "--json",
            ]
            created = run(command, root, env)
            payload = json.loads(created.stdout)
            if not payload.get("tgfVersion"):
                raise RuntimeError(f"{profile}: missing resolved tgfVersion")
            go_mod = (destination / "go.mod").read_text(encoding="utf-8")
            if "replace github.com/thkhxm/tgf/v2" in go_mod or " latest" in go_mod:
                raise RuntimeError(f"{profile}: unpinned or replaced tgf dependency")
            run([str(binary), "verify", "--dir", str(destination)], root, env)
            results.append({"profile": profile, "version": payload["tgfVersion"]})

        # A project-owned workspace is valid and must not require GOWORK=off.
        own_workspace = root / "http-rest"
        run([args.go, "work", "init", "."], own_workspace, env)
        run([str(binary), "verify", "--dir", str(own_workspace)], root, env)
        print(json.dumps({"status": "PASS", "results": results}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except subprocess.CalledProcessError as error:
        print(error.stdout, file=sys.stderr)
        print(error.stderr, file=sys.stderr)
        raise
