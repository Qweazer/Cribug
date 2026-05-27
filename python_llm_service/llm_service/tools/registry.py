"""Tool registry for managing tool discovery and lifecycle.

The ToolRegistry provides a central registry for all tools in the Cribug
Python Skills System. It handles:

- Registration and unregistration of tool classes
- Singleton instance management (one instance per tool name)
- Metadata and schema retrieval
- Tool discovery from Python package directories
- Agent-aware filtering (by category, danger level, cost)
- Module-level convenience via get_registry()

Usage:
    registry = ToolRegistry()
    registry.register(MyTool)
    tool = registry.get_tool("my_tool")
    schemas = registry.get_all_schemas()
"""

import importlib
import inspect
import os
import pkgutil
import sys
from typing import Any, Dict, List, Optional, Set, Type

from llm_service.tools.base import Tool, ToolMetadata


class ToolRegistry:
    """Central registry for managing tool classes and their instances.

    Provides registration, discovery, singleton instance management,
    schema retrieval, and agent-aware filtering.

    The registry maintains:
    - _tool_classes: Maps tool name -> Tool subclass (the class object)
    - _instances: Maps tool name -> Tool instance (singleton, lazy-created)
    """

    def __init__(self) -> None:
        """Initialize an empty registry."""
        self._tool_classes: Dict[str, Type[Tool]] = {}
        self._instances: Dict[str, Tool] = {}

    # ------------------------------------------------------------------
    # Registration
    # ------------------------------------------------------------------

    def register(
        self,
        tool_class: Type[Tool],
        override: bool = False,
    ) -> None:
        """Register a tool class.

        The tool's name is obtained from its metadata (``_get_metadata().name``).

        Args:
            tool_class: A subclass of ``Tool``.
            override: If ``True``, silently replace an existing registration
                with the same name. If ``False`` (default), raise
                ``ValueError`` when the name is already registered.

        Raises:
            TypeError: If ``tool_class`` is not a subclass of ``Tool``.
            ValueError: If a tool with the same name is already registered
                and ``override`` is ``False``.

        Example:
            >>> registry.register(EchoTool)
            >>> registry.register(EchoToolV2, override=True)
        """
        if not (isinstance(tool_class, type) and issubclass(tool_class, Tool)):
            raise TypeError(
                f"Expected a subclass of Tool, got {type(tool_class).__name__}"
            )

        # Instantiate once to get metadata (required for name)
        dummy_instance = tool_class()
        name = dummy_instance._get_metadata().name

        if name in self._tool_classes and not override:
            raise ValueError(
                f"Tool '{name}' is already registered. Use override=True to replace."
            )

        self._tool_classes[name] = tool_class
        # Invalidate cached instance so the next get_tool call creates a fresh one
        if name in self._instances:
            del self._instances[name]

    def unregister(self, tool_name: str) -> None:
        """Remove a tool from the registry.

        This is a no-op if the tool name is not registered.

        Args:
            tool_name: The name of the tool to remove.
        """
        self._tool_classes.pop(tool_name, None)
        self._instances.pop(tool_name, None)

    # ------------------------------------------------------------------
    # Instance retrieval
    # ------------------------------------------------------------------

    def get_tool(self, name: str) -> Optional[Tool]:
        """Get a tool instance by name (singleton per name).

        The first call for a given name creates and caches the instance;
        subsequent calls return the same instance until the tool is
        re-registered or unregistered.

        Args:
            name: The tool name.

        Returns:
            A ``Tool`` instance if the name is registered, or ``None``.
        """
        if name not in self._tool_classes:
            return None

        if name not in self._instances:
            tool_class = self._tool_classes[name]
            self._instances[name] = tool_class()

        return self._instances[name]

    # ------------------------------------------------------------------
    # Listing and querying
    # ------------------------------------------------------------------

    def list_tools(self) -> List[str]:
        """Return the names of all registered tools.

        Returns:
            A list of tool name strings.
        """
        return list(self._tool_classes.keys())

    def list_categories(self) -> List[str]:
        """Return all unique tool categories.

        Returns:
            A sorted list of category strings.
        """
        categories: Set[str] = set()
        for tool_class in self._tool_classes.values():
            # Instantiate to read metadata
            instance = tool_class()
            categories.add(instance._get_metadata().category)
        return sorted(categories)

    def list_tools_by_category(self, category: str) -> List[str]:
        """Return names of tools belonging to a given category.

        Args:
            category: The category to filter by.

        Returns:
            A list of tool name strings in the given category.
        """
        result: List[str] = []
        for name, tool_class in self._tool_classes.items():
            instance = tool_class()
            if instance._get_metadata().category == category:
                result.append(name)
        return result

    # ------------------------------------------------------------------
    # Metadata and schema
    # ------------------------------------------------------------------

    def get_tool_metadata(self, name: str) -> Optional[ToolMetadata]:
        """Get metadata for a registered tool.

        Args:
            name: The tool name.

        Returns:
            A ``ToolMetadata`` instance if the tool is registered, or ``None``.
        """
        tool = self.get_tool(name)
        if tool is None:
            return None
        return tool._get_metadata()

    def get_tool_schema(self, name: str) -> Optional[Dict[str, Any]]:
        """Get the OpenAI-compatible JSON schema for a registered tool.

        Args:
            name: The tool name.

        Returns:
            A schema dictionary if the tool is registered, or ``None``.
        """
        tool = self.get_tool(name)
        if tool is None:
            return None
        return tool.get_schema()

    def get_all_schemas(self) -> List[Dict[str, Any]]:
        """Get schemas for all registered tools.

        Returns:
            A list of schema dictionaries.
        """
        return [
            tool_class().get_schema()
            for tool_class in self._tool_classes.values()
        ]

    # ------------------------------------------------------------------
    # Discovery
    # ------------------------------------------------------------------

    def discover_tools(self, package_path: str) -> int:
        """Discover and register Tool subclasses from a package directory.

        Scans the given filesystem path for Python modules, imports them,
        and registers any class that is a concrete subclass of ``Tool``
        (i.e. ``Tool`` itself is excluded).

        Args:
            package_path: Filesystem path to a Python package directory
                (must contain an ``__init__.py``).

        Returns:
            The number of tools discovered and registered.

        Example:
            >>> count = registry.discover_tools("/path/to/my_tools_pkg")
            >>> print(f"Discovered {count} tools")
        """
        if not os.path.isdir(package_path):
            return 0

        # Ensure the parent directory is on sys.path so we can import
        parent_dir = os.path.dirname(package_path)
        pkg_name = os.path.basename(package_path)

        if parent_dir not in sys.path:
            sys.path.insert(0, parent_dir)

        discovered = 0

        # Walk the package directory for Python modules
        for importer, modname, is_pkg in pkgutil.iter_modules([package_path]):
            full_module_name = f"{pkg_name}.{modname}"
            try:
                module = importlib.import_module(full_module_name)
            except Exception:
                continue

            # Find all Tool subclasses defined in this module (not imported)
            for name, obj in inspect.getmembers(module, inspect.isclass):
                if (
                    issubclass(obj, Tool)
                    and obj is not Tool
                    and obj.__module__ == module.__name__
                ):
                    try:
                        self.register(obj)
                        discovered += 1
                    except (ValueError, TypeError):
                        # Skip on duplicate or invalid registration
                        continue

        return discovered

    # ------------------------------------------------------------------
    # Agent filtering
    # ------------------------------------------------------------------

    def filter_tools_for_agent(
        self,
        categories: Optional[List[str]] = None,
        exclude_dangerous: bool = False,
        max_cost: Optional[float] = None,
    ) -> List[str]:
        """Filter registered tools for agent use.

        Args:
            categories: If provided, only include tools whose category
                appears in this list.
            exclude_dangerous: If ``True``, exclude tools whose metadata
                marks them as dangerous.
            max_cost: If provided, exclude tools whose ``cost_per_use``
                exceeds this value.

        Returns:
            A list of tool name strings matching all criteria.
        """
        result: List[str] = []

        for name, tool_class in self._tool_classes.items():
            instance = tool_class()
            metadata = instance._get_metadata()

            # Filter by category
            if categories is not None and metadata.category not in categories:
                continue

            # Filter by danger level
            if exclude_dangerous and metadata.dangerous:
                continue

            # Filter by cost
            if max_cost is not None and metadata.cost_per_use > max_cost:
                continue

            result.append(name)

        return result


# ------------------------------------------------------------------
# Global singleton
# ------------------------------------------------------------------

_global_registry: Optional[ToolRegistry] = None


def get_registry() -> ToolRegistry:
    """Return the global ``ToolRegistry`` singleton.

    Creates the singleton on first call; subsequent calls return the
    same instance.

    Returns:
        The global ``ToolRegistry`` instance.
    """
    global _global_registry
    if _global_registry is None:
        _global_registry = ToolRegistry()
    return _global_registry
