"""Safe Math Skill - Safe mathematical operations with strict validation"""
from typing import Optional, Dict, Any, List
from ..base import Tool, ToolMetadata, ToolParameter, ToolParameterType, ToolResult


class SafeMathTool(Tool):
    """Safe mathematical operations with strict validation.

    Execution mode: python_inline (NO Rust sandbox, NO eval)
    Only whitelist operations: add, subtract, multiply, divide, power
    """

    VALID_OPERATIONS = {"add", "subtract", "multiply", "divide", "power"}
    POWER_MAX_EXPONENT = 10

    def _get_metadata(self) -> ToolMetadata:
        return ToolMetadata(
            name="safe_math_skill",
            version="1.0.0",
            description="Safe mathematical operations with strict validation",
            category="calculation",
            author="Cribug",
            execution_mode="python_inline",
            requires_sandbox=False,
            requires_llm=False,
            risk_level="low",
            side_effects=False,
            timeout_seconds=10,
            rate_limit=None,
            cost_per_use=0.0,
            requires_auth=False,
            sandboxed=True,
        )

    def _get_parameters(self) -> List[ToolParameter]:
        return [
            ToolParameter(
                name="operation",
                type=ToolParameterType.STRING,
                description="Mathematical operation: add, subtract, multiply, divide, power",
                required=True,
                enum=["add", "subtract", "multiply", "divide", "power"],
            ),
            ToolParameter(
                name="a",
                type=ToolParameterType.FLOAT,
                description="First operand",
                required=True,
            ),
            ToolParameter(
                name="b",
                type=ToolParameterType.FLOAT,
                description="Second operand",
                required=True,
            ),
        ]

    async def _execute_impl(
        self,
        session_context: Optional[Dict] = None,
        observer: Optional[Any] = None,
        **kwargs
    ) -> ToolResult:
        operation = kwargs.get("operation", "").lower()
        a = kwargs.get("a")
        b = kwargs.get("b")

        if operation not in self.VALID_OPERATIONS:
            return ToolResult(
                success=False,
                output=None,
                error=f"Unsupported operation: {operation}. Valid: {self.VALID_OPERATIONS}",
            )

        if not isinstance(a, (int, float)) or not isinstance(b, (int, float)):
            return ToolResult(
                success=False,
                output=None,
                error="Parameters 'a' and 'b' must be numeric",
            )

        a = float(a)
        b = float(b)

        try:
            if operation == "add":
                result = a + b
            elif operation == "subtract":
                result = a - b
            elif operation == "multiply":
                result = a * b
            elif operation == "divide":
                if b == 0:
                    return ToolResult(
                        success=False,
                        output=None,
                        error="Division by zero",
                    )
                result = a / b
            elif operation == "power":
                if abs(b) > self.POWER_MAX_EXPONENT:
                    return ToolResult(
                        success=False,
                        output=None,
                        error=f"Exponent too large: abs(b) must be <= {self.POWER_MAX_EXPONENT}",
                    )
                result = a ** b
            else:
                return ToolResult(
                    success=False,
                    output=None,
                    error=f"Unsupported operation: {operation}",
                )

            return ToolResult(
                success=True,
                output={
                    "operation": operation,
                    "a": a,
                    "b": b,
                    "result": result,
                },
            )

        except Exception as e:
            return ToolResult(
                success=False,
                output=None,
                error=f"Execution failed: {str(e)}",
            )
