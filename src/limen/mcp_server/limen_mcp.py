from mcp.server.fastmcp import FastMCP
import requests

mcp = FastMCP("opencode")


# ---------------------------------------------------
# backend wrappers
# ---------------------------------------------------


def ask_gemma(prompt: str) -> str:
    return f"[Gemma]\nPlan:\n{prompt}"


def ask_deepseek(prompt: str) -> str:
    return f"[DeepSeek]\nImplementation:\n{prompt}"


# ---------------------------------------------------
# exposed tools
# ---------------------------------------------------


@mcp.tool()
def ping() -> str:
    return "pong"


@mcp.tool()
def planner(task: str) -> str:
    """Ask Gemma to make a plan."""

    return ask_gemma(task)


@mcp.tool()
def coder(task: str) -> str:
    """Ask DeepSeek to implement something."""

    return ask_deepseek(task)


@mcp.tool()
def review_file(path: str) -> str:
    """Read a file for Gemini."""

    with open(path, encoding="utf8") as f:
        return f.read()


@mcp.tool()
def git_diff() -> str:
    import subprocess

    return subprocess.check_output(["git", "diff"], text=True)


@mcp.tool()
def run_tests() -> str:
    import subprocess

    proc = subprocess.run(["pytest", "-q"], capture_output=True, text=True)

    return proc.stdout + proc.stderr


if __name__ == "__main__":
    mcp.run()
