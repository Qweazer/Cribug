"""Tests for llm_summary_skill"""
import asyncio
import os

import pytest
from llm_service.tools.builtin.llm_summary_skill import LLMSummaryTool


class TestLLMSummaryTool:
    def setup_method(self):
        self.tool = LLMSummaryTool()

    def test_get_metadata(self):
        metadata = self.tool._get_metadata()
        assert metadata.name == "llm_summary_skill"
        assert metadata.execution_mode == "python_llm"
        assert metadata.requires_sandbox is False
        assert metadata.requires_llm is True
        assert metadata.risk_level == "medium"

    def test_get_parameters(self):
        params = self.tool._get_parameters()
        param_names = [p.name for p in params]
        assert "text" in param_names
        assert "max_length" in param_names
        assert "style" in param_names
        assert "language" in param_names
        assert "context" in param_names

    def test_empty_text_rejected(self):
        result = asyncio.run(self.tool.execute(None, None, text=""))
        assert result.success is False
        assert "empty" in result.error.lower()

    def test_missing_api_key_clear(self):
        """Test that with no API keys configured, proper error is returned."""
        saved = {}
        for key in ("OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY"):
            saved[key] = os.environ.get(key)
            os.environ.pop(key, None)

        try:
            result = asyncio.run(self.tool.execute(None, None, text="test"))
            assert result.success is False
            assert any(w in result.error.lower() for w in ("api", "config", "key"))
        finally:
            for key, val in saved.items():
                if val is not None:
                    os.environ[key] = val
                else:
                    os.environ.pop(key, None)

    def test_invalid_style(self):
        with pytest.raises(ValueError):
            asyncio.run(self.tool.execute(None, None, text="Hello world", style="invalid_style"))

    def test_text_too_large(self):
        large_text = "x" * 100000
        result = asyncio.run(self.tool.execute(None, None, text=large_text))
        assert result.success is False
        assert "large" in result.error.lower()

    def test_execution_time_set(self):
        result = asyncio.run(self.tool.execute(None, None, text="test"))
        assert result.execution_time_ms is not None

    def test_build_summary_prompt_with_context(self):
        prompt = self.tool._build_summary_prompt(
            text="Hello world",
            style="concise",
            language="en",
            max_length=100,
            context="Some retrieved context",
        )
        assert "Some retrieved context" in prompt
        assert "Use the following retrieved context" in prompt
        assert "Hello world" in prompt
        assert "Summary:" in prompt

    def test_build_summary_prompt_without_context(self):
        prompt = self.tool._build_summary_prompt(
            text="Hello world",
            style="concise",
            language="en",
            max_length=100,
        )
        assert "Hello world" in prompt
        assert "Use the following retrieved context" not in prompt
        assert "Summary:" in prompt
