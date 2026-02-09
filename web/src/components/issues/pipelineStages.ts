export type PipelineStageKey = "queued" | "planning" | "in_progress" | "review" | "done";
export type PipelineStageState = "completed" | "current" | "future";

export type PipelineStageDefinition = {
  key: PipelineStageKey;
  label: string;
};

export type PipelineStatusResolution = {
  currentStage: PipelineStageKey;
  blocked: boolean;
  normalizedStatus: string;
};

export const PIPELINE_STAGES: PipelineStageDefinition[] = [
  { key: "queued", label: "Queued" },
  { key: "planning", label: "Planning" },
  { key: "in_progress", label: "Active" },
  { key: "review", label: "Review" },
  { key: "done", label: "Done" },
];

function normalizeStatus(rawStatus: string | null | undefined): string {
  return typeof rawStatus === "string" ? rawStatus.trim().toLowerCase() : "";
}

export function mapIssueStatusToPipeline(rawStatus: string | null | undefined): PipelineStatusResolution {
  const normalizedStatus = normalizeStatus(rawStatus);
  switch (normalizedStatus) {
    case "planning":
      return { currentStage: "planning", blocked: false, normalizedStatus };
    case "in_progress":
      return { currentStage: "in_progress", blocked: false, normalizedStatus };
    case "review":
      return { currentStage: "review", blocked: false, normalizedStatus };
    case "done":
    case "cancelled":
    case "closed":
      return { currentStage: "done", blocked: false, normalizedStatus };
    case "blocked":
    case "flagged":
      return { currentStage: "in_progress", blocked: true, normalizedStatus };
    case "queued":
    case "ready":
    case "ready_for_work":
    case "dispatched":
    default:
      return { currentStage: "queued", blocked: false, normalizedStatus };
  }
}

export function pipelineStageIndex(stageKey: PipelineStageKey): number {
  return PIPELINE_STAGES.findIndex((stage) => stage.key === stageKey);
}

export function pipelineStageStates(currentStage: PipelineStageKey): Record<PipelineStageKey, PipelineStageState> {
  const currentIndex = pipelineStageIndex(currentStage);
  return PIPELINE_STAGES.reduce<Record<PipelineStageKey, PipelineStageState>>((accumulator, stage, index) => {
    if (index < currentIndex) {
      accumulator[stage.key] = "completed";
      return accumulator;
    }
    if (index === currentIndex) {
      accumulator[stage.key] = "current";
      return accumulator;
    }
    accumulator[stage.key] = "future";
    return accumulator;
  }, {
    queued: "future",
    planning: "future",
    in_progress: "future",
    review: "future",
    done: "future",
  });
}

export function formatTimeInStage(stageUpdatedAt: string | null | undefined): string | null {
  if (!stageUpdatedAt) {
    return null;
  }
  const parsed = new Date(stageUpdatedAt);
  if (Number.isNaN(parsed.getTime())) {
    return null;
  }
  const elapsedMs = Math.max(0, Date.now() - parsed.getTime());
  const elapsedMinutes = Math.floor(elapsedMs / 60000);
  if (elapsedMinutes < 1) {
    return "Just entered";
  }
  if (elapsedMinutes < 60) {
    return `${elapsedMinutes}m in stage`;
  }
  const elapsedHours = Math.floor(elapsedMinutes / 60);
  if (elapsedHours < 24) {
    return `${elapsedHours}h in stage`;
  }
  const elapsedDays = Math.floor(elapsedHours / 24);
  return `${elapsedDays}d in stage`;
}

