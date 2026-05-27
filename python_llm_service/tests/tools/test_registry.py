"""Tests for the ToolRegistry class.

Tests registration, unregistration, singleton management, schema retrieval,
discovery, and agent filtering.
"""

import pytest
import tempfile
import os
import sys
from typing import Any, Dict, List, Optional

from llm_service.tools.base import (
    Tool,
    ToolMetadata,
    ToolParameter,
    ToolResult,
    ToolParameterType,
)
from llm_service.tools.registry import ToolRegistry, get_registry


# =============================================================================
# Helper tool classes for testing
# =============================================================================

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
        return ToolResult(success=True, output=f"Echo: {kwargs['message']}")


class MathTool(Tool):
    """A math tool for testing."""

    def _get_metadata(self) -> ToolMetadata:
        return ToolMetadata(
            name="math",
            version="1.0.0",
            description="Performs basic math",
            category="math",
            cost_per_use=0.01,
        )

    def _get_parameters(self) -> List[ToolParameter]:
        return [
            ToolParameter(
                name="a",
                type=ToolParameterType.FLOAT,
                description="First number",
                required=True,
            ),
            ToolParameter(
                name="b",
                type=ToolParameterType.FLOAT,
                description="Second number",
                required=True,
            ),
        ]

    async def _execute_impl(
        self,
        session_context: Optional[Dict[str, Any]],
        observer: Optional[Any],
        **kwargs,
    ) -> ToolResult:
        return ToolResult(success=True, output=kwargs["a"] + kwargs["b"])


class DangerousTool(Tool):
    """A dangerous tool for testing filtering."""

    def _get_metadata(self) -> ToolMetadata:
        return ToolMetadata(
            name="dangerous_action",
            version="1.0.0",
            description="A potentially dangerous tool",
            category="system",
            dangerous=True,
            risk_level="critical",
            cost_per_use=0.5,
        )

    def _get_parameters(self) -> List[ToolParameter]:
        return [
            ToolParameter(
                name="command",
                type=ToolParameterType.STRING,
                description="Command to run",
                required=True,
            )
        ]

    async def _execute_impl(
        self,
        session_context: Optional[Dict[str, Any]],
        observer: Optional[Any],
        **kwargs,
    ) -> ToolResult:
        return ToolResult(success=True, output=f"Ran: {kwargs['command']}")


class CostlyTool(Tool):
    """An expensive tool for testing cost filtering."""

    def _get_metadata(self) -> ToolMetadata:
        return ToolMetadata(
            name="llm_summarize",
            version="1.0.0",
            description="Summarizes text using LLM",
            category="llm",
            requires_llm=True,
            cost_per_use=0.1,
        )

    def _get_parameters(self) -> List[ToolParameter]:
        return [
            ToolParameter(
                name="text",
                type=ToolParameterType.STRING,
                description="Text to summarize",
                required=True,
            )
        ]

    async def _execute_impl(
        self,
        session_context: Optional[Dict[str, Any]],
        observer: Optional[Any],
        **kwargs,
    ) -> ToolResult:
        return ToolResult(success=True, output="Summary")


# =============================================================================
# ToolRegistry Tests
# =============================================================================

class TestToolRegistry:
    """Tests for the ToolRegistry class."""

    def setup_method(self):
        """Create a fresh registry for each test."""
        self.registry = ToolRegistry()

    # --- register / unregister ---

    def test_register_tool(self):
        """Test registering a tool class."""
        self.registry.register(EchoTool)
        names = self.registry.list_tools()
        assert "echo" in names

    def test_register_multiple_tools(self):
        """Test registering multiple tool classes."""
        self.registry.register(EchoTool)
        self.registry.register(MathTool)
        names = self.registry.list_tools()
        assert "echo" in names
        assert "math" in names
        assert len(names) == 2

    def test_duplicate_register_raises_value_error(self):
        """Test that registering a duplicate name raises ValueError."""
        self.registry.register(EchoTool)
        with pytest.raises(ValueError, match="already registered"):
            self.registry.register(EchoTool)

    def test_register_override_succeeds(self):
        """Test that registering with override=True replaces existing."""
        self.registry.register(EchoTool)

        # Override with a different tool that has the same name
        class EchoToolV2(Tool):
            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="echo",
                    version="2.0.0",
                    description="Echo v2",
                    category="utility",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return []

            async def _execute_impl(
                self, session_context, observer, **kwargs
            ) -> ToolResult:
                return ToolResult(success=True, output="v2")

        self.registry.register(EchoToolV2, override=True)

        # Should now return the new version instance
        tool = self.registry.get_tool("echo")
        assert tool._get_metadata().version == "2.0.0"

    def test_unregister_tool(self):
        """Test unregistering a tool."""
        self.registry.register(EchoTool)
        assert "echo" in self.registry.list_tools()

        self.registry.unregister("echo")
        assert "echo" not in self.registry.list_tools()

    def test_unregister_nonexistent(self):
        """Test unregistering a tool that doesn't exist (no-op)."""
        # Should not raise
        self.registry.unregister("nonexistent")

    def test_register_invalid_class(self):
        """Test that registering a non-Tool subclass raises TypeError."""

        class NotATool:
            pass

        with pytest.raises(TypeError, match="Expected a subclass of Tool"):
            self.registry.register(NotATool)

    # --- get_tool ---

    def test_get_tool_returns_instance(self):
        """Test that get_tool returns a Tool instance."""
        self.registry.register(EchoTool)
        tool = self.registry.get_tool("echo")
        assert isinstance(tool, Tool)
        assert isinstance(tool, EchoTool)

    def test_get_tool_singleton_per_name(self):
        """Test that get_tool returns the same instance for the same name."""
        self.registry.register(EchoTool)
        tool1 = self.registry.get_tool("echo")
        tool2 = self.registry.get_tool("echo")
        assert tool1 is tool2

    def test_get_tool_returns_none_for_missing(self):
        """Test that get_tool returns None for unregistered tool."""
        tool = self.registry.get_tool("nonexistent")
        assert tool is None

    def test_get_tool_after_unregister(self):
        """Test that get_tool returns None after unregister."""
        self.registry.register(EchoTool)
        self.registry.unregister("echo")
        tool = self.registry.get_tool("echo")
        assert tool is None

    # --- list_tools ---

    def test_list_tools_empty(self):
        """Test that list_tools returns empty list initially."""
        assert self.registry.list_tools() == []

    def test_list_tools_returns_all_names(self):
        """Test that list_tools returns all registered tool names."""
        self.registry.register(EchoTool)
        self.registry.register(MathTool)
        names = self.registry.list_tools()
        assert sorted(names) == ["echo", "math"]

    # --- list_categories ---

    def test_list_categories(self):
        """Test listing all categories."""
        self.registry.register(EchoTool)  # category: utility
        self.registry.register(MathTool)  # category: math
        categories = self.registry.list_categories()
        assert sorted(categories) == ["math", "utility"]

    def test_list_categories_empty(self):
        """Test that list_categories returns empty list initially."""
        assert self.registry.list_categories() == []

    def test_list_categories_deduplicates(self):
        """Test that list_categories returns unique categories."""
        class AnotherUtilityTool(Tool):
            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="util2",
                    version="1.0.0",
                    description="Another utility",
                    category="utility",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return []

            async def _execute_impl(
                self, session_context, observer, **kwargs
            ) -> ToolResult:
                return ToolResult(success=True, output="ok")

        self.registry.register(EchoTool)  # category: utility
        self.registry.register(AnotherUtilityTool)  # category: utility
        self.registry.register(MathTool)  # category: math

        categories = self.registry.list_categories()
        assert sorted(categories) == ["math", "utility"]

    # --- list_tools_by_category ---

    def test_list_tools_by_category(self):
        """Test filtering tools by category."""
        self.registry.register(EchoTool)  # utility
        self.registry.register(MathTool)  # math
        self.registry.register(DangerousTool)  # system

        utility_tools = self.registry.list_tools_by_category("utility")
        assert utility_tools == ["echo"]

        math_tools = self.registry.list_tools_by_category("math")
        assert math_tools == ["math"]

    def test_list_tools_by_category_empty_for_unknown(self):
        """Test that unknown category returns empty list."""
        self.registry.register(EchoTool)
        tools = self.registry.list_tools_by_category("nonexistent")
        assert tools == []

    def test_list_tools_by_category_no_tools(self):
        """Test list_tools_by_category on empty registry."""
        tools = self.registry.list_tools_by_category("utility")
        assert tools == []

    # --- get_tool_metadata ---

    def test_get_tool_metadata(self):
        """Test retrieving metadata for a registered tool."""
        self.registry.register(EchoTool)
        metadata = self.registry.get_tool_metadata("echo")
        assert metadata is not None
        assert metadata.name == "echo"
        assert metadata.version == "1.0.0"
        assert metadata.description == "Echoes the input back"
        assert metadata.category == "utility"

    def test_get_tool_metadata_none_for_missing(self):
        """Test get_tool_metadata returns None for unregistered tool."""
        metadata = self.registry.get_tool_metadata("nonexistent")
        assert metadata is None

    # --- get_tool_schema ---

    def test_get_tool_schema(self):
        """Test retrieving schema for a registered tool."""
        self.registry.register(EchoTool)
        schema = self.registry.get_tool_schema("echo")
        assert schema is not None
        assert schema["name"] == "echo"
        assert "description" in schema
        assert "parameters" in schema

    def test_get_tool_schema_none_for_missing(self):
        """Test get_tool_schema returns None for unregistered tool."""
        schema = self.registry.get_tool_schema("nonexistent")
        assert schema is None

    # --- get_all_schemas ---

    def test_get_all_schemas(self):
        """Test retrieving schemas for all registered tools."""
        self.registry.register(EchoTool)
        self.registry.register(MathTool)
        schemas = self.registry.get_all_schemas()
        assert len(schemas) == 2

        schema_names = {s["name"] for s in schemas}
        assert schema_names == {"echo", "math"}

    def test_get_all_schemas_empty(self):
        """Test get_all_schemas on empty registry."""
        schemas = self.registry.get_all_schemas()
        assert schemas == []

    # --- filter_tools_for_agent ---

    def test_filter_tools_for_agent_all(self):
        """Test filter with no restrictions returns all."""
        self.registry.register(EchoTool)
        self.registry.register(MathTool)
        self.registry.register(DangerousTool)
        self.registry.register(CostlyTool)

        filtered = self.registry.filter_tools_for_agent()
        assert sorted(filtered) == sorted(["echo", "math", "dangerous_action", "llm_summarize"])

    def test_filter_tools_by_category(self):
        """Test filtering tools by allowed categories."""
        self.registry.register(EchoTool)  # utility
        self.registry.register(MathTool)  # math
        self.registry.register(DangerousTool)  # system
        self.registry.register(CostlyTool)  # llm

        filtered = self.registry.filter_tools_for_agent(categories=["utility", "math"])
        assert sorted(filtered) == sorted(["echo", "math"])

    def test_filter_exclude_dangerous(self):
        """Test filtering out dangerous tools."""
        self.registry.register(EchoTool)
        self.registry.register(DangerousTool)

        filtered = self.registry.filter_tools_for_agent(exclude_dangerous=True)
        assert "echo" in filtered
        assert "dangerous_action" not in filtered

    def test_filter_max_cost(self):
        """Test filtering tools by maximum cost."""
        self.registry.register(EchoTool)  # cost_per_use=0.0
        self.registry.register(MathTool)  # cost_per_use=0.01
        self.registry.register(CostlyTool)  # cost_per_use=0.1

        filtered = self.registry.filter_tools_for_agent(max_cost=0.05)
        assert "echo" in filtered
        assert "math" in filtered
        assert "llm_summarize" not in filtered

    def test_filter_combined_criteria(self):
        """Test filtering with all criteria combined."""
        self.registry.register(EchoTool)  # utility, cost=0.0
        self.registry.register(MathTool)  # math, cost=0.01
        self.registry.register(DangerousTool)  # system, dangerous=True, cost=0.5
        self.registry.register(CostlyTool)  # llm, cost=0.1

        filtered = self.registry.filter_tools_for_agent(
            categories=["utility", "math", "llm"],
            exclude_dangerous=True,
            max_cost=0.05,
        )
        assert filtered == ["echo", "math"]
        assert "dangerous_action" not in filtered
        assert "llm_summarize" not in filtered

    # --- discover_tools ---

    def test_discover_tools(self):
        """Test discovering tools from a package directory."""
        with tempfile.TemporaryDirectory() as tmpdir:
            # Create a tools package with a discoverable tool module
            pkg_dir = os.path.join(tmpdir, "discoverable_tools")
            os.makedirs(pkg_dir)

            # Create __init__.py
            with open(os.path.join(pkg_dir, "__init__.py"), "w") as f:
                f.write("")

            # Create a module with a tool
            tool_module = os.path.join(pkg_dir, "custom_tool.py")
            with open(tool_module, "w") as f:
                f.write("""
from llm_service.tools.base import Tool, ToolMetadata, ToolParameter, ToolResult, ToolParameterType
from typing import Any, Dict, List, Optional

class DiscoveredTool(Tool):
    def _get_metadata(self) -> ToolMetadata:
        return ToolMetadata(
            name="discovered",
            version="1.0.0",
            description="Discovered via package scan",
            category="discovery",
        )
    def _get_parameters(self) -> List[ToolParameter]:
        return []
    async def _execute_impl(self, session_context, observer, **kwargs) -> ToolResult:
        return ToolResult(success=True, output="discovered")
""")

            count = self.registry.discover_tools(pkg_dir)
            assert count >= 1
            assert "discovered" in self.registry.list_tools()

    def test_discover_tools_invalid_path(self):
        """Test discover_tools with invalid path returns 0."""
        count = self.registry.discover_tools("/nonexistent/path")
        assert count == 0

    def test_discover_tools_empty_package(self):
        """Test discover_tools with empty package returns 0."""
        with tempfile.TemporaryDirectory() as tmpdir:
            pkg_dir = os.path.join(tmpdir, "empty_pkg")
            os.makedirs(pkg_dir)
            with open(os.path.join(pkg_dir, "__init__.py"), "w") as f:
                f.write("")

            count = self.registry.discover_tools(pkg_dir)
            assert count == 0


# =============================================================================
# Global Singleton Tests
# =============================================================================

class TestGetRegistrySingleton:
    """Tests for the global get_registry() singleton."""

    def test_get_registry_returns_registry(self):
        """Test that get_registry() returns a ToolRegistry instance."""
        reg = get_registry()
        assert isinstance(reg, ToolRegistry)

    def test_get_registry_singleton(self):
        """Test that get_registry() always returns the same instance."""
        reg1 = get_registry()
        reg2 = get_registry()
        assert reg1 is reg2
