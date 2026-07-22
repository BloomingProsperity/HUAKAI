import shutil
import tempfile
from pathlib import Path
from urllib.parse import urlsplit

from official_runner import (
    ChatPayload,
    CommandSpec,
    ProcessExecutor,
    RunnerConfig,
    RunnerFailure,
    _child_environment,
    _hermes_config,
    _write_private_json,
    execute_process,
)


async def verify_official_tool_surface(
    config: RunnerConfig,
    *,
    executor: ProcessExecutor | None = None,
) -> None:
    config.work_root.mkdir(mode=0o700, parents=True, exist_ok=True)
    config.work_root.chmod(0o700)
    check_root = Path(
        tempfile.mkdtemp(prefix="tool-surface-check-", dir=config.work_root)
    )
    check_root.chmod(0o700)
    try:
        hermes_home = check_root / "hermes-home"
        home = check_root / "home"
        work = check_root / "work"
        runtime = check_root / "runtime"
        for directory in (hermes_home, home, work, runtime):
            directory.mkdir(mode=0o700)

        payload = ChatPayload(
            messages=[],
            model_base_url="https://example.invalid/v1",
            model_api_key="startup-model-surface-check.invalid",
            mcp_token="startup-mcp-surface-check.invalid",
            internal_token_expires_at=1,
            model="startup-surface-check",
        )
        _write_private_json(
            hermes_home / "config.yaml",
            _hermes_config(config, payload, work),
        )
        command = CommandSpec(
            argv=(config.hermes_binary, "tools", "list", "--platform", "cli"),
            cwd=work,
            env=_child_environment(
                hermes_home=hermes_home,
                home=home,
                runtime=runtime,
                internal_host=urlsplit(config.mcp_url).hostname or "",
                egress_proxy_url=config.egress_proxy_url,
            ),
            timeout_seconds=15,
            terminate_grace_seconds=config.terminate_grace_seconds,
            max_stdout_bytes=262_144,
            max_stderr_bytes=65_536,
        )
        result = await (executor or execute_process)(command)
        if (
            result.returncode != 0
            or result.stdout_truncated
            or result.stderr_truncated
            or not tool_surface_is_restricted(result.stdout)
        ):
            raise RunnerFailure("tool_surface_check_failed")
    finally:
        shutil.rmtree(check_root, ignore_errors=True)


def tool_surface_is_restricted(output: bytes) -> bool:
    try:
        text = output.decode("utf-8", errors="strict")
    except UnicodeDecodeError:
        return False

    section = ""
    saw_builtin_header = False
    saw_mcp_header = False
    builtin_rows: list[str] = []
    mcp_names: list[str] = []

    for line in text.splitlines():
        value = line.strip()
        if not value:
            continue
        if value == "Built-in toolsets (cli):":
            if saw_builtin_header or saw_mcp_header:
                return False
            saw_builtin_header = True
            section = "builtin"
            continue
        if value == "MCP servers:":
            if not saw_builtin_header or saw_mcp_header:
                return False
            saw_mcp_header = True
            section = "mcp"
            continue
        if section == "builtin":
            builtin_rows.append(value)
            continue
        if section == "mcp":
            mcp_names.append(value.split(maxsplit=1)[0])
            continue
        return False

    return (
        saw_builtin_header
        and saw_mcp_header
        and bool(builtin_rows)
        and all("disabled" in row.split() for row in builtin_rows)
        and mcp_names == ["huakai"]
    )
