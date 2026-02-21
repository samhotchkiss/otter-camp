import {
  mapIssueStatusToPipeline,
  PIPELINE_STAGES,
  pipelineStageStates,
} from "./pipelineStages";

type PipelineMiniProgressProps = {
  status?: string | null;
  className?: string;
};

function miniStageClass(stageState: "completed" | "current" | "future"): string {
  switch (stageState) {
    case "completed":
      return "bg-emerald-400";
    case "current":
      return "bg-[color:var(--accent)]";
    case "future":
    default:
      return "bg-[var(--border)]";
  }
}

export default function PipelineMiniProgress({ status, className = "" }: PipelineMiniProgressProps) {
  const resolved = mapIssueStatusToPipeline(status);
  const states = pipelineStageStates(resolved.currentStage);

  return (
    <div
      className={`inline-flex items-center gap-1 ${className}`.trim()}
      data-testid="pipeline-mini-progress"
      aria-label="Task progress"
    >
      {PIPELINE_STAGES.map((stage, index) => {
        const stageState = states[stage.key];
        const isBlocked = resolved.blocked && stage.key === resolved.currentStage;
        return (
          <div key={stage.key} className="inline-flex items-center gap-1">
            <span
              className={`relative inline-flex h-2 w-2 rounded-full ${miniStageClass(stageState)} ${
                stageState === "current" ? "ring-2 ring-[color:var(--accent)]/30" : ""
              }`}
              data-testid={`mini-stage-${stage.key}`}
              data-stage-state={stageState}
              data-blocked={isBlocked ? "true" : "false"}
              title={`${stage.label}${isBlocked ? " (blocked)" : ""}`}
            >
              {isBlocked && (
                <span className="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full bg-amber-400" />
              )}
            </span>
            {index < PIPELINE_STAGES.length - 1 && (
              <span className={`h-px w-2 ${stageState === "future" ? "bg-[var(--border)]" : "bg-emerald-400/60"}`} />
            )}
          </div>
        );
      })}
    </div>
  );
}
