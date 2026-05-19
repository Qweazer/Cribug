from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import time

app = FastAPI()

# Role-based system prompts
ROLE_PROMPTS = {
    None: "You are a helpful assistant.",
    "planner": "You are a planner agent. Break down the user's query into a short plan.",
    "researcher": "You are a researcher agent. Gather relevant points and produce useful supporting information.",
    "critic": "You are a critic agent. Review the candidate answer and identify gaps, risks, weak reasoning, and concrete improvements. Be concise but specific.",
    "synthesizer": "You are a synthesizer agent. Combine the original query, intermediate outputs, and critique into a final polished answer.",
}

# Mock responses for different roles
ROLE_MOCK_RESPONSES = {
    None: "Here is a helpful response to your question.",
    "planner": "Based on your query, I recommend breaking this down into 2-3 steps. First, we gather information. Then we analyze. Finally, we synthesize a solution.",
    "researcher": "After researching this topic, I found several key points: (1) context matters, (2) structure helps clarity, (3) synthesis produces better outcomes.",
    "critic": "The current answer has several strengths but also areas for improvement: Strengths: clear structure, good examples. Areas for improvement: could address edge cases more thoroughly, and the conclusion could be more actionable.",
    "synthesizer": "In summary, synthesizing the analysis and critique: the best approach involves structured thinking, addressing concerns raised by the critique, and delivering a clear actionable conclusion.",
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