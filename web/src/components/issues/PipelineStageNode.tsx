import type { PipelineStageKey, PipelineStageState } from "./pipelineStages";

type PipelineStageNodeProps = {
  stageKey: PipelineStageKey;
  label: string;
  state: PipelineStageState;
  isBlocked?: boolean;
  assigneeName?: string | null;
  timeInStageLabel?: string | null;
  onSelect?: (stage: PipelineStageKey) => void;
  disabled?: boolean;
};

function stageShellClass(state: PipelineStageState): string {
  switch (state) {
    case "completed":
      return "border-emerald-400/50 bg-emerald-500/10 text-emerald-200";
    case "current":
      return "border-[color:var(--accent)] bg-[var(--surface)] text-[var(--text)] shadow-[0_0_0_1px_var(--accent)]";
    case "future":
    default:
      return "border-[var(--border)] bg-[var(--surface-alt)] text-[var(--text-muted)]";
  }
}

export default function PipelineStageNode({
  stageKey,
  label,
  state,
  isBlocked = false,
  assigneeName,
  timeInStageLabel,
  onSelect,
  disabled = false,
}: PipelineStageNodeProps) {
  const canSelect = typeof onSelect === "function";
  const ownerInitial = assigneeName && assigneeName.trim() !== ""
    ? assigneeName.trim().charAt(0).toUpperCase()
    : null;
  const stageTitle = [
    label,
    state === "current" && assigneeName ? `Owner: ${assigneeName}` : null,
    state === "current" && timeInStageLabel ? timeInStageLabel : null,
    isBlocked ? "Blocked" : null,
  ].filter(Boolean).join(" • ");

  const content = (
    <div
      className={`group relative min-h-[88px] rounded-xl border px-3 py-2 text-left transition duration-300 ${stageShellClass(state)} ${
        canSelect && !disabled ? "hover:-translate-y-0.5 hover:border-[color:var(--accent)]" : ""
      }`}
      data-testid={`pipeline-stage-${stageKey}`}
      data-stage-state={state}
      data-blocked={isBlocked ? "true" : "false"}
      title={stageTitle}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-[11px] font-semibold uppercase tracking-wide">{label}</span>
        {state === "completed" && (
          <span className="text-xs font-bold text-emerald-200" aria-hidden="true">
            ✓
          </span>
        )}
        {state === "current" && (
          <span className="h-2 w-2 rounded-full bg-[color:var(--accent)] animate-pulse" aria-hidden="true" />
        )}
      </div>

      <div className="mt-2 flex items-center gap-2">
        {ownerInitial && state === "current" && (
          <span className="inline-flex h-5 w-5 items-center justify-center rounded-full bg-[color:var(--accent)]/25 text-[10px] font-semibold text-[var(--text)]">
            {ownerInitial}
          </span>
        )}
        {state === "current" && assigneeName && (
          <span className="text-xs text-[var(--text)]">{assigneeName}</span>
        )}
      </div>

      {state === "current" && timeInStageLabel && (
        <p className="mt-2 text-[11px] text-[var(--text-muted)]">{timeInStageLabel}</p>
      )}

      {isBlocked && (
        <span className="mt-2 inline-flex rounded-full border border-amber-400/50 bg-amber-500/15 px-2 py-0.5 text-[10px] font-semibold text-amber-200">
          Blocked
        </span>
      )}
    </div>
  );

  if (!canSelect) {
    return content;
  }

  return (
    <button
      type="button"
      className="w-full bg-transparent p-0 disabled:cursor-not-allowed"
      onClick={() => onSelect(stageKey)}
      disabled={disabled}
      aria-label={`Move issue to ${label}`}
    >
      {content}
    </button>
  );
}

