import pytest
import unittest.mock as mock
import tempfile
import pathlib
import os
import asyncio

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from limen.mcp_server.limen_mcp import mcp, ask_gemma, ask_deepseek

@pytest.mark.asyncio
async def test_ping():
    # Test ping tool
    content, metadata = await mcp.call_tool("ping", {})
    assert len(content) == 1
    assert content[0].text == "pong"
    assert metadata["result"] == "pong"

@pytest.mark.asyncio
async def test_planner():
    # Test planner tool
    task = "Design a new database schema"
    content, metadata = await mcp.call_tool("planner", {"task": task})
    assert len(content) == 1
    assert content[0].text == ask_gemma(task)
    assert metadata["result"] == ask_gemma(task)

@pytest.mark.asyncio
async def test_coder():
    # Test coder tool
    task = "Write a python function to add two numbers"
    content, metadata = await mcp.call_tool("coder", {"task": task})
    assert len(content) == 1
    assert content[0].text == ask_deepseek(task)
    assert metadata["result"] == ask_deepseek(task)

@pytest.mark.asyncio
async def test_review_file():
    # Test review_file tool using a temporary file
    with tempfile.NamedTemporaryFile(mode="w", suffix=".txt", delete=False, encoding="utf-8") as f:
        f.write("Hello, World!")
        temp_path = f.name

    try:
        content, metadata = await mcp.call_tool("review_file", {"path": temp_path})
        assert len(content) == 1
        assert content[0].text == "Hello, World!"
        assert metadata["result"] == "Hello, World!"
    finally:
        os.remove(temp_path)

@pytest.mark.asyncio
async def test_git_diff():
    # Mock subprocess.check_output to avoid dependencies on actual git diff state
    with mock.patch("subprocess.check_output") as mock_diff:
        mock_diff.return_value = "diff --git a/file.txt b/file.txt\n+added line"
        
        content, metadata = await mcp.call_tool("git_diff", {})
        assert len(content) == 1
        assert "diff --git a/file.txt" in content[0].text
        assert metadata["result"] == "diff --git a/file.txt b/file.txt\n+added line"
        mock_diff.assert_called_once_with(["git", "diff"], text=True)

@pytest.mark.asyncio
async def test_run_tests():
    # Mock subprocess.run for run_tests tool to avoid recursive calls/errors
    with mock.patch("subprocess.run") as mock_run:
        mock_process = mock.Mock()
        mock_process.stdout = "=== 1 passed in 0.05s ==="
        mock_process.stderr = ""
        mock_run.return_value = mock_process
        
        content, metadata = await mcp.call_tool("run_tests", {})
        assert len(content) == 1
        assert "1 passed" in content[0].text
        assert metadata["result"] == "=== 1 passed in 0.05s ==="
        mock_run.assert_called_once_with(["pytest", "-q"], capture_output=True, text=True)

@pytest.mark.asyncio
async def test_mcp_server_integration():
    # Run the server in a separate process and communicate with it using stdio transport
    server_params = StdioServerParameters(
        command="python",
        args=["src/limen/mcp_server/limen_mcp.py"],
        env=os.environ.copy()
    )

    async with stdio_client(server_params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            
            # List tools and check that we got the expected ones
            tools = await session.list_tools()
            tool_names = [tool.name for tool in tools.tools]
            assert "ping" in tool_names
            assert "planner" in tool_names
            assert "coder" in tool_names
            assert "review_file" in tool_names
            assert "git_diff" in tool_names
            assert "run_tests" in tool_names
            
            # Call the ping tool via the running server process
            res = await session.call_tool("ping", arguments={})
            assert len(res.content) == 1
            assert res.content[0].type == "text"
            assert res.content[0].text == "pong"
