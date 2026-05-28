"""Tests for the /embed endpoint (llm_service/api/embed.py).

Uses FastAPI TestClient for synchronous testing.
5 mock tests always run; 2 real tests require REAL_EMBEDDING_TEST=1.
"""
import os
import pytest
from fastapi.testclient import TestClient

from app import app

client = TestClient(app)


class TestEmbed:
    """Mock embedding tests (always run)."""

    def test_embed_single_text_returns_correct_dimension(self):
        """Single text input returns a 1536-dimensional vector."""
        response = client.post("/embed", json={
            "input": "Hello world",
        })
        assert response.status_code == 200
        data = response.json()
        assert len(data["data"]) == 1
        assert len(data["data"][0]["embedding"]) == 1536

    def test_embed_batch_returns_multiple_vectors(self):
        """Batch of 3 texts returns 3 embedding vectors."""
        response = client.post("/embed", json={
            "input": ["text one", "text two", "text three"],
        })
        assert response.status_code == 200
        data = response.json()
        assert len(data["data"]) == 3
        for i, item in enumerate(data["data"]):
            assert len(item["embedding"]) == 1536
            assert item["index"] == i

    def test_embed_empty_input_rejected(self):
        """Empty input list returns 400."""
        response = client.post("/embed", json={
            "input": [],
        })
        assert response.status_code == 400

    def test_embed_string_input_accepted(self):
        """String input (not list) returns 1 vector."""
        response = client.post("/embed", json={
            "input": "single string",
        })
        assert response.status_code == 200
        data = response.json()
        assert len(data["data"]) == 1

    def test_embed_returns_model_and_usage(self):
        """Response includes model and usage fields."""
        response = client.post("/embed", json={
            "input": "test",
            "model": "text-embedding-3-small",
        })
        assert response.status_code == 200
        data = response.json()
        assert data["model"] == "text-embedding-3-small"
        assert "usage" in data
        assert data["usage"]["prompt_tokens"] > 0
        assert data["usage"]["total_tokens"] > 0


@pytest.mark.skipif(
    not os.environ.get("REAL_EMBEDDING_TEST"),
    reason="REAL_EMBEDDING_TEST not set",
)
class TestRealEmbed:
    """Real embedding tests (only with REAL_EMBEDDING_TEST=1)."""

    def test_real_embed_returns_1536d_vector(self):
        """Real OpenAI embedding returns 1536d vector."""
        response = client.post("/embed", json={
            "input": "Hello world",
            "provider": "openai",
        })
        if response.status_code == 502:
            pytest.skip("OpenAI API returned an error or is unavailable")
        assert response.status_code == 200
        data = response.json()
        assert len(data["data"][0]["embedding"]) == 1536

    def test_real_embed_no_api_key_fails_cleanly(self):
        """When no API key set, real embedding returns mock fallback or handled error."""
        saved = os.environ.pop("OPENAI_API_KEY", None)
        try:
            response = client.post("/embed", json={
                "input": "test",
                "provider": "openai",
            })
            # Should handle gracefully: either 200 (mock fallback) or a 502/400 error
            assert response.status_code in (200, 400, 502)
        finally:
            if saved is not None:
                os.environ["OPENAI_API_KEY"] = saved
