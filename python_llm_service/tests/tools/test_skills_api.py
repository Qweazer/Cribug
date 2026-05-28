"""Tests for the Skills API (FastAPI router in api/skills.py).

Tests all REST endpoints: list, metadata, execute, audit, and error cases.
Uses httpx AsyncClient with ASGITransport to simulate HTTP requests.
"""
import os
import pytest
from typing import Any, Dict, List

from httpx import ASGITransport, AsyncClient

# Module-level mark makes all tests asyncio-aware in strict mode
pytestmark = pytest.mark.asyncio

# Ensure no API keys are set for LLM summary tests
_api_key_vars = ["OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY",
                  "MINIMAX_API_KEY", "LLM_API_KEY"]
_saved_env = {k: os.environ.pop(k, None) for k in _api_key_vars}

from app import app

# Restore env after importing app (which triggers register_cribug_tools)
for k, v in _saved_env.items():
    if v is not None:
        os.environ[k] = v


class TestSkillsAPI:
    """Tests for the /tools REST API."""

    _transport = ASGITransport(app=app)

    async def _client(self):
        return AsyncClient(transport=self._transport, base_url="http://test")

    # ------------------------------------------------------------------
    # GET /tools - list all skills
    # ------------------------------------------------------------------

    async def test_list_skills(self):
        """GET /tools returns sorted list of all registered skill names."""
        async with await self._client() as client:
            response = await client.get("/tools")
        assert response.status_code == 200
        data: List[str] = response.json()
        assert isinstance(data, list)
        assert "echo_skill" in data
        assert "safe_math_skill" in data
        assert "llm_summary_skill" in data
        # Verify sorted order
        assert data == sorted(data)

    # ------------------------------------------------------------------
    # GET /tools/audit - audit stats
    # ------------------------------------------------------------------

    async def test_audit_stats(self):
        """GET /tools/audit returns expected structure with zero counters."""
        async with await self._client() as client:
            response = await client.get("/tools/audit")
        assert response.status_code == 200
        data: Dict[str, Any] = response.json()
        assert data["total_calls"] == 0
        assert data["by_skill"] == {}
        assert data["error_rate"] == 0.0

    # ------------------------------------------------------------------
    # GET /tools/{skill_name} - metadata
    # ------------------------------------------------------------------

    async def test_get_skill_metadata(self):
        """GET /tools/echo_skill returns metadata with execution_mode python_inline."""
        async with await self._client() as client:
            response = await client.get("/tools/echo_skill")
        assert response.status_code == 200
        data: Dict[str, Any] = response.json()
        assert data["name"] == "echo_skill"
        assert data["execution_mode"] == "python_inline"
        assert data["requires_sandbox"] is False
        assert data["requires_llm"] is False
        assert data["risk_level"] == "low"
        assert "parameters" in data

    async def test_get_nonexistent_skill(self):
        """GET /tools/nonexistent returns 404."""
        async with await self._client() as client:
            response = await client.get("/tools/nonexistent_skill_xyz")
        assert response.status_code == 404

    # ------------------------------------------------------------------
    # POST /tools/{skill_name}/execute - execution
    # ------------------------------------------------------------------

    async def test_execute_echo_skill(self):
        """POST /tools/echo_skill/execute with text and uppercase=True returns HELLO."""
        async with await self._client() as client:
            response = await client.post(
                "/tools/echo_skill/execute",
                json={"parameters": {"text": "hello", "uppercase": True}},
            )
        assert response.status_code == 200
        data: Dict[str, Any] = response.json()
        assert data["tool_name"] == "echo_skill"
        assert data["success"] is True
        assert data["output"]["text"] == "HELLO"
        assert data["request_id"] is not None

    async def test_execute_math_skill(self):
        """POST /tools/safe_math_skill/execute with add(2,3) returns result=5."""
        async with await self._client() as client:
            response = await client.post(
                "/tools/safe_math_skill/execute",
                json={"parameters": {"operation": "add", "a": 2, "b": 3}},
            )
        assert response.status_code == 200
        data: Dict[str, Any] = response.json()
        assert data["tool_name"] == "safe_math_skill"
        assert data["success"] is True
        assert data["output"]["result"] == 5.0

    async def test_execute_math_division_by_zero(self):
        """POST /tools/safe_math_skill/execute with divide(5,0) returns error."""
        async with await self._client() as client:
            response = await client.post(
                "/tools/safe_math_skill/execute",
                json={"parameters": {"operation": "divide", "a": 5, "b": 0}},
            )
        assert response.status_code == 200
        data: Dict[str, Any] = response.json()
        assert data["success"] is False
        assert data["error"] is not None

    async def test_execute_llm_summary_no_api_key(self):
        """POST /tools/llm_summary_skill/execute without API key returns error.

        The error message should contain "API" or "config" indicating
        that no LLM configuration was found.
        """
        # Clear API keys for this test
        for k in _api_key_vars:
            os.environ.pop(k, None)

        async with await self._client() as client:
            response = await client.post(
                "/tools/llm_summary_skill/execute",
                json={"parameters": {"text": "Hello world", "style": "concise"}},
            )
        data: Dict[str, Any] = response.json()
        assert data["success"] is False
        assert data["error"] is not None
        error_lower = data["error"].lower()
        assert "api" in error_lower or "config" in error_lower or "key" in error_lower

    async def test_execute_nonexistent_skill(self):
        """POST /tools/nonexistent/execute returns 404."""
        async with await self._client() as client:
            response = await client.post(
                "/tools/nonexistent_skill_xyz/execute",
                json={"parameters": {}},
            )
        assert response.status_code == 404

    async def test_execute_echo_missing_required(self):
        """POST /tools/echo_skill/execute with empty params returns success=False."""
        async with await self._client() as client:
            response = await client.post(
                "/tools/echo_skill/execute",
                json={"parameters": {}},
            )
        assert response.status_code == 200
        data: Dict[str, Any] = response.json()
        assert data["success"] is False
        assert data["error"] is not None
