from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from functools import lru_cache
import tiktoken
import time
import os

from adapters.openai import build_client, chat as real_chat

MODEL_TO_ENCODING = {
    "gpt-4o": "o200k_base", "gpt-4o-mini": "o200k_base",
    "gpt-4o-2024-05-13": "o200k_base", "gpt-4": "cl100k_base",
    "gpt-4-0314": "cl100k_base", "gpt-3.5-turbo": "cl100k_base",
    "gpt-3.5-turbo-0301": "cl100k_base",
}

@lru_cache(maxsize=256)
def get_encoding(model: str):
    encoding_name = MODEL_TO_ENCODING.get(model, "cl100k_base")
    return tiktoken.get_encoding(encoding_name)

app = FastAPI()

from llm_service.api.skills import router as skills_router
app.include_router(skills_router)
from llm_service.api.embed import router as embed_router
app.include_router(embed_router)

_real_client = build_client()

ROLE_PROMPTS = {
    None: "You are a helpful assistant.",
    "planner": "You are a planner agent. Break down the user's query into a short plan.",
    "researcher": "You are a researcher agent. Gather relevant points and produce useful supporting information.",
    "critic": "You are a critic agent. Review the candidate answer and identify gaps, risks, weak reasoning, and concrete improvements. Be concise but specific.",
    "synthesizer": "You are a synthesizer agent. Combine the original query, intermediate outputs, and critique into a final polished answer.",
}

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
    role: str | None = None
    metadata: dict = {}
    # Request-level config overrides (Provider Config Foundation)
    llm_base_url: str | None = None
    llm_api_key_env: str | None = None
    require_real: bool = False
    allow_mock_fallback: bool = True

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
    mode: str = "unknown"          # "real" | "mock"
    fallback_used: bool = False

class TokenizeRequest(BaseModel):
    text: str
    model: str = "gpt-4o-mini"

@app.get("/health")
def health():
    return {
        "status": "healthy",
        "mode": "real" if _real_client is not None else "mock",
        "provider": "openai_compatible",
        "model": os.environ.get("LLM_MODEL", "(from env)"),
    }

@app.post("/tokenize")
def tokenize(req: TokenizeRequest):
    try:
        encoding = get_encoding(req.model)
        tokens = encoding.encode(req.text)
        return {"token_count": len(tokens), "encoding": encoding.name, "model": req.model}
    except Exception:
        try:
            encoding = tiktoken.get_encoding("cl100k_base")
            return {"token_count": len(encoding.encode(req.text)), "encoding": encoding.name, "model": req.model, "fallback": True}
        except Exception:
            return {"token_count": max(1, len(req.text) // 4), "encoding": "fallback", "model": req.model, "fallback": True}

@app.post("/chat")
def chat(req: LLMRequest):
    if req.provider != "openai_compatible":
        raise HTTPException(status_code=400, detail="provider must be openai_compatible")
    if not req.messages:
        raise HTTPException(status_code=400, detail="messages cannot be empty")

    start = time.time()

    # ── Resolve model ──────────────────────────────────────────────────
    # Priority: request-level model > LLM_MODEL env (dev fallback only)
    env_model = os.environ.get("LLM_MODEL", "")
    effective_model = req.model
    if not effective_model or effective_model == "gpt-4o-mini":
        if env_model and req.require_real:
            effective_model = env_model  # only use env model when require_real
        elif env_model:
            effective_model = env_model

    # ── Build per-request client if config overrides provided ─────────
    client = _real_client
    used_client = client
    if req.llm_base_url or req.llm_api_key_env:
        api_key = None
        if req.llm_api_key_env:
            api_key = os.environ.get(req.llm_api_key_env)
        if not api_key:
            api_key = os.environ.get("LLM_API_KEY") or os.environ.get("OPENAI_API_KEY")
        if api_key:
            used_client = build_client(api_key=api_key, base_url=req.llm_base_url)

    # ── Fail-fast: require_real but no client ─────────────────────────
    if req.require_real and used_client is None:
        api_key_env = req.llm_api_key_env or "LLM_API_KEY"
        raise HTTPException(
            status_code=503,
            detail=f"require_real=true but no API key found (env: {api_key_env}). Cannot proceed without real LLM."
        )

    role = req.role
    if not role and req.metadata.get("agent_role"):
        role = req.metadata.get("agent_role")

    mock_content = ROLE_MOCK_RESPONSES.get(role, ROLE_MOCK_RESPONSES[None])

    last_user_content = None
    for msg in reversed(req.messages):
        if msg.role == "user":
            last_user_content = msg.content
            break
    if last_user_content is None:
        raise HTTPException(status_code=400, detail="no user message found")

    # ── Real LLM path ──────────────────────────────────────────────────
    if used_client is not None:
        try:
            messages = []
            for msg in req.messages:
                role = msg.role
                if role not in ("system", "user", "assistant"):
                    role = "user"
                messages.append({"role": role, "content": msg.content})

            result = real_chat(
                client=used_client,
                model=effective_model,
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
                mode="real",
                fallback_used=False,
            )
        except Exception as e:
            if req.require_real:
                raise HTTPException(
                    status_code=502,
                    detail=f"require_real=true but real LLM call failed: {str(e)[:200]}"
                )
            print(f"[WARN] Real LLM call failed, falling back to mock: {e}")

    # ── Mock fallback ──────────────────────────────────────────────────
    if req.require_real:
        raise HTTPException(status_code=503, detail="require_real=true but no LLM client available")
    content = mock_content
    prompt_text = f"{last_user_content}"
    prompt_tokens = max(1, len(prompt_text) // 4)
    return LLMResponse(
        content=content,
        usage=Usage(prompt_tokens=prompt_tokens, completion_tokens=len(content)//4, total_tokens=prompt_tokens+len(content)//4),
        model="mock",
        provider="mock",
        finish_reason="stop",
        latency_ms=int((time.time() - start) * 1000),
        error=None,
        mode="mock",
        fallback_used=True,
    )
