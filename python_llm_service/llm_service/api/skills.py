"""Cribug Skills API - REST endpoints for Go orchestrator integration"""
import uuid
from typing import Dict, Any, List, Optional
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field
from ..tools import get_registry

router = APIRouter(prefix="/tools", tags=["skills"])


class ExecuteRequest(BaseModel):
    parameters: Dict[str, Any] = Field(default_factory=dict, description="Skill parameters")
    session_context: Optional[Dict[str, Any]] = Field(default=None)
    request_id: Optional[str] = Field(default_factory=lambda: uuid.uuid4().hex[:12])
    agent_id: Optional[str] = Field(default=None)
    workflow_id: Optional[str] = Field(default=None)


class ExecuteResponse(BaseModel):
    tool_name: str
    request_id: str
    success: bool
    output: Optional[Any] = None
    error: Optional[str] = None
    error_type: Optional[str] = None
    execution_time_ms: Optional[int] = None
    overflow: bool = False
    metadata: Dict[str, Any] = Field(default_factory=dict)


class SkillMetadataResponse(BaseModel):
    name: str
    version: str
    description: str
    category: str
    execution_mode: str
    requires_sandbox: bool
    requires_llm: bool
    risk_level: str
    side_effects: bool
    parameters: Dict[str, Any]


def register_cribug_tools():
    """Register Cribug built-in skills"""
    from ..tools.builtin.echo_skill import EchoTool
    from ..tools.builtin.safe_math_skill import SafeMathTool
    from ..tools.builtin.llm_summary_skill import LLMSummaryTool

    registry = get_registry()
    for tool_class in [EchoTool, SafeMathTool, LLMSummaryTool]:
        try:
            registry.register(tool_class)
        except ValueError:
            pass


register_cribug_tools()


@router.get("", response_model=List[str])
async def list_skills() -> List[str]:
    """List all registered skill names"""
    registry = get_registry()
    return sorted(registry.list_tools())


@router.get("/audit")
async def get_audit_stats() -> Dict[str, Any]:
    """Get skill execution statistics"""
    return {"total_calls": 0, "by_skill": {}, "error_rate": 0.0}


@router.get("/{skill_name}", response_model=SkillMetadataResponse)
async def get_skill_metadata(skill_name: str) -> SkillMetadataResponse:
    """Get metadata and parameters for a specific skill"""
    registry = get_registry()
    tool = registry.get_tool(skill_name)

    if not tool:
        raise HTTPException(status_code=404, detail=f"Skill '{skill_name}' not found")

    metadata = tool.metadata
    schema = tool.get_schema()

    return SkillMetadataResponse(
        name=metadata.name,
        version=metadata.version,
        description=metadata.description,
        category=metadata.category,
        execution_mode=getattr(metadata, "execution_mode", "python_inline"),
        requires_sandbox=getattr(metadata, "requires_sandbox", False),
        requires_llm=getattr(metadata, "requires_llm", False),
        risk_level=getattr(metadata, "risk_level", "low"),
        side_effects=getattr(metadata, "side_effects", False),
        parameters=schema.get("parameters", {}),
    )


@router.post("/{skill_name}/execute", response_model=ExecuteResponse)
async def execute_skill(skill_name: str, request_data: ExecuteRequest) -> ExecuteResponse:
    """Execute a skill with given parameters"""
    registry = get_registry()
    tool = registry.get_tool(skill_name)

    if not tool:
        raise HTTPException(status_code=404, detail=f"Skill '{skill_name}' not found")

    try:
        session_ctx = request_data.session_context
        if session_ctx:
            allowed_keys = {"session_id", "agent_id", "user_id", "workflow_id"}
            session_ctx = {k: v for k, v in session_ctx.items() if k in allowed_keys}

        result = await tool.execute(session_context=session_ctx, observer=None, **request_data.parameters)

        return ExecuteResponse(
            tool_name=skill_name,
            request_id=request_data.request_id,
            success=result.success,
            output=result.output if result.success else None,
            error=result.error if not result.success else None,
            execution_time_ms=result.execution_time_ms,
            overflow=(result.metadata or {}).get("overflow", False),
            metadata=result.metadata or {},
        )

    except ValueError as e:
        return ExecuteResponse(
            tool_name=skill_name,
            request_id=request_data.request_id,
            success=False,
            error=str(e),
            error_type="invalid_arguments",
        )
    except Exception as e:
        return ExecuteResponse(
            tool_name=skill_name,
            request_id=request_data.request_id,
            success=False,
            error=str(e),
            error_type="execution_failed",
        )
