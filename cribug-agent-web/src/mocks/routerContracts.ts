import { RouterContract } from "../types";
import fc_dag_pipeline from "./fc_dag_pipeline.json";
import fc_debate from "./fc_debate.json";
import fc_direct_simple from "./fc_direct_simple.json";
import fc_full_payload from "./fc_full_payload.json";
import fc_rag_local from "./fc_rag_local.json";
import fc_react_calculation from "./fc_react_calculation.json";
import fc_reflection from "./fc_reflection.json";
import fc_research_v2 from "./fc_research_v2.json";
import fc_sandbox from "./fc_sandbox.json";
import fc_tot from "./fc_tot.json";

const samples: Record<string, RouterContract> = {
  fc_dag_pipeline: fc_dag_pipeline as RouterContract,
  fc_debate: fc_debate as RouterContract,
  fc_direct_simple: fc_direct_simple as RouterContract,
  fc_full_payload: fc_full_payload as RouterContract,
  fc_rag_local: fc_rag_local as RouterContract,
  fc_react_calculation: fc_react_calculation as RouterContract,
  fc_reflection: fc_reflection as RouterContract,
  fc_research_v2: fc_research_v2 as RouterContract,
  fc_sandbox: fc_sandbox as RouterContract,
  fc_tot: fc_tot as RouterContract,
};

export function loadSampleContract(name: string): RouterContract | null {
  return samples[name] || null;
}

export function listSampleNames(): string[] {
  return Object.keys(samples);
}
