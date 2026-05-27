from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from functools import lru_cache
import tiktoken
import time
import os

from adapters.openai import build_client, chat as real_chat

# tiktoken encoding mappings
MODEL_TO_ENCODING = {
    "gpt-4o": "o200k_base",
    "gpt-4o-mini": "o200k_base",
    "gpt-4o-2024-05-13": "o200k_base",
    "gpt-4": "cl100k_base",
    "gpt-4-0314": "cl100k_base",
    "gpt-3.5-turbo": "cl100k_base",
    "gpt-3.5-turbo-0301": "cl100k_base",
}

@lru_cache(maxsize=256)
def get_encoding(model: str):
    """Get tiktoken encoding for a model (cached)."""
    encoding_name = MODEL_TO_ENCODING.get(model, "cl100k_base")
    return tiktoken.get_encoding(encoding_name)

app = FastAPI()

# Phase 6C: Skills System
from llm_service.api.skills import router as skills_router
app.include_router(skills_router)

# Build real LLM client from env vars (falls back to mock if no API key)
_real_client = build_client()

# Role-based system prompts (safe, non-sensitive content)
ROLE_PROMPTS = {
    None: "You are a helpful assistant.",
    "planner": "You are a planner agent. Break down the user's query into a short plan.",
    "researcher": "You are a researcher agent. Gather relevant points and produce useful supporting information.",
    "critic": "You are a critic agent. Review the candidate answer and identify gaps, risks, weak reasoning, and concrete improvements. Be concise but specific.",
    "synthesizer": "You are a synthesizer agent. Combine the original query, intermediate outputs, and critique into a final polished answer.",
}

# Mock responses for different roles (safe, non-sensitive, short content)
ROLE_MOCK_RESPONSES = {
    None: "Safe mock response.",
    "planner": "Plan: analyze query, gather info, synthesize answer.",
    "researcher": "Key points: structure, clarity, synthesis.",
    "critic": "Strengths: clear structure. Improvements: address edge cases.",
    "synthesizer": "Final answer: structured approach with actionable conclusion.",
}


class LLMMessage(BaseModel):
    role: str
    content: str


class LLMRequest(BaseModel):
    trace_id: str
    task_id: str
    session_id: str | None = None
    provider: str
    model: str
    messages: list[LLMMessage]
    temperature: float = 0.7
    max_completion_tokens: int = 1024
    response_format: str | None = None
    role: str | None = None  # "planner" | "researcher" | "critic" | "synthesizer"
    metadata: dict = {}


class Usage(BaseModel):
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int


class LLMResponse(BaseModel):
    content: str
    usage: Usage
    model: str
    provider: str
    finish_reason: str
    provider_response_id: str | None = None
    latency_ms: int
    error: str | None = None


class TokenizeRequest(BaseModel):
    text: str
    model: str = "gpt-4o-mini"


@app.get("/health")
def health():
    return {
        "status": "healthy",
        "mode": "real" if _real_client is not None else "mock",
        "provider": "openai_compatible",
        "model": os.environ.get("LLM_MODEL", "gpt-4o-mini"),
    }


@app.post("/tokenize")
def tokenize(req: TokenizeRequest):
    """Count tokens using tiktoken."""
    try:
        encoding = get_encoding(req.model)
        tokens = encoding.encode(req.text)
        return {
            "token_count": len(tokens),
            "encoding": encoding.name,
            "model": req.model
        }
    except Exception as e:
        # Fallback for unknown models
        try:
            encoding = tiktoken.get_encoding("cl100k_base")
            tokens = encoding.encode(req.text)
            return {
                "token_count": len(tokens),
                "encoding": encoding.name,
                "model": req.model,
                "fallback": True
            }
        except Exception:
            # Ultimate fallback: rough estimate
            return {
                "token_count": max(1, len(req.text) // 4),
                "encoding": "fallback",
                "model": req.model,
                "fallback": True
            }


@app.post("/chat")
def chat(req: LLMRequest):
    if req.provider != "openai_compatible":
        raise HTTPException(status_code=400, detail="provider must be openai_compatible")
    if not req.messages:
        raise HTTPException(status_code=400, detail="messages cannot be empty")

    start = time.time()

    # Get system prompt based on role (use role from request body or from metadata)
    role = req.role
    if not role and req.metadata.get("agent_role"):
        role = req.metadata.get("agent_role")

    system_prompt = ROLE_PROMPTS.get(role, ROLE_PROMPTS[None])

    # Get mock response based on role
    mock_content = ROLE_MOCK_RESPONSES.get(role, ROLE_MOCK_RESPONSES[None])

    # Extract user message content
    last_user_content = None
    for msg in reversed(req.messages):
        if msg.role == "user":
            last_user_content = msg.content
            break

    if last_user_content is None:
        raise HTTPException(status_code=400, detail="no user message found")

    # ── Real LLM path ──────────────────────────────────────────────
    if _real_client is not None:
        try:
            messages = []
            for msg in req.messages:
                # Normalize system/user/assistant roles; skip unsupported roles
                role = msg.role
                if role not in ("system", "user", "assistant"):
                    role = "user"
                messages.append({"role": role, "content": msg.content})

            result = real_chat(
                client=_real_client,
                model=req.model,
                messages=messages,
                temperature=req.temperature,
                max_completion_tokens=req.max_completion_tokens,
            )

            usage = result["usage"]
            return LLMResponse(
                content=result["content"],
                usage=Usage(
                    prompt_tokens=usage["prompt_tokens"],
                    completion_tokens=usage["completion_tokens"],
                    total_tokens=usage["total_tokens"],
                ),
                model=result["model"],
                provider=req.provider,
                finish_reason=result["finish_reason"],
                provider_response_id=result.get("provider_response_id"),
                latency_ms=result["latency_ms"],
                error=None,
            )
        except Exception as e:
            # Real LLM failed — fall back to mock with error logged
            print(f"[WARN] Real LLM call failed, falling back to mock: {e}")
            # Fall through to mock path below

    # ── Mock path (fallback) ──────────────────────────────────────
    content = mock_content

    prompt_text = f"{system_prompt} {last_user_content}"
    prompt_tokens = max(1, len(prompt_text) // 4)
    completion_tokens = max(1, len(content) // 4)

    latency_ms = int((time.time() - start) * 1000)

    return LLMResponse(
        content=content,
        usage=Usage(
            prompt_tokens=prompt_tokens,
            completion_tokens=completion_tokens,
            total_tokens=prompt_tokens + completion_tokens
        ),
        model=req.model,
        provider=req.provider,
        finish_reason="stop",
        provider_response_id=f"mock-{req.task_id}",
        latency_ms=latency_ms,
        error=None,
    )