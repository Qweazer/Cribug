"""Deterministic fake embedding provider (SHA256-based, no API key needed)."""
import hashlib
import os
from typing import List, Optional

from .base import EmbeddingProvider, EmbeddingResult


class FakeEmbeddingProvider(EmbeddingProvider):
    """Deterministic fake embeddings — always available, no API key."""

    async def embed(self, texts: List[str], model: Optional[str] = None) -> EmbeddingResult:
        dim = int(os.getenv("EMBEDDING_DIMENSION", "1536"))
        vectors = [_fake_vector(t, dim) for t in texts]
        return EmbeddingResult(
            vectors=vectors,
            model=model or "fake-embedding",
            dimension=dim,
            provider="fake",
            token_count=sum(max(1, len(t) // 4) for t in texts),
        )


def _fake_vector(text: str, dim: int) -> List[float]:
    h = hashlib.sha256(text.encode()).digest()
    vec = []
    for i in range(dim):
        b1 = h[i % len(h)]
        b2 = h[(i + 31) % len(h)]
        val = ((b1 << 8 | b2) / 65535.0) * 2.0 - 1.0
        vec.append(val)
    norm = sum(v * v for v in vec) ** 0.5
    if norm > 0:
        vec = [v / norm for v in vec]
    return vec
