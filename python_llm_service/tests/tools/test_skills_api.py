"""Tests for Skills API endpoints"""
import pytest
from httpx import AsyncClient, ASGITransport
from llm_service.api.skills import router, register_cribug_tools
from llm_service.tools import get_registry


@pytest.fixture(scope="module")
def _register_tools():
    register_cribug_tools()


@pytest.fixture
async def client(_register_tools):
    transport = ASGITransport(app=router)
    async with AsyncClient(transport=transport, base_url="http://test") as ac:
        yield ac


@pytest.mark.asyncio
async def test_list_skills(client):
    response = await client.get("/tools")
    assert response.status_code == 200
    data = response.json()
    assert isinstance(data, list)
    assert "echo_skill" in data
    assert "safe_math_skill" in data
    assert "llm_summary_skill" in data


@pytest.mark.asyncio
async def test_get_skill_metadata(client):
    response = await client.get("/tools/echo_skill")
    assert response.status_code == 200
    data = response.json()
    assert data["name"] == "echo_skill"
    assert data["execution_mode"] == "python_inline"


@pytest.mark.asyncio
async def test_get_nonexistent_skill(client):
    response = await client.get("/tools/nonexistent_skill")
    assert response.status_code == 404


@pytest.mark.asyncio
async def test_execute_echo_skill(client):
    response = await client.post(
        "/tools/echo_skill/execute",
        json={"parameters": {"text": "hello", "uppercase": True}}
    )
    assert response.status_code == 200
    data = response.json()
    assert data["success"] is True
    assert data["output"]["text"] == "HELLO"


@pytest.mark.asyncio
async def test_execute_math_skill(client):
    response = await client.post(
        "/tools/safe_math_skill/execute",
        json={"parameters": {"operation": "add", "a": 2, "b": 3}}
    )
    assert response.status_code == 200
    data = response.json()
    assert data["success"] is True
    assert data["output"]["result"] == 5


@pytest.mark.asyncio
async def test_execute_math_error(client):
    response = await client.post(
        "/tools/safe_math_skill/execute",
        json={"parameters": {"operation": "divide", "a": 1, "b": 0}}
    )
    assert response.status_code == 200
    data = response.json()
    assert data["success"] is False


@pytest.mark.asyncio
async def test_execute_nonexistent_skill(client):
    response = await client.post(
        "/tools/nonexistent_skill/execute",
        json={"parameters": {}}
    )
    assert response.status_code == 404
