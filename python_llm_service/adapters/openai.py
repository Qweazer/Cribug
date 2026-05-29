"""OpenAI-compatible adapter for real LLM calls (MiniMax, OpenAI, etc.)."""

import os
import time
from openai import OpenAI


def build_client(api_key: str | None = None, base_url: str | None = None) -> OpenAI | None:
    """Build an OpenAI-compatible client. Returns None if no API key is configured."""
    key = api_key or os.environ.get("LLM_API_KEY") or os.environ.get("OPENAI_API_KEY")
    if not key:
        return None
    url = base_url or os.environ.get("LLM_BASE_URL") or os.environ.get("OPENAI_BASE_URL")
    kwargs = {"api_key": key}
    if url:
        kwargs["base_url"] = url
    return OpenAI(**kwargs)


def chat(
    client: OpenAI,
    model: str,
    messages: list[dict],
    temperature: float = 0.7,
    max_completion_tokens: int = 1024,
) -> dict:
    """Call the real LLM and return content + usage dict."""
    start = time.time()

    resp = client.chat.completions.create(
        model=model,
        messages=messages,
        temperature=temperature,
        max_completion_tokens=max_completion_tokens,
    )

    latency_ms = int((time.time() - start) * 1000)

    choice = resp.choices[0]
    content = choice.message.content or ""

    usage = {
        "prompt_tokens": resp.usage.prompt_tokens if resp.usage else 0,
        "completion_tokens": resp.usage.completion_tokens if resp.usage else 0,
        "total_tokens": resp.usage.total_tokens if resp.usage else 0,
    }

    return {
        "content": content,
        "usage": usage,
        "model": resp.model or model,
        "finish_reason": choice.finish_reason or "stop",
        "provider_response_id": resp.id,
        "latency_ms": latency_ms,
    }
