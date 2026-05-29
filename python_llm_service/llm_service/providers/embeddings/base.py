"""Abstract base class for embedding providers + factory function."""
import os
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import List, Optional


@dataclass
class EmbeddingResult:
    vectors: List[List[float]]
    model: str
    dimension: int
    provider: str
    token_count: int = 0


class EmbeddingProvider(ABC):
    """Abstract embedding provider."""

    @abstractmethod
    async def embed(self, texts: List[str], model: Optional[str] = None) -> EmbeddingResult:
        """Generate embeddings. Raise EmbeddingError on failure."""
        ...


def create_embedding_provider() -> "EmbeddingProvider":
    """Factory: instantiate the embedding provider from env config."""
    provider_name = os.getenv("EMBEDDING_PROVIDER", "fake").strip().lower()
    if provider_name == "openai":
        from .openai_provider import OpenAIEmbeddingProvider
        return OpenAIEmbeddingProvider()
    elif provider_name == "google":
        from .google_provider import GoogleEmbeddingProvider
        return GoogleEmbeddingProvider()
    else:
        from .fake_provider import FakeEmbeddingProvider
        return FakeEmbeddingProvider()
