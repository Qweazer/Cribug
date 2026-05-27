"""Echo Skill: A simple tool that echoes input text back.

This is a minimal, stateless tool intended as a reference implementation
for the Cribug Python Skills System. It requires no sandbox, no LLM,
no network, and no file access.
"""

from typing import Any, Dict, List, Optional

from llm_service.tools.base import (
    Tool,
    ToolMetadata,
    ToolParameter,
    ToolParameterType,
    ToolResult,
)


class EchoTool(Tool):
    """Tool that echoes the provided text back.

    Parameters:
        text: The string to echo back.
        uppercase: If True, the text is converted to uppercase.
    """

    def _get_metadata(self) -> ToolMetadata:
        return ToolMetadata(
            name="echo_skill",
            version="1.0.0",
            description="Echoes the provided text back to the caller",
            category="utility",
            author="Cribug",
            execution_mode="python_inline",
            requires_sandbox=False,
            requires_llm=False,
            risk_level="low",
            side_effects=False,
        )

    def _get_parameters(self) -> List[ToolParameter]:
        return [
            ToolParameter(
                name="text",
                type=ToolParameterType.STRING,
                description="The text to echo back",
                required=True,
            ),
            ToolParameter(
                name="uppercase",
                type=ToolParameterType.BOOLEAN,
                description="Whether to convert the text to uppercase",
                required=False,
                default=False,
            ),
        ]

    async def _execute_impl(
        self,
        session_context: Optional[Dict[str, Any]],
        observer: Optional[Any],
        **kwargs,
    ) -> ToolResult:
        """Echo the text back, optionally uppercasing it."""
        text = kwargs["text"]
        uppercase = kwargs.get("uppercase", False)

        if uppercase:
            result_text = text.upper()
        else:
            result_text = text

        output = {"text": result_text, "length": len(result_text)}
        return ToolResult(success=True, output=output)
