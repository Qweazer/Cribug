"""LLM Summary Skill - Real LLM text summarization (NO MOCK)"""
import os
from typing import Optional, Dict, Any, List
from ..base import Tool, ToolMetadata, ToolParameter, ToolParameterType, ToolResult


class LLMSummaryTool(Tool):
    """Summarizes text using real LLM provider.

    Execution mode: python_llm
    Requires: OPENAI_API_KEY or ANTHROPIC_API_KEY or GOOGLE_API_KEY
    NO MOCK - must call real LLM provider
    """

    VALID_STYLES = {"concise", "bullets", "technical"}
    DEFAULT_MAX_LENGTH = 200
    MAX_INPUT_LENGTH = 50000
    MAX_OUTPUT_LENGTH = 10000
    LLM_TIMEOUT_SECONDS = 30

    def _get_metadata(self) -> ToolMetadata:
        return ToolMetadata(
            name="llm_summary_skill",
            version="1.0.0",
            description="Summarizes text using real LLM provider",
            category="llm",
            author="Cribug",
            execution_mode="python_llm",
            requires_sandbox=False,
            requires_llm=True,
            risk_level="medium",
            side_effects=False,
            timeout_seconds=self.LLM_TIMEOUT_SECONDS + 10,
            rate_limit=10,
            cost_per_use=0.001,
            requires_auth=True,
            sandboxed=True,
        )

    def _get_parameters(self) -> List[ToolParameter]:
        return [
            ToolParameter(
                name="text",
                type=ToolParameterType.STRING,
                description="Text to summarize",
                required=True,
            ),
            ToolParameter(
                name="max_length",
                type=ToolParameterType.INTEGER,
                description="Maximum length of summary in characters",
                required=False,
                default=self.DEFAULT_MAX_LENGTH,
                min_value=10,
                max_value=2000,
            ),
            ToolParameter(
                name="style",
                type=ToolParameterType.STRING,
                description="Summary style: concise, bullets, technical",
                required=False,
                default="concise",
                enum=["concise", "bullets", "technical"],
            ),
            ToolParameter(
                name="language",
                type=ToolParameterType.STRING,
                description="Language for summary (en or zh)",
                required=False,
                default="zh",
            ),
        ]

    def _get_llm_config(self) -> Dict[str, str]:
        """Read LLM configuration from environment variables.

        Priority:
        1. MINIMAX_API_KEY -> provider=minimax, model from LLM_MODEL or MiniMax-M2.7
        2. LLM_API_KEY + LLM_PROVIDER=minimax -> provider=minimax
        3. LLM_API_KEY + LLM_PROVIDER -> generic openai_compatible
        4. OPENAI_API_KEY -> provider=openai
        5. ANTHROPIC_API_KEY -> provider=anthropic
        6. GOOGLE_API_KEY -> provider=google
        """
        config = {}

        # MiniMax (dedicated)
        if os.getenv("MINIMAX_API_KEY"):
            config["provider"] = "minimax"
            config["api_key"] = os.getenv("MINIMAX_API_KEY")
            config["model"] = os.getenv("LLM_MODEL") or os.getenv("MINIMAX_MODEL") or "MiniMax-M2.7"
            config["base_url"] = os.getenv("MINIMAX_BASE_URL") or "https://api.minimaxi.com/v1"
            return config

        # Generic LLM_API_KEY with LLM_PROVIDER (supports minimax, openai_compatible)
        llm_api_key = os.getenv("LLM_API_KEY")
        llm_provider = os.getenv("LLM_PROVIDER", "").lower()
        if llm_api_key:
            config["api_key"] = llm_api_key
            config["model"] = os.getenv("LLM_MODEL") or "MiniMax-M2.7"
            if llm_provider == "minimax":
                config["provider"] = "minimax"
                config["base_url"] = os.getenv("LLM_BASE_URL") or os.getenv("MINIMAX_BASE_URL") or "https://api.minimaxi.com/v1"
            else:
                config["provider"] = llm_provider or "openai_compatible"
                config["base_url"] = os.getenv("LLM_BASE_URL") or os.getenv("OPENAI_BASE_URL")
            return config

        # OpenAI
        if os.getenv("OPENAI_API_KEY"):
            config["provider"] = "openai"
            config["api_key"] = os.getenv("OPENAI_API_KEY")
            config["model"] = os.getenv("OPENAI_MODEL") or os.getenv("LLM_MODEL") or "gpt-4o-mini"
            config["base_url"] = os.getenv("LLM_BASE_URL") or os.getenv("OPENAI_BASE_URL")
            return config

        # Anthropic
        if os.getenv("ANTHROPIC_API_KEY"):
            config["provider"] = "anthropic"
            config["api_key"] = os.getenv("ANTHROPIC_API_KEY")
            config["model"] = os.getenv("ANTHROPIC_MODEL") or os.getenv("LLM_MODEL") or "claude-3-haiku-20240307"
            return config

        # Google
        if os.getenv("GOOGLE_API_KEY"):
            config["provider"] = "google"
            config["api_key"] = os.getenv("GOOGLE_API_KEY")
            config["model"] = os.getenv("GOOGLE_MODEL") or os.getenv("LLM_MODEL") or "gemini-pro"
            return config

        return {}

    def _build_summary_prompt(self, text: str, style: str, language: str, max_length: int) -> str:
        """Build the LLM prompt for summarization"""
        style_instruction = {
            "concise": "Provide a concise summary in the target language.",
            "bullets": "Provide a bullet-point summary in the target language.",
            "technical": "Provide a technical summary with key points and details.",
        }.get(style, "Provide a concise summary.")

        lang_instruction = {
            "en": "Write the summary in English.",
            "zh": "用目标语言（中文）撰写摘要。",
        }.get(language, f"Write the summary in {language}.")

        return f"""Summarize the following text.
Requirements:
- Summary must be no more than {max_length} characters
- {style_instruction}
- {lang_instruction}

Text to summarize:
{text}

Summary:"""

    async def _execute_impl(
        self,
        session_context: Optional[Dict] = None,
        observer: Optional[Any] = None,
        **kwargs
    ) -> ToolResult:
        text = kwargs.get("text", "").strip()
        max_length = kwargs.get("max_length", self.DEFAULT_MAX_LENGTH)
        style = kwargs.get("style", "concise")
        language = kwargs.get("language", "zh")

        if not text:
            return ToolResult(
                success=False,
                output=None,
                error="Input text cannot be empty",
            )

        if len(text) > self.MAX_INPUT_LENGTH:
            return ToolResult(
                success=False,
                output=None,
                error=f"Input text too large: {len(text)} chars (max {self.MAX_INPUT_LENGTH})",
            )

        if style not in self.VALID_STYLES:
            return ToolResult(
                success=False,
                output=None,
                error=f"Invalid style: {style}. Valid: {self.VALID_STYLES}",
            )

        config = self._get_llm_config()
        if not config or not config.get("api_key"):
            return ToolResult(
                success=False,
                output=None,
                error="LLM configuration missing: no API key found. Set OPENAI_API_KEY, ANTHROPIC_API_KEY, or GOOGLE_API_KEY",
            )

        provider = config["provider"]
        model = config["model"]
        api_key = config["api_key"]
        base_url = config.get("base_url")

        prompt = self._build_summary_prompt(text, style, language, max_length)

        try:
            import httpx

            headers = {"Authorization": f"Bearer {api_key}"}
            payload = {
                "model": model,
                "messages": [{"role": "user", "content": prompt}],
                "max_tokens": min(max_length, 2000),
                "temperature": 0.3,
            }

            if base_url:
                url = f"{base_url.rstrip('/')}/chat/completions"
            else:
                if provider == "openai":
                    url = "https://api.openai.com/v1/chat/completions"
                elif provider == "anthropic":
                    headers = {"x-api-key": api_key, "anthropic-version": "2023-06-01"}
                    payload = {
                        "model": model,
                        "messages": [{"role": "user", "content": prompt}],
                        "max_tokens": min(max_length, 2000),
                        "temperature": 0.3,
                    }
                    url = "https://api.anthropic.com/v1/messages"
                else:
                    url = "https://api.openai.com/v1/chat/completions"

            async with httpx.AsyncClient(timeout=self.LLM_TIMEOUT_SECONDS) as client:
                response = await client.post(url, json=payload, headers=headers)

                if response.status_code != 200:
                    error_body = response.text
                    return ToolResult(
                        success=False,
                        output=None,
                        error=f"LLM provider error ({response.status_code}): {error_body[:200]}",
                    )

                result_data = response.json()

            if provider == "anthropic":
                summary = result_data.get("content", [{}])[0].get("text", "")
            else:
                choices = result_data.get("choices", [])
                summary = choices[0].get("message", {}).get("content", "") if choices else ""

            overflow = len(summary) > self.MAX_OUTPUT_LENGTH
            if overflow:
                summary = summary[:self.MAX_OUTPUT_LENGTH] + "..."

            usage = result_data.get("usage", {})
            token_usage = {
                "input_tokens": usage.get("prompt_tokens", 0),
                "output_tokens": usage.get("completion_tokens", 0),
                "total_tokens": usage.get("total_tokens", 0),
            }

            return ToolResult(
                success=True,
                output={
                    "summary": summary,
                    "style": style,
                    "language": language,
                    "provider": provider,
                    "model": model,
                    "token_usage": token_usage,
                },
                metadata={"overflow": overflow, "provider": provider, "model": model, "token_usage": token_usage},
            )

        except Exception as e:
            error_str = str(e)
            if "timeout" in error_str.lower():
                return ToolResult(success=False, output=None, error="LLM timeout")
            return ToolResult(success=False, output=None, error=f"LLM execution failed: {error_str}")
