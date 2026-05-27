"""Base classes for the Tool Skills System.

This module provides the foundation for all tools in the Cribug Python Skills System:
- ToolMetadata: Describes a tool's properties and requirements
- ToolParameter: Defines input parameters for tools
- ToolParameterType: Enum of supported parameter types
- ToolResult: Holds execution results
- Tool: Abstract base class for all tools

The Tool.execute() wrapper handles parameter validation, type coercion,
rate limiting, execution timing, and error wrapping.
"""

import re
import time
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Dict, List, Optional, Union


class ToolParameterType(Enum):
    """Supported parameter types for tool inputs."""

    STRING = "string"
    INTEGER = "integer"
    FLOAT = "float"
    BOOLEAN = "boolean"
    ARRAY = "array"
    OBJECT = "object"
    FILE = "file"


@dataclass
class ToolMetadata:
    """Metadata describing a tool's properties and requirements.

    Attributes:
        name: Unique identifier for the tool
        version: Semantic version string (e.g., "1.0.0")
        description: Human-readable description of what the tool does
        category: Category classification (e.g., "utility", "math", "web")
        author: Author name or organization (default: "Cribug")
        execution_mode: How the tool is executed (default: "python_inline")
        requires_sandbox: Whether tool requires sandboxed execution
        requires_llm: Whether tool requires LLM access
        risk_level: Risk assessment ("low", "medium", "high", "critical")
        side_effects: Whether tool has observable effects beyond return value
        timeout_seconds: Maximum execution time allowed (default: 30)
        rate_limit: Maximum calls per minute, None if unlimited
        cost_per_use: Monetary cost per execution (default: 0.0)
        requires_auth: Whether authentication is required
        sandboxed: Whether tool runs in sandboxed environment
        memory_limit_mb: Memory limit in megabytes (default: 512)
        session_aware: Whether tool maintains session state
        dangerous: Whether tool can cause harm if misused
        input_examples: Example inputs for documentation
    """

    name: str
    version: str
    description: str
    category: str
    author: str = "Cribug"
    execution_mode: str = "python_inline"  # Required!
    requires_sandbox: bool = False  # Required!
    requires_llm: bool = False  # Required!
    risk_level: str = "low"  # Required!
    side_effects: bool = False  # Required!
    timeout_seconds: int = 30
    rate_limit: Optional[int] = None
    cost_per_use: float = 0.0
    requires_auth: bool = False
    sandboxed: bool = True
    memory_limit_mb: int = 512
    session_aware: bool = False
    dangerous: bool = False
    input_examples: Optional[List[Dict[str, Any]]] = None


@dataclass
class ToolParameter:
    """Definition of a tool input parameter.

    Attributes:
        name: Parameter identifier
        type: Parameter data type
        description: Human-readable description
        required: Whether parameter must be provided (default: True)
        default: Default value if not required
        enum: Allowed values (None if unrestricted)
        min_value: Minimum for numeric types
        max_value: Maximum for numeric types
        pattern: Regex pattern for string validation
        max_length: Maximum string length
    """

    name: str
    type: ToolParameterType
    description: str
    required: bool = True
    default: Any = None
    enum: Optional[List[Any]] = None
    min_value: Optional[Union[int, float]] = None
    max_value: Optional[Union[int, float]] = None
    pattern: Optional[str] = None
    max_length: Optional[int] = None


@dataclass
class ToolResult:
    """Result of a tool execution.

    Attributes:
        success: Whether execution succeeded
        output: The output data (None on failure)
        error: Error message if success is False
        metadata: Additional execution metadata
        execution_time_ms: Execution time in milliseconds
        tokens_used: Number of LLM tokens consumed (if applicable)
    """

    success: bool
    output: Any = None
    error: Optional[str] = None
    metadata: Optional[Dict[str, Any]] = None
    execution_time_ms: Optional[int] = None
    tokens_used: Optional[int] = None


class Tool(ABC):
    """Abstract base class for all tools.

    Subclasses must implement:
    - _get_metadata(): Return ToolMetadata describing this tool
    - _get_parameters(): Return list of ToolParameter definitions
    - _execute_impl(): Perform the actual tool execution

    The execute() method provides a wrapper that handles:
    - Parameter validation (required, type, enum, min/max, pattern)
    - Type coercion (INTEGER from float/int/string, FLOAT from int/string, BOOLEAN from string)
    - Rate limiting check
    - Execution timing
    - Error wrapping

    Example:
        class EchoTool(Tool):
            def _get_metadata(self) -> ToolMetadata:
                return ToolMetadata(
                    name="echo",
                    version="1.0.0",
                    description="Echoes input back",
                    category="utility",
                )

            def _get_parameters(self) -> List[ToolParameter]:
                return [
                    ToolParameter(
                        name="message",
                        type=ToolParameterType.STRING,
                        description="Message to echo",
                    )
                ]

            async def _execute_impl(self, session_context, observer, **kwargs) -> ToolResult:
                return ToolResult(success=True, output=kwargs["message"])

        tool = EchoTool()
        result = await tool.execute(session_context, observer, message="Hello")
    """

    def __init__(self):
        """Initialize the tool."""
        self.metadata = self._get_metadata()
        self.parameters = self._get_parameters()
        self._last_execution_time: Optional[float] = None

    @abstractmethod
    def _get_metadata(self) -> ToolMetadata:
        """Return metadata describing this tool.

        Returns:
            ToolMetadata with tool information and requirements.
        """
        pass

    @abstractmethod
    def _get_parameters(self) -> List[ToolParameter]:
        """Return parameter definitions for this tool.

        Returns:
            List of ToolParameter definitions for all input parameters.
        """
        pass

    @abstractmethod
    async def _execute_impl(
        self,
        session_context: Optional[Dict[str, Any]],
        observer: Optional[Any],
        **kwargs,
    ) -> ToolResult:
        """Execute the tool with the given parameters.

        This is the main implementation method that subclasses override.

        Args:
            session_context: Session context dictionary (may be None)
            observer: Observer object for progress/cancellation (may be None)
            **kwargs: Parameter values as defined by _get_parameters()

        Returns:
            ToolResult with success status and output/error.
        """
        pass

    def _validate_parameters(self, **kwargs) -> Dict[str, Any]:
        """Validate and coerce parameters against the parameter definitions.

        Args:
            **kwargs: Raw parameter values to validate.

        Returns:
            Dictionary of validated and coerced parameter values.

        Raises:
            ValueError: If required parameters are missing or validation fails.
        """
        params = self._get_parameters()
        validated: Dict[str, Any] = {}

        # Build a map of parameter definitions by name
        param_map: Dict[str, ToolParameter] = {p.name: p for p in params}

        # Check for missing required parameters
        for param in params:
            if param.required and param.name not in kwargs and param.default is None:
                raise ValueError(
                    f"Missing required parameter '{param.name}' for tool '{self._get_metadata().name}'"
                )

        # Validate and coerce each provided parameter
        for name, value in kwargs.items():
            if name not in param_map:
                # Unknown parameters are passed through as-is
                validated[name] = value
                continue

            param = param_map[name]
            validated_value = self._coerce_and_validate(param, value)
            validated[name] = validated_value

        # Fill in defaults for optional parameters not provided
        for param in params:
            if param.name not in validated and param.default is not None:
                validated[param.name] = param.default

        return validated

    def _coerce_and_validate(
        self, param: ToolParameter, value: Any
    ) -> Any:
        """Coerce and validate a single parameter value.

        Args:
            param: The parameter definition.
            value: The value to coerce and validate.

        Returns:
            The coerced and validated value.

        Raises:
            ValueError: If validation fails.
        """
        # Handle None for optional parameters
        if value is None:
            return None

        # Type coercion based on parameter type
        coerced: Any = None
        if param.type == ToolParameterType.INTEGER:
            coerced = self._coerce_to_int(value)
        elif param.type == ToolParameterType.FLOAT:
            coerced = self._coerce_to_float(value)
        elif param.type == ToolParameterType.BOOLEAN:
            coerced = self._coerce_to_bool(value)
        elif param.type == ToolParameterType.ARRAY:
            coerced = self._coerce_to_array(value)
        elif param.type == ToolParameterType.OBJECT:
            coerced = self._coerce_to_object(value)
        else:
            coerced = self._coerce_to_string(value)

        # Validate enum constraints
        if param.enum is not None and coerced not in param.enum:
            raise ValueError(
                f"Parameter '{param.name}' value '{coerced}' not in allowed values: {param.enum}"
            )

        # Validate min/max for numeric types
        if param.type in (ToolParameterType.INTEGER, ToolParameterType.FLOAT):
            if param.min_value is not None and coerced < param.min_value:
                raise ValueError(
                    f"Parameter '{param.name}' value {coerced} is below minimum {param.min_value}"
                )
            if param.max_value is not None and coerced > param.max_value:
                raise ValueError(
                    f"Parameter '{param.name}' value {coerced} is above maximum {param.max_value}"
                )

        # Validate regex pattern for strings
        if param.type == ToolParameterType.STRING and param.pattern is not None:
            if not re.match(param.pattern, str(coerced)):
                raise ValueError(
                    f"Parameter '{param.name}' value '{coerced}' does not match pattern '{param.pattern}'"
                )

        # Validate max_length for strings
        if param.type == ToolParameterType.STRING and param.max_length is not None:
            if len(str(coerced)) > param.max_length:
                raise ValueError(
                    f"Parameter '{param.name}' value length {len(str(coerced))} exceeds max_length {param.max_length}"
                )

        return coerced

    def _coerce_to_int(self, value: Any) -> int:
        """Coerce a value to integer."""
        if isinstance(value, int):
            return value
        if isinstance(value, float):
            if value.is_integer():
                return int(value)
            raise ValueError(f"Cannot coerce float {value} to integer (would lose precision)")
        if isinstance(value, str):
            try:
                return int(value)
            except ValueError:
                raise ValueError(f"Cannot coerce string '{value}' to integer")
        raise ValueError(f"Cannot coerce {type(value).__name__} to integer")

    def _coerce_to_float(self, value: Any) -> float:
        """Coerce a value to float."""
        if isinstance(value, float):
            return value
        if isinstance(value, int):
            return float(value)
        if isinstance(value, str):
            try:
                return float(value)
            except ValueError:
                raise ValueError(f"Cannot coerce string '{value}' to float")
        raise ValueError(f"Cannot coerce {type(value).__name__} to float")

    def _coerce_to_bool(self, value: Any) -> bool:
        """Coerce a value to boolean."""
        if isinstance(value, bool):
            return value
        if isinstance(value, str):
            lower = value.lower()
            if lower in ("true", "1", "yes", "on"):
                return True
            if lower in ("false", "0", "no", "off", ""):
                return False
            raise ValueError(f"Cannot coerce string '{value}' to boolean")
        if isinstance(value, (int, float)):
            return bool(value)
        raise ValueError(f"Cannot coerce {type(value).__name__} to boolean")

    def _coerce_to_string(self, value: Any) -> str:
        """Coerce a value to string."""
        return str(value)

    def _coerce_to_array(self, value: Any) -> list:
        """Coerce a value to array."""
        if isinstance(value, list):
            return value
        if isinstance(value, tuple):
            return list(value)
        raise ValueError(f"Cannot coerce {type(value).__name__} to array")

    def _coerce_to_object(self, value: Any) -> dict:
        """Coerce a value to object/dict."""
        if isinstance(value, dict):
            return value
        raise ValueError(f"Cannot coerce {type(value).__name__} to object")

    def _check_rate_limit(self) -> None:
        """Check if rate limiting should block execution.

        Raises:
            ValueError: If rate limit is exceeded.
        """
        metadata = self._get_metadata()
        if metadata.rate_limit is None:
            return

        # Simple rate limiting: check time since last execution
        current_time = time.time()
        min_interval = 60.0 / metadata.rate_limit

        if self._last_execution_time is not None:
            elapsed = current_time - self._last_execution_time
            if elapsed < min_interval:
                wait_time = min_interval - elapsed
                raise ValueError(
                    f"Rate limit exceeded for tool '{metadata.name}'. "
                    f"Please wait {wait_time:.2f} seconds before retrying."
                )

        self._last_execution_time = current_time

    async def execute(
        self,
        session_context: Optional[Dict[str, Any]],
        observer: Optional[Any],
        **kwargs,
    ) -> ToolResult:
        """Execute the tool with the given parameters.

        This is the main public method that wraps _execute_impl with:
        - Parameter validation (raises ValueError for validation errors)
        - Type coercion
        - Rate limiting check
        - Execution timing
        - Error wrapping for execution errors

        Args:
            session_context: Session context dictionary (may be None)
            observer: Observer object for progress/cancellation (may be None)
            **kwargs: Parameter values as defined by _get_parameters()

        Returns:
            ToolResult with success status and output/error.

        Raises:
            ValueError: If required parameters are missing or validation fails.
        """
        start_time = time.time()

        # Check rate limit first (raises ValueError if exceeded)
        self._check_rate_limit()

        # Validate and coerce parameters (raises ValueError if invalid)
        validated_kwargs = self._validate_parameters(**kwargs)

        try:
            # Execute the tool implementation
            result = await self._execute_impl(session_context, observer, **validated_kwargs)

            # Set execution time if not already set by implementation
            if result.execution_time_ms is None:
                elapsed_ms = int((time.time() - start_time) * 1000)
                result.execution_time_ms = elapsed_ms

            return result

        except ValueError:
            # Re-raise validation errors (already raised above, but catch
            # any that might come from _execute_impl for param validation)
            raise
        except Exception as e:
            elapsed_ms = int((time.time() - start_time) * 1000)
            return ToolResult(
                success=False,
                error=f"Unexpected error in {self._get_metadata().name}: {type(e).__name__}: {e}",
                execution_time_ms=elapsed_ms,
            )

    def get_schema(self) -> Dict[str, Any]:
        """Get an OpenAI-compatible JSON schema for this tool.

        Returns:
            Dictionary containing the tool schema in OpenAI function format.
        """
        metadata = self._get_metadata()
        params = self._get_parameters()

        # Build the parameters schema
        properties: Dict[str, Any] = {}
        required: List[str] = []

        for param in params:
            prop: Dict[str, Any] = {"description": param.description}

            # Map parameter types to JSON schema types
            if param.type == ToolParameterType.STRING:
                prop["type"] = "string"
                if param.enum:
                    prop["enum"] = param.enum
                if param.pattern:
                    prop["pattern"] = param.pattern
                if param.max_length:
                    prop["maxLength"] = param.max_length
            elif param.type == ToolParameterType.INTEGER:
                prop["type"] = "integer"
                if param.min_value is not None:
                    prop["minimum"] = param.min_value
                if param.max_value is not None:
                    prop["maximum"] = param.max_value
            elif param.type == ToolParameterType.FLOAT:
                prop["type"] = "number"
                if param.min_value is not None:
                    prop["minimum"] = param.min_value
                if param.max_value is not None:
                    prop["maximum"] = param.max_value
            elif param.type == ToolParameterType.BOOLEAN:
                prop["type"] = "boolean"
            elif param.type == ToolParameterType.ARRAY:
                prop["type"] = "array"
            elif param.type == ToolParameterType.OBJECT:
                prop["type"] = "object"
            elif param.type == ToolParameterType.FILE:
                prop["type"] = "string"  # File paths as strings

            properties[param.name] = prop

            if param.required:
                required.append(param.name)

        return {
            "name": metadata.name,
            "description": metadata.description,
            "parameters": {
                "type": "object",
                "properties": properties,
                "required": required,
            },
        }