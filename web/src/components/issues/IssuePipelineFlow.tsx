import PipelineStageNode from "./PipelineStageNode";
import {
  formatTimeInStage,
  mapIssueStatusToPipeline,
  PIPELINE_STAGES,
  pipelineStageIndex,
  pipelineStageStates,
  type PipelineStageKey,
} from "./pipelineStages";

type IssuePipelineFlowProps = {
  status?: string | null;
  assigneeName?: string | null;
  stageUpdatedAt?: string | null;
  onStageSelect?: (stage: PipelineStageKey) => void;
  disabled?: boolean;
};

function connectorClass(completed: boolean): string {
  if (completed) {
    return "bg-emerald-400/60";
  }
  return "bg-transparent border-t border-dashed border-[var(--border)]";
}

export default function IssuePipelineFlow({
  status,
  assigneeName,
  stageUpdatedAt,
  onStageSelect,
  disabled = false,
}: IssuePipelineFlowProps) {
  const resolved = mapIssueStatusToPipeline(status);
  const states = pipelineStageStates(resolved.currentStage);
  const currentIndex = pipelineStageIndex(resolved.currentStage);
  const progressPercent = PIPELINE_STAGES.length <= 1
    ? 0
    : (currentIndex / (PIPELINE_STAGES.length - 1)) * 100;
  const timeInStageLabel = formatTimeInStage(stageUpdatedAt);

  return (
    <section className="rounded-xl border border-[var(--border)] bg-[var(--surface-alt)] p-3" data-testid="issue-pipeline-flow">
      <div className="relative mb-3 hidden md:block">
        <div className="h-px w-full bg-[var(--border)]" />
        <div
          className="absolute left-0 top-0 h-px bg-[color:var(--accent)] transition-all duration-300"
          style={{ width: `${progressPercent}%` }}
          aria-hidden="true"
        />
        <span
          className="absolute top-0 h-2 w-2 -translate-y-1/2 rounded-full bg-[color:var(--accent)] shadow-[0_0_8px_var(--accent)] transition-all duration-300"
          style={{ left: `calc(${progressPercent}% - 4px)` }}
          aria-hidden="true"
        />
      </div>

      <ol className="flex flex-col gap-2 md:flex-row md:items-stretch md:gap-3">
        {PIPELINE_STAGES.map((stage, index) => {
          const stageState = states[stage.key];
          const connectorIsCompleted = index <= currentIndex;
          const stageIsBlocked = resolved.blocked && stage.key === resolved.currentStage;
          const stageAssigneeName = stageState === "current" ? assigneeName : null;
          const stageTimeLabel = stageState === "current" ? timeInStageLabel : null;
          return (
            <li key={stage.key} className="flex min-w-0 flex-1 items-center gap-2">
              <div className="min-w-0 flex-1">
                <PipelineStageNode
                  stageKey={stage.key}
                  label={stage.label}
                  state={stageState}
                  isBlocked={stageIsBlocked}
                  assigneeName={stageAssigneeName}
                  timeInStageLabel={stageTimeLabel}
                  onSelect={onStageSelect}
                  disabled={disabled}
                />
              </div>
              {index < PIPELINE_STAGES.length - 1 && (
                <>
                  <span
                    className={`hidden h-px w-5 md:block ${connectorClass(connectorIsCompleted)}`}
                    aria-hidden="true"
                  />
                  <span
                    className={`block h-4 w-px md:hidden ${connectorIsCompleted ? "bg-emerald-400/60" : "bg-[var(--border)]"}`}
                    aria-hidden="true"
                  />
                </>
              )}
            </li>
          );
        })}
      </ol>
    </section>
  );
}

