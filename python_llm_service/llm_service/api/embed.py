"""Cribug Embedding API — delegates to pluggable EmbeddingProvider.

Providers are selected via EMBEDDING_PROVIDER env var:
  openai  — OpenAI text-embedding-3-small (requires EMBEDDING_API_KEY)
  fake    — deterministic SHA256 fake embeddings (always available)

REAL_EMBEDDING_TEST=1 with EMBEDDING_PROVIDER=fake will fail explicitly.
"""
import logging
import os
from typing import List, Union

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

from ..providers.embeddings import create_embedding_provider
from ..providers.embeddings.base import EmbeddingResult
from ..providers.embeddings.openai_provider import EmbeddingError

logger = logging.getLogger(__name__)
router = APIRouter(tags=["embedding"])


# ── Pydantic models ──────────────────────────────────────────────────────────


class EmbedRequest(BaseModel):
    input: Union[str, List[str]]
    model: str = "text-embedding-3-small"
    provider: str = "openai"  # kept for backward compat, ignored


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


# ── Route ────────────────────────────────────────────────────────────────────


@router.post("/embed", response_model=EmbedResponse)
async def embed(req: EmbedRequest) -> EmbedResponse:
    """Produce embedding vectors for the given input text(s)."""

    # Normalise input
    texts = req.input if isinstance(req.input, list) else [req.input]
    if not texts:
        raise HTTPException(status_code=400, detail="input cannot be empty")

    provider = create_embedding_provider()
    provider_name = os.getenv("EMBEDDING_PROVIDER", "fake").strip().lower()
    is_fake = provider_name == "fake"

    # Use provider-specific default model if request model is generic
    model = req.model
    default_model = os.getenv("EMBEDDING_MODEL", "")
    if default_model and model == "text-embedding-3-small":
        model = default_model
    real_required = os.getenv("REAL_EMBEDDING_TEST", "") == "1"

    # Gate: REAL_EMBEDDING_TEST=1 + fake provider → fail explicitly
    if real_required and is_fake:
        raise HTTPException(
            status_code=400,
            detail=(
                "REAL_EMBEDDING_TEST=1 requires a real embedding provider. "
                "Current EMBEDDING_PROVIDER=fake. Set EMBEDDING_PROVIDER=openai "
                "and EMBEDDING_API_KEY."
            ),
        )

    try:
        result: EmbeddingResult = await provider.embed(texts, model)
    except EmbeddingError as exc:
        if real_required:
            raise HTTPException(status_code=502, detail=str(exc))
        raise HTTPException(status_code=502, detail=str(exc))
    except Exception as exc:
        logger.warning("Embedding provider exception: %s", exc)
        raise HTTPException(status_code=502, detail=f"Embedding failed: {exc}")

    data = [
        EmbeddingData(embedding=v, index=i)
        for i, v in enumerate(result.vectors)
    ]

    return EmbedResponse(
        object="list",
        data=data,
        model=result.model,
        usage=EmbedUsage(
            prompt_tokens=result.token_count,
            total_tokens=result.token_count,
        ),
        mock=is_fake,
    )
