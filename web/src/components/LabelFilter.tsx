import type { LabelOption } from "./LabelPill";
import { labelPillStyles } from "./LabelPill";

export type { LabelOption } from "./LabelPill";

type LabelFilterProps = {
  labels: LabelOption[];
  selectedLabelIDs: string[];
  onChange: (labelIDs: string[]) => void;
  className?: string;
};

export default function LabelFilter({
  labels,
  selectedLabelIDs,
  onChange,
  className,
}: LabelFilterProps) {
  const selectedSet = new Set(selectedLabelIDs);

  return (
    <div className={["flex flex-wrap items-center gap-2", className ?? ""].filter(Boolean).join(" ")}>
      {labels.map((label) => {
        const selected = selectedSet.has(label.id);
        return (
          <button
            key={label.id}
            type="button"
            aria-label={`Toggle label ${label.name}`}
            aria-pressed={selected}
            onClick={() => {
              if (selected) {
                onChange(selectedLabelIDs.filter((id) => id !== label.id));
                return;
              }
              onChange([...selectedLabelIDs, label.id]);
            }}
            className={[
              "inline-flex items-center rounded-full border px-2.5 py-1 text-xs font-medium transition",
              selected ? "border-transparent ring-2 ring-offset-1 ring-sky-500/40" : "border-transparent opacity-85 hover:opacity-100",
            ].join(" ")}
            style={labelPillStyles(label.color)}
          >
            {label.name}
          </button>
        );
      })}
      {selectedLabelIDs.length > 0 && (
        <button
          type="button"
          aria-label="Clear label filters"
          onClick={() => onChange([])}
          className="rounded-full px-2.5 py-1 text-xs font-medium text-slate-500 hover:bg-slate-100 hover:text-slate-700 dark:text-slate-300 dark:hover:bg-slate-800 dark:hover:text-slate-100"
        >
          Clear
        </button>
      )}
    </div>
  );
}
