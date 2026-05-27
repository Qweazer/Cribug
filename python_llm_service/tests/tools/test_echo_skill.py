"""Tests for the EchoTool (echo_skill).

Tests metadata correctness and execution behavior for the built-in echo tool.
"""

import asyncio
import pytest
from typing import Any, Dict

from llm_service.tools.base import ToolParameterType
from llm_service.tools.builtin.echo_skill import EchoTool


class TestEchoTool:
    """Tests for EchoTool."""

    def setup_method(self):
        """Create a fresh EchoTool instance for each test."""
        self.tool = EchoTool()

    # ------------------------------------------------------------------
    # Metadata tests
    # ------------------------------------------------------------------

    def test_metadata_name(self):
        """The registry name must be 'echo_skill'."""
        metadata = self.tool._get_metadata()
        assert metadata.name == "echo_skill"

    def test_metadata_execution_mode(self):
        """execution_mode must be 'python_inline'."""
        metadata = self.tool._get_metadata()
        assert metadata.execution_mode == "python_inline"

    def test_metadata_requires_sandbox(self):
        """requires_sandbox must be False."""
        metadata = self.tool._get_metadata()
        assert metadata.requires_sandbox is False

    def test_metadata_requires_llm(self):
        """requires_llm must be False."""
        metadata = self.tool._get_metadata()
        assert metadata.requires_llm is False

    def test_metadata_risk_level(self):
        """risk_level must be 'low'."""
        metadata = self.tool._get_metadata()
        assert metadata.risk_level == "low"

    # ------------------------------------------------------------------
    # Parameter tests
    # ------------------------------------------------------------------

    def test_parameters_required_text(self):
        """text must be a required string parameter."""
        params = self.tool._get_parameters()
        param_map = {p.name: p for p in params}

        assert "text" in param_map
        assert param_map["text"].type == ToolParameterType.STRING
        assert param_map["text"].required is True

    def test_parameters_optional_uppercase(self):
        """uppercase must be an optional boolean with default False."""
        params = self.tool._get_parameters()
        param_map = {p.name: p for p in params}

        assert "uppercase" in param_map
        assert param_map["uppercase"].type == ToolParameterType.BOOLEAN
        assert param_map["uppercase"].required is False
        assert param_map["uppercase"].default is False

    # ------------------------------------------------------------------
    # Execution tests
    # ------------------------------------------------------------------

    def test_execute_basic(self):
        """execute() returns the echoed text and its length."""
        result = asyncio.run(self.tool.execute(None, None, text="hello"))

        assert result.success is True
        assert result.output == {"text": "hello", "length": 5}

    def test_execute_uppercase(self):
        """With uppercase=True, the result text is uppercased."""
        result = asyncio.run(self.tool.execute(None, None, text="hello", uppercase=True))

        assert result.success is True
        assert result.output == {"text": "HELLO", "length": 5}

    def test_execute_uppercase_default_false(self):
        """uppercase defaults to False (no transformation)."""
        result = asyncio.run(self.tool.execute(None, None, text="Hello World"))

        assert result.success is True
        assert result.output == {"text": "Hello World", "length": 11}

    def test_execution_time_ms_set(self):
        """The execution_time_ms field must be populated."""
        result = asyncio.run(self.tool.execute(None, None, text="hello"))

        assert result.success is True
        assert result.execution_time_ms is not None
        assert isinstance(result.execution_time_ms, int)
        assert result.execution_time_ms >= 0

    def test_execute_empty_string(self):
        """Empty string should work correctly."""
        result = asyncio.run(self.tool.execute(None, None, text=""))

        assert result.success is True
        assert result.output == {"text": "", "length": 0}

    def test_missing_required_text_raises(self):
        """Omitting required 'text' should raise ValueError."""
        with pytest.raises(ValueError, match="Missing required parameter.*text"):
            asyncio.run(self.tool.execute(None, None))
