from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum
from subprocess import CompletedProcess, TimeoutExpired, run


class ToolState(StrEnum):
    SUCCESS = 'success'
    FAILED = 'failed'
    MISSING = 'missing'
    EXECUTION_FAILED = 'execution_failed'
    TIMED_OUT = 'timed_out'


@dataclass(frozen=True, slots=True)
class ToolEvidence:
    state: ToolState
    return_code: int | None
    stdout: str
    stderr: str


def run_tool(command: tuple[str, ...], timeout_seconds: float) -> ToolEvidence:
    try:
        completed: CompletedProcess[str] = run(  # noqa: S603
            command,
            capture_output=True,
            check=False,
            text=True,
            timeout=timeout_seconds,
        )
    except FileNotFoundError as error:
        return ToolEvidence(ToolState.MISSING, None, '', str(error))
    except TimeoutExpired:
        return ToolEvidence(ToolState.TIMED_OUT, None, '', '')
    except OSError as error:
        return ToolEvidence(ToolState.EXECUTION_FAILED, None, '', str(error))
    tool_state = ToolState.SUCCESS if completed.returncode == 0 else ToolState.FAILED
    return ToolEvidence(tool_state, completed.returncode, completed.stdout, completed.stderr)
