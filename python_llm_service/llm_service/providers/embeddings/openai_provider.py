"""OpenAI / OpenAI-compatible embedding provider."""
import os
from typing import List, Optional

import httpx

from .base import EmbeddingProvider, EmbeddingResult


class EmbeddingError(Exception):
    """Raised when the embedding provider fails."""


class OpenAIEmbeddingProvider(EmbeddingProvider):
    """OpenAI text-embedding-3-small (or compatible)."""

    def __init__(self):
        self.api_key = os.getenv("EMBEDDING_API_KEY") or os.getenv("OPENAI_API_KEY")
        self.base_url = os.getenv("EMBEDDING_BASE_URL") or os.getenv("LLM_BASE_URL", "https://api.openai.com")
        self.default_model = os.getenv("EMBEDDING_MODEL", "text-embedding-3-small")
        self.dimension = int(os.getenv("EMBEDDING_DIMENSION", "1536"))
        self.timeout = int(os.getenv("EMBEDDING_TIMEOUT_SECONDS", "30"))

    async def embed(self, texts: List[str], model: Optional[str] = None) -> EmbeddingResult:
        if not self.api_key:
            raise EmbeddingError(
                "EMBEDDING_API_KEY or OPENAI_API_KEY is required for OpenAI embedding provider. "
                "Set EMBEDDING_PROVIDER=fake for deterministic fake embeddings."
            )

        model = model or self.default_model
        url = f"{self.base_url.rstrip('/')}/embeddings"

        async with httpx.AsyncClient(timeout=self.timeout) as client:
            resp = await client.post(
                url,
                headers={
                    "Authorization": f"Bearer {self.api_key}",
                    "Content-Type": "application/json",
                },
                json={"input": texts, "model": model},
            )

            if resp.status_code != 200:
                raise EmbeddingError(
                    f"OpenAI embedding failed (HTTP {resp.status_code}): {resp.text[:300]}"
                )

            body = resp.json()

        if "data" not in body:
            raise EmbeddingError(f"Unexpected embedding response: {list(body.keys())}")

        vectors = [item["embedding"] for item in body["data"]]
        usage = body.get("usage", {})
        result_model = body.get("model", model)

        return EmbeddingResult(
            vectors=vectors,
            model=result_model,
            dimension=self.dimension,
            provider="openai",
            token_count=usage.get("total_tokens", 0),
        )
