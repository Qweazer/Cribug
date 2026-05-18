from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import time

app = FastAPI()


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


@app.get("/health")
def health():
    return {"status": "healthy"}


@app.post("/chat")
def chat(req: LLMRequest):
    if req.provider != "openai_compatible":
        raise HTTPException(status_code=400, detail="provider must be openai_compatible")
    if not req.messages:
        raise HTTPException(status_code=400, detail="messages cannot be empty")

    last_user_content = None
    for msg in reversed(req.messages):
        if msg.role == "user":
            last_user_content = msg.content
            break

    if last_user_content is None:
        raise HTTPException(status_code=400, detail="no user message found")

    start = time.time()
    content = f"mock answer: {last_user_content}"

    prompt_text = " ".join(m.content for m in req.messages)
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
        error=None
    )