#!/usr/bin/env python3
"""Static contract checks for the tgf-server-dev Skill."""

from __future__ import annotations

import sys
from pathlib import Path


def main() -> int:
    root = Path(__file__).resolve().parents[1]
    skill = (root / "SKILL.md").read_text(encoding="utf-8")
    openai = (root / "agents" / "openai.yaml").read_text(encoding="utf-8")

    required = {
        "deterministic generator": "tgfctl",
        "init command": "tgfctl init",
        "provenance command": "tgfctl verify",
        "public module": "github.com/thkhxm/tgf/v2",
        "latest policy": "@latest",
        "workspace policy": "go.work",
        "bounded repair": "最多修复 3 轮",
        "framework signal ownership": "框架统一处理 `SIGINT/SIGTERM`",
    }
    errors = [f"missing {name}: {needle}" for name, needle in required.items() if needle not in skill]
    if "$tgf-server-dev" not in openai:
        errors.append("agents/openai.yaml default_prompt must mention $tgf-server-dev")
    if "allow_implicit_invocation: true" not in openai:
        errors.append("agents/openai.yaml must enable implicit invocation")
    if (root / "templates").exists() and any(path.is_file() for path in (root / "templates").rglob("*")):
        errors.append("Skill templates/ must not exist; tgfctl embedded templates are authoritative")
    if "go.mod" in skill and "本地 `replace`" not in skill:
        errors.append("Skill must explicitly reject local replace")

    for path in root.rglob("*"):
        if path.is_file() and path.suffix in {".md", ".yaml", ".py"}:
            if path.resolve() == Path(__file__).resolve():
                continue
            text = path.read_text(encoding="utf-8")
            if "{{PROJECT_NAME}}" in text or "{{LOGIN_TOKEN_SECRET}}" in text:
                errors.append(f"legacy template placeholder remains: {path.relative_to(root)}")

    if errors:
        for error in errors:
            print(f"ERROR: {error}")
        return 1
    print("tgf-server-dev static contract: PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())
