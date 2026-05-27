"""Tests for the base Tool classes.

Tests ToolMetadata, ToolParameter, ToolResult, and the Tool ABC
with its execute() wrapper that handles validation, coercion, and timing.
"""

import pytest
import time
from typing import Any, Dict, List, Optional
from dataclasses import dataclass

# Import the classes under test
from llm_service.tools.base import (
    Tool,
    ToolMetadata,
    ToolParameter,
    ToolResult,
    ToolParameterType,
)


# =============================================================================
# ToolMetadata Tests
# =============================================================================

class TestToolMetadata:
    """Tests for ToolMetadata dataclass."""

    def test_metadata_all_required_fields(self):
        """Test that ToolMetadata accepts all required fields."""
        metadata = ToolMetadata(
            name="test_tool",
            version="1.0.0",
            description="A test tool",
            category="testing",
            author="Test Author",
            execution_mode="python_inline",
            requires_sandbox=False,
            requires_llm=False,
            risk_level="low",
            side_effects=False,
        )

        assert metadata.name == "test_tool"
        assert metadata.version == "1.0.0"
        assert metadata.description == "A test tool"
        assert metadata.category == "testing"
        assert metadata.author == "Test Author"
        assert metadata.execution_mode == "python_inline"
        assert metadata.requires_sandbox is False
        assert metadata.requires_llm is False
        assert metadata.risk_level == "low"
        assert metadata.side_effects is False

    def test_metadata_default_values(self):
        """Test that ToolMetadata has correct default values."""
        metadata = ToolMetadata(
            name="test_tool",
            version="1.0.0",
            description="A test tool",
            category="testing",
        )

        assert metadata.author == "Cribug"
        assert metadata.execution_mode == "python_inline"
        assert metadata.requires_sandbox is False
        assert metadata.requires_llm is False
        assert metadata.risk_level == "low"
        assert metadata.side_effects is False
        assert metadata.timeout_seconds == 30
        assert metadata.rate_limit is None
        assert metadata.cost_per_use == 0.0
        assert metadata.requires_auth is False
        assert metadata.sandboxed is True
        assert metadata.memory_limit_mb == 512
        assert metadata.session_aware is False
        assert metadata.dangerous is False
        assert metadata.input_examples is None

    def test_metadata_with_optional_fields(self):
        """Test ToolMetadata with all optional fields set."""
        metadata = ToolMetadata(
            name="complex_tool",
            version="2.0.0",
            description="A complex tool with all options",
            category="advanced",
            author="Dev Team",
            execution_mode="sandboxed",
            requires_sandbox=True,
            requires_llm=True,
            risk_level="high",
            side_effects=True,
            timeout_seconds=60,
            rate_limit=100,
            cost_per_use=0.05,
            requires_auth=True,
            sandboxed=True,
            memory_limit_mb=1024,
            session_aware=True,
            dangerous=True,
            input_examples=[
                {"name": "Alice", "age": 30},
                {"name": "Bob", "age": 25},
            ],
        )

        assert metadata.timeout_seconds == 60
        assert metadata.rate_limit == 100
        assert metadata.cost_per_use == 0.05
        assert metadata.requires_auth is True
        assert metadata.memory_limit_mb == 1024
        assert metadata.session_aware is True
        assert metadata.dangerous is True
        assert len(metadata.input_examples) == 2


# =============================================================================
# ToolParameter Tests
# =============================================================================

class TestToolParameter:
    """Tests for ToolParameter dataclass."""

    def test_parameter_required_fields(self):
        """Test ToolParameter with required fields only."""
        param = ToolParameter(
            name="input_text",
            type=ToolParameterType.STRING,
            description="The text to process",
            required=True,
        )

        assert param.name == "input_text"
        assert param.type == ToolParameterType.STRING
        assert param.description == "The text to process"
        assert param.required is True
        assert param.default is None
        assert param.enum is None
        assert param.min_value is None
        assert param.max_value is None
        assert param.pattern is None
        assert param.max_length is None

    def test_parameter_with_defaults(self):
        """Test ToolParameter with default values."""
        param = ToolParameter(
            name="count",
            type=ToolParameterType.INTEGER,
            description="Number of items",
            required=False,
            default=10,
        )

        assert param.required is False
        assert param.default == 10

    def test_parameter_with_enum(self):
        """Test ToolParameter with enum constraint."""
        param = ToolParameter(
            name="color",
            type=ToolParameterType.STRING,
            description="Color choice",
            required=True,
            enum=["red", "green", "blue"],
        )

        assert param.enum == ["red", "green", "blue"]

    def test_parameter_with_min_max(self):
        """Test ToolParameter with min/max constraints."""
        param = ToolParameter(
            name="score",
            type=ToolParameterType.FLOAT,
            description="Score value",
            required=True,
            min_value=0.0,
            max_value=100.0,
        )

        assert param.min_value == 0.0
        assert param.max_value == 100.0

    def test_parameter_with_pattern(self):
        """Test ToolParameter with regex pattern."""
        param = ToolParameter(
            name="email",
            type=ToolParameterType.STRING,
            description="Email address",
            required=True,
            pattern=r"^[\w\.-]+@[\w\.-]+\.\w+$",
            max_length=255,
        )

        assert param.pattern == r"^[\w\.-]+@[\w\.-]+\.\w+$"
        assert param.max_length == 255

    def test_parameter_types(self):
        """Test all parameter types."""
        types = [
            ToolParameterType.STRING,
            ToolParameterType.INTEGER,
            ToolParameterType.FLOAT,
            ToolParameterType.BOOLEAN,
            ToolParameterType.ARRAY,
            ToolParameterType.OBJECT,
            ToolParameterType.FILE,
        ]

        for param_type in types:
            param = ToolParameter(
                name="test",
                type=param_type,
                description="Test parameter",
            )
            assert param.type == param_type


# =============================================================================
# ToolResult Tests
# =============================================================================

class TestToolResult:
    """Tests for ToolResult dataclass."""

    def test_result_success(self):
        """Test ToolResult for successful execution."""
        result = ToolResult(
            success=True,
            output={"answer": 42},
        )

        assert result.success is True
        assert result.output == {"answer": 42}
        assert result.error is None
        assert result.metadata is None

    def test_result_failure(self):
        """Test ToolResult for failed execution."""
        result = ToolResult(
            success=False,
            error="Something went wrong",
        )

        assert result.success is False
        assert result.output is None
        assert result.error == "Something went wrong"

    def test_result_with_metadata(self):
        """Test ToolResult with metadata including timing."""
        result = ToolResult(
            success=True,
            output="processed data",
            metadata={"rows_processed": 100},
            execution_time_ms=150,
            tokens_used=500,
        )

        assert result.metadata == {"rows_processed": 100}
        assert result.execution_time_ms == 150
        assert result.tokens_used == 500


# =============================================================================
# Tool ABC Tests
# =============================================================================

class TestTool:
    """Tests for the Tool ABC."""

    def test_tool_metadata_abstract(self):
        """Test that Tool requires _get_metadata implementation."""
        with pytest.raises(TypeError):
            tool = Tool()  # type: ignore

    def test_tool_parameter_validation_abstract(self):
        """Test that Tool requires _get_parameters implementation."""
        with pytest.raises(TypeError):
            tool = Tool()  # type: ignore

    def test_tool_execute_abstract(self):
        """Test that Tool requires _execute_impl implementation."""
        with pytest.raises(TypeError):
            tool = Tool()  # type: ignore

    def test_concrete_tool_class(self):
        """Test creating a concrete Tool implementation."""

        class EchoTool(Tool):
            """A simple echo tool for testing."""

            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="echo",
                    version="1.0.0",
                    description="Echoes the input back",
                    category="utility",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return [
                    ToolParameter(
                        name="message",
                        type=ToolParameterType.STRING,
                        description="Message to echo",
                        required=True,
                    )
                ]

            async def _execute_impl(
                self,
                session_context: Optional[Dict[str, Any]],
                observer: Optional[Any],
                **kwargs,
            ) -> ToolResult:
                message = kwargs.get("message", "")
                return ToolResult(success=True, output=f"Echo: {message}")

        tool = EchoTool()
        assert tool._get_metadata().name == "echo"
        assert len(tool._get_parameters()) == 1

    def test_tool_execute_with_validation(self):
        """Test Tool.execute() wrapper validates required parameters."""

        class StrictTool(Tool):
            """A tool that requires a parameter."""

            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="strict",
                    version="1.0.0",
                    description="A strict tool",
                    category="testing",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return [
                    ToolParameter(
                        name="required_arg",
                        type=ToolParameterType.STRING,
                        description="A required argument",
                        required=True,
                    )
                ]

            async def _execute_impl(
                self,
                session_context: Optional[Dict[str, Any]],
                observer: Optional[Any],
                **kwargs,
            ) -> ToolResult:
                return ToolResult(success=True, output="ok")

        tool = StrictTool()

        # Missing required parameter should raise ValueError
        with pytest.raises(ValueError, match="required_arg"):
            import asyncio
            asyncio.run(tool.execute(None, None))

    def test_tool_execute_sets_timing(self):
        """Test that Tool.execute() sets execution_time_ms."""

        class TimingTool(Tool):
            """A tool that takes some time."""

            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="timing",
                    version="1.0.0",
                    description="A timing tool",
                    category="testing",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return []

            async def _execute_impl(
                self,
                session_context: Optional[Dict[str, Any]],
                observer: Optional[Any],
                **kwargs,
            ) -> ToolResult:
                # Simulate some work
                time.sleep(0.05)  # 50ms
                return ToolResult(success=True, output="done")

        tool = TimingTool()

        import asyncio
        result = asyncio.run(tool.execute(None, None))

        assert result.success is True
        assert result.execution_time_ms is not None
        assert result.execution_time_ms >= 40  # Allow some tolerance

    def test_tool_execute_type_coercion(self):
        """Test that Tool.execute() coerces types."""

        class CoercionTool(Tool):
            """A tool with various parameter types."""

            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="coercion",
                    version="1.0.0",
                    description="A coercion tool",
                    category="testing",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return [
                    ToolParameter(
                        name="count",
                        type=ToolParameterType.INTEGER,
                        description="A count parameter",
                        required=True,
                    ),
                    ToolParameter(
                        name="ratio",
                        type=ToolParameterType.FLOAT,
                        description="A ratio parameter",
                        required=True,
                    ),
                    ToolParameter(
                        name="enabled",
                        type=ToolParameterType.BOOLEAN,
                        description="An enabled parameter",
                        required=True,
                    ),
                ]

            async def _execute_impl(
                self,
                session_context: Optional[Dict[str, Any]],
                observer: Optional[Any],
                **kwargs,
            ) -> ToolResult:
                return ToolResult(
                    success=True,
                    output={
                        "count_type": type(kwargs["count"]).__name__,
                        "ratio_type": type(kwargs["ratio"]).__name__,
                        "enabled_type": type(kwargs["enabled"]).__name__,
                    },
                )

        tool = CoercionTool()

        import asyncio
        result = asyncio.run(tool.execute(
            None,
            None,
            count="42",  # String to int
            ratio="3.14",  # String to float
            enabled="true",  # String to bool
        ))

        assert result.success is True
        # Types should be coerced appropriately
        assert result.output["count_type"] == "int"
        assert result.output["ratio_type"] == "float"
        assert result.output["enabled_type"] == "bool"

    def test_tool_execute_enum_validation(self):
        """Test that Tool.execute() validates enum values."""

        class EnumTool(Tool):
            """A tool with enum parameter."""

            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="enum_tool",
                    version="1.0.0",
                    description="An enum tool",
                    category="testing",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return [
                    ToolParameter(
                        name="size",
                        type=ToolParameterType.STRING,
                        description="Size option",
                        required=True,
                        enum=["small", "medium", "large"],
                    )
                ]

            async def _execute_impl(
                self,
                session_context: Optional[Dict[str, Any]],
                observer: Optional[Any],
                **kwargs,
            ) -> ToolResult:
                return ToolResult(success=True, output=f"size: {kwargs['size']}")

        tool = EnumTool()

        import asyncio

        # Valid enum value should work
        result = asyncio.run(tool.execute(None, None, size="medium"))
        assert result.success is True

        # Invalid enum value should raise ValueError
        with pytest.raises(ValueError, match="size"):
            asyncio.run(tool.execute(None, None, size="huge"))

    def test_tool_execute_min_max_validation(self):
        """Test that Tool.execute() validates min/max values."""

        class RangeTool(Tool):
            """A tool with range constraints."""

            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="range_tool",
                    version="1.0.0",
                    description="A range tool",
                    category="testing",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return [
                    ToolParameter(
                        name="value",
                        type=ToolParameterType.INTEGER,
                        description="A value with range",
                        required=True,
                        min_value=0,
                        max_value=100,
                    )
                ]

            async def _execute_impl(
                self,
                session_context: Optional[Dict[str, Any]],
                observer: Optional[Any],
                **kwargs,
            ) -> ToolResult:
                return ToolResult(success=True, output=kwargs["value"])

        tool = RangeTool()

        import asyncio

        # Valid range should work
        result = asyncio.run(tool.execute(None, None, value=50))
        assert result.success is True

        # Below min should raise ValueError
        with pytest.raises(ValueError, match="value"):
            asyncio.run(tool.execute(None, None, value=-5))

        # Above max should raise ValueError
        with pytest.raises(ValueError, match="value"):
            asyncio.run(tool.execute(None, None, value=150))


# =============================================================================
# Tool.get_schema() Tests
# =============================================================================

class TestToolSchema:
    """Tests for the get_schema() method."""

    def test_get_schema_basic(self):
        """Test get_schema() returns OpenAI-compatible schema."""

        class SchemaTool(Tool):
            """A tool with parameters for schema testing."""

            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="schema_test",
                    version="1.0.0",
                    description="A schema test tool",
                    category="testing",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return [
                    ToolParameter(
                        name="message",
                        type=ToolParameterType.STRING,
                        description="A message to process",
                        required=True,
                    ),
                    ToolParameter(
                        name="count",
                        type=ToolParameterType.INTEGER,
                        description="How many times",
                        required=False,
                        default=1,
                    ),
                ]

            async def _execute_impl(
                self,
                session_context: Optional[Dict[str, Any]],
                observer: Optional[Any],
                **kwargs,
            ) -> ToolResult:
                return ToolResult(success=True, output="ok")

        tool = SchemaTool()
        schema = tool.get_schema()

        # Should have standard OpenAI function schema structure
        assert "name" in schema
        assert "description" in schema
        assert "parameters" in schema

        assert schema["name"] == "schema_test"
        assert schema["description"] == "A schema test tool"

        # Parameters should be in proper format
        params = schema["parameters"]
        assert params["type"] == "object"
        assert "properties" in params
        assert "required" in params

        # Check message parameter
        assert "message" in params["properties"]
        assert params["properties"]["message"]["type"] == "string"
        assert params["properties"]["message"]["description"] == "A message to process"

        # Check count parameter
        assert "count" in params["properties"]
        assert params["properties"]["count"]["type"] == "integer"

    def test_get_schema_includes_enum(self):
        """Test get_schema() includes enum constraints."""
        import enum

        class EnumSchemaTool(Tool):
            """A tool with enum for schema testing."""

            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="enum_schema",
                    version="1.0.0",
                    description="Enum schema test",
                    category="testing",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return [
                    ToolParameter(
                        name="color",
                        type=ToolParameterType.STRING,
                        description="Color choice",
                        required=True,
                        enum=["red", "green", "blue"],
                    )
                ]

            async def _execute_impl(
                self,
                session_context: Optional[Dict[str, Any]],
                observer: Optional[Any],
                **kwargs,
            ) -> ToolResult:
                return ToolResult(success=True, output="ok")

        tool = EnumSchemaTool()
        schema = tool.get_schema()

        params = schema["parameters"]
        assert params["properties"]["color"]["enum"] == ["red", "green", "blue"]

    def test_get_schema_includes_ranges(self):
        """Test get_schema() includes min/max constraints."""
        import enum

        class RangeSchemaTool(Tool):
            """A tool with ranges for schema testing."""

            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="range_schema",
                    version="1.0.0",
                    description="Range schema test",
                    category="testing",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return [
                    ToolParameter(
                        name="score",
                        type=ToolParameterType.FLOAT,
                        description="Score value",
                        required=True,
                        min_value=0.0,
                        max_value=100.0,
                    )
                ]

            async def _execute_impl(
                self,
                session_context: Optional[Dict[str, Any]],
                observer: Optional[Any],
                **kwargs,
            ) -> ToolResult:
                return ToolResult(success=True, output="ok")

        tool = RangeSchemaTool()
        schema = tool.get_schema()

        params = schema["parameters"]
        assert params["properties"]["score"]["minimum"] == 0.0
        assert params["properties"]["score"]["maximum"] == 100.0