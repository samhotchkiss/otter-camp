import type { CSSProperties } from "react";

const FALLBACK_LABEL_COLOR = "#6b7280";

export type LabelOption = {
  id: string;
  name: string;
  color: string;
};

function hexToRgb(hex: string): [number, number, number] | null {
  const value = hex.trim().replace(/^#/, "");
  if (/^[0-9a-fA-F]{3}$/.test(value)) {
    const expanded = value
      .split("")
      .map((part) => part + part)
      .join("");
    const int = Number.parseInt(expanded, 16);
    return [(int >> 16) & 255, (int >> 8) & 255, int & 255];
  }
  if (/^[0-9a-fA-F]{6}$/.test(value)) {
    const int = Number.parseInt(value, 16);
    return [(int >> 16) & 255, (int >> 8) & 255, int & 255];
  }
  return null;
}

function normalizeLabelColor(color: string): string {
  return hexToRgb(color) ? color : FALLBACK_LABEL_COLOR;
}

export function labelPillStyles(color: string): CSSProperties {
  const normalizedColor = normalizeLabelColor(color);
  const rgb = hexToRgb(normalizedColor);
  if (!rgb) {
    return {
      color: FALLBACK_LABEL_COLOR,
      backgroundColor: "rgba(107, 114, 128, 0.15)",
    };
  }
  return {
    color: `rgb(${rgb[0]}, ${rgb[1]}, ${rgb[2]})`,
    backgroundColor: `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, 0.15)`,
  };
}

type LabelPillProps = {
  label: LabelOption;
  editable?: boolean;
  onRemove?: (label: LabelOption) => void;
  onClick?: (label: LabelOption) => void;
  className?: string;
};

export default function LabelPill({
  label,
  editable = false,
  onRemove,
  onClick,
  className,
}: LabelPillProps) {
  const isClickable = typeof onClick === "function";
  const baseClassName = [
    "inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium transition",
    isClickable ? "cursor-pointer hover:opacity-85" : "",
    className ?? "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <span
      data-testid={`label-pill-${label.id}`}
      role={isClickable ? "button" : undefined}
      tabIndex={isClickable ? 0 : undefined}
      onClick={() => onClick?.(label)}
      onKeyDown={(event) => {
        if (!isClickable) {
          return;
        }
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onClick?.(label);
        }
      }}
      className={baseClassName}
      style={labelPillStyles(label.color)}
    >
      <span>{label.name}</span>
      {editable && onRemove && (
        <button
          type="button"
          className="rounded-full p-0.5 hover:bg-black/10"
          aria-label={`Remove label ${label.name}`}
          onClick={(event) => {
            event.stopPropagation();
            onRemove(label);
          }}
        >
          <span aria-hidden="true">&times;</span>
        </button>
      )}
    </span>
  );
}
