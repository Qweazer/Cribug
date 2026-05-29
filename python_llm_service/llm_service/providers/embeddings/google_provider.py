"""Google Gemini embedding provider."""
import os
from typing import List, Optional

import httpx

from .base import EmbeddingProvider, EmbeddingResult


class EmbeddingError(Exception):
    """Raised when the embedding provider fails."""


class GoogleEmbeddingProvider(EmbeddingProvider):
    """Google Gemini Embedding (text-embedding-004, gemini-embedding-001, etc.).

    API: POST /v1beta/models/{model}:embedContent?key=API_KEY
    """

    def __init__(self):
        self.api_key = (os.getenv("EMBEDDING_API_KEY") or os.getenv("OPENAI_API_KEY") or "").strip()
        self.default_model = os.getenv("EMBEDDING_MODEL", "models/gemini-embedding-001").strip()
        self.dimension = int(os.getenv("EMBEDDING_DIMENSION", "3072"))
        self.timeout = int(os.getenv("EMBEDDING_TIMEOUT_SECONDS", "30"))

    async def embed(self, texts: List[str], model: Optional[str] = None) -> EmbeddingResult:
        if not self.api_key:
            raise EmbeddingError("EMBEDDING_API_KEY is required for Google embedding provider.")

        model = model or self.default_model
        if not model.startswith("models/"):
            model = f"models/{model}"
        vectors = []
        total_tokens = 0

        async with httpx.AsyncClient(timeout=self.timeout) as client:
            for text in texts:
                url = (
                    "https://generativelanguage.googleapis.com/v1beta/"
                    f"{model}:embedContent?key={self.api_key}"
                )
                resp = await client.post(
                    url,
                    headers={"Content-Type": "application/json"},
                    json={
                        "model": model,
                        "content": {"parts": [{"text": text}]},
                    },
                )
                if resp.status_code != 200:
                    raise EmbeddingError(
                        f"Google embedding failed (HTTP {resp.status_code}) "
                        f"model={model}: {resp.text[:200]}"
                    )
                body = resp.json()
                if "embedding" not in body:
                    raise EmbeddingError(f"Unexpected Google embedding response: {list(body.keys())}")

                vec = body["embedding"]["values"]
                if len(vec) > self.dimension:
                    vec = vec[:self.dimension]
                vectors.append(vec)
                total_tokens += len(text) // 4

        return EmbeddingResult(
            vectors=vectors,
            model=model,
            dimension=self.dimension,
            provider="google",
            token_count=total_tokens,
        )
