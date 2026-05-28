"""Cribug Embedding API - /embed endpoint with mock provider fallback.

Default behaviour: returns deterministic fake embeddings (SHA256-based) when
no OPENAI_API_KEY is set.  When OPENAI_API_KEY is present and provider="openai",
the real OpenAI /v1/embeddings endpoint is called via httpx.
"""
import hashlib
import os
from typing import List, Union

import httpx
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

router = APIRouter(tags=["embedding"])

# Configuration from environment
EMBEDDING_DIM = int(os.environ.get("EMBEDDING_DIM", "1536"))
EMBEDDING_TIMEOUT = int(os.environ.get("EMBEDDING_TIMEOUT_SECONDS", "30"))


# ── Pydantic models ──────────────────────────────────────────────────────────


class EmbedRequest(BaseModel):
    input: Union[str, List[str]]
    model: str = "text-embedding-3-small"
    provider: str = "openai"


class EmbeddingData(BaseModel):
    object: str = "embedding"
    embedding: List[float]
    index: int


class EmbedUsage(BaseModel):
    prompt_tokens: int
    total_tokens: int


class EmbedResponse(BaseModel):
    object: str = "list"
    data: List[EmbeddingData]
    model: str
    usage: EmbedUsage
    mock: bool = False


# ── Fake embedding algorithm ─────────────────────────────────────────────────


def _generate_fake_embedding(text: str, dim: int) -> List[float]:
    """Deterministic pseudo-embedding based on SHA-256 (no API key needed)."""
    h = hashlib.sha256(text.encode()).digest()
    vec: List[float] = []
    for i in range(dim):
        b1 = h[i % len(h)]
        b2 = h[(i + 31) % len(h)]
        val = ((b1 << 8 | b2) / 65535.0) * 2.0 - 1.0
        vec.append(val)
    # Normalise
    norm = sum(v * v for v in vec) ** 0.5
    if norm > 0:
        vec = [v / norm for v in vec]
    return vec


# ── Route ────────────────────────────────────────────────────────────────────


@router.post("/embed", response_model=EmbedResponse)
async def embed(req: EmbedRequest) -> EmbedResponse:
    """Produce an embedding vector for the given input text(s).

    *Real*:      when ``OPENAI_API_KEY`` is set and ``provider="openai"``.
    *Mock*:      otherwise – deterministic SHA-256-based fake embeddings.
    """
    # Normalise input to always be a list of strings
    inputs = req.input
    if isinstance(inputs, str):
        inputs = [inputs]

    if not inputs:
        raise HTTPException(status_code=400, detail="input cannot be empty")

    api_key = os.environ.get("OPENAI_API_KEY")

    # ── Real OpenAI path ────────────────────────────────────────────────
    if api_key and req.provider == "openai":
        try:
            async with httpx.AsyncClient(timeout=EMBEDDING_TIMEOUT) as client:
                resp = await client.post(
                    "https://api.openai.com/v1/embeddings",
                    headers={
                        "Authorization": f"Bearer {api_key}",
                        "Content-Type": "application/json",
                    },
                    json={"input": inputs, "model": req.model},
                )
                resp.raise_for_status()
                body = resp.json()

            data = [
                EmbeddingData(embedding=item["embedding"], index=item["index"])
                for item in body["data"]
            ]

            return EmbedResponse(
                data=data,
                model=body["model"],
                usage=EmbedUsage(
                    prompt_tokens=body["usage"]["prompt_tokens"],
                    total_tokens=body["usage"]["total_tokens"],
                ),
                mock=False,
            )
        except Exception as exc:
            raise HTTPException(status_code=502, detail=str(exc))

    # ── Mock path (fallback) ────────────────────────────────────────────
    data = [
        EmbeddingData(
            embedding=_generate_fake_embedding(text, EMBEDDING_DIM),
            index=i,
        )
        for i, text in enumerate(inputs)
    ]

    total_tokens = sum(max(1, len(t) // 4) for t in inputs)

    return EmbedResponse(
        data=data,
        model=req.model,
        usage=EmbedUsage(
            prompt_tokens=total_tokens,
            total_tokens=total_tokens,
        ),
        mock=True,
    )
