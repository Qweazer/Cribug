# Phase 6F: Research-Synthesis v1 — Design Spec

## 版本说明

在 6E (RAG pipeline) 基础上，实现多子问题分解、并行 RAG retrieval、evidence Workspace 写入、LLM synthesis 的研究综合能力。

## 1. Architecture

```
ResearchSynthesisWorkflow (deterministic)
  ├─ DecomposeQueryActivity          → LLM: break query into subqueries
  ├─ RetrieveEvidenceActivity ×N    → parallel RAG retrieval per subquery
  │   (reuses 6E-3 EmbedAndSearchChunks + FetchChunkContent + PackContext)
  ├─ WorkspaceAppend ×N             → write evidence to Workspace
  └─ SynthesizeResultActivity       → LLM: synthesize answer from evidence
```

## 2. Data Structures

```go
type ResearchQuery struct {
    ID, Query, AgentID, WorkflowID, Status string
    Subqueries []ResearchSubquery
}

type ResearchSubquery struct {
    ID, ParentID, Text, Status, EvidenceRef string
}

type ResearchEvidence struct {
    SubqueryID string; ChunkIDs []string; Summary string; Score float64
}

type ResearchSynthesisResult struct {
    QueryID, Answer string; Evidence []EvidenceRef; SubqueryAnswers []SubqueryAnswer; TokenCount int
}
```

## 3. Activities (new)

- DecomposeQueryActivity: calls LLM to decompose, returns []ResearchSubquery
- RetrieveEvidenceActivity: reuses RAG 6E-3 (embed→search→fetch→pack) for one subquery
- SynthesizeResultActivity: calls LLM to synthesize answer from all evidence

## 4. Workflow

ResearchSynthesisWorkflow:
1. Decompose → subqueries
2. Parallel RetrieveEvidence per subquery (Temporal Futures)
3. Collect results, write to Workspace
4. Synthesize via LLM
5. Return answer + evidence metadata

## 5. Non-Goals
- No Debate, TOT, citation graph, evidence conflict resolution
- No new HookPoint
- Mock LLM by default
- No modifying go/ or python/llm-service/
