from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import time

app = FastAPI()

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


@app.get("/health")
def health():
    return {"status": "healthy"}


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

    # Use mock content (role-based) but can incorporate user content
    content = mock_content

    # Calculate tokens based on content length
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
        error=None
    )