"""Tests for safe_math_skill"""
import asyncio
import pytest
from llm_service.tools.builtin.safe_math_skill import SafeMathTool


class TestSafeMathTool:
    def setup_method(self):
        self.tool = SafeMathTool()

    def test_get_metadata(self):
        metadata = self.tool._get_metadata()
        assert metadata.name == "safe_math_skill"
        assert metadata.execution_mode == "python_inline"
        assert metadata.requires_sandbox is False
        assert metadata.requires_llm is False
        assert metadata.risk_level == "low"

    def test_get_parameters(self):
        params = self.tool._get_parameters()
        param_names = [p.name for p in params]
        assert "operation" in param_names
        assert "a" in param_names
        assert "b" in param_names

    def test_add(self):
        result = asyncio.run(self.tool.execute(None, None, operation="add", a=2, b=3))
        assert result.success is True
        assert result.output["result"] == 5

    def test_subtract(self):
        result = asyncio.run(self.tool.execute(None, None, operation="subtract", a=10, b=3))
        assert result.success is True
        assert result.output["result"] == 7

    def test_multiply(self):
        result = asyncio.run(self.tool.execute(None, None, operation="multiply", a=4, b=5))
        assert result.success is True
        assert result.output["result"] == 20

    def test_divide(self):
        result = asyncio.run(self.tool.execute(None, None, operation="divide", a=10, b=2))
        assert result.success is True
        assert result.output["result"] == 5.0

    def test_divide_by_zero(self):
        result = asyncio.run(self.tool.execute(None, None, operation="divide", a=10, b=0))
        assert result.success is False
        assert result.output is None
        assert result.error is not None

    def test_power_valid(self):
        result = asyncio.run(self.tool.execute(None, None, operation="power", a=2, b=3))
        assert result.success is True
        assert result.output["result"] == 8

    def test_power_exponent_too_large(self):
        result = asyncio.run(self.tool.execute(None, None, operation="power", a=2, b=15))
        assert result.success is False

    def test_power_negative_exponent(self):
        result = asyncio.run(self.tool.execute(None, None, operation="power", a=2, b=-3))
        assert result.success is True
        assert result.output["result"] == 0.125

    def test_unsupported_operation(self):
        with pytest.raises(ValueError):
            asyncio.run(self.tool.execute(None, None, operation="mod", a=10, b=3))

    def test_invalid_arguments_string(self):
        with pytest.raises(ValueError):
            asyncio.run(self.tool.execute(None, None, operation="add", a="not a number", b=3))

    def test_execution_time_set(self):
        result = asyncio.run(self.tool.execute(None, None, operation="add", a=1, b=2))
        assert result.execution_time_ms is not None
