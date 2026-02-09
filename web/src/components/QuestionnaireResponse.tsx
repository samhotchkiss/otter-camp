import type { MessageQuestionnaire } from "./messaging/types";

type QuestionnaireResponseProps = {
  questionnaire: MessageQuestionnaire;
};

function formatAnswer(value: unknown): string {
  if (typeof value === "boolean") {
    return value ? "Yes" : "No";
  }
  if (typeof value === "number") {
    return String(value);
  }
  if (typeof value === "string") {
    const trimmed = value.trim();
    return trimmed || "Not answered";
  }
  if (Array.isArray(value)) {
    const asStrings = value
      .filter((entry): entry is string => typeof entry === "string")
      .map((entry) => entry.trim())
      .filter((entry) => entry.length > 0);
    return asStrings.length > 0 ? asStrings.join(", ") : "Not answered";
  }
  return "Not answered";
}

function formatAnsweredAt(value: string | undefined): string {
  if (!value) {
    return "Unknown time";
  }
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return "Unknown time";
  }
  return parsed.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

export default function QuestionnaireResponse({ questionnaire }: QuestionnaireResponseProps) {
  const title = questionnaire.title?.trim() || "Questionnaire";
  const responses = questionnaire.responses ?? {};

  return (
    <div className="space-y-3 rounded-xl border border-[var(--border)]/80 bg-[var(--surface)] p-3">
      <div>
        <p className="text-sm font-semibold text-[var(--text)]">{title}</p>
        <p className="text-xs text-[var(--text-muted)]">
          Asked by {questionnaire.author}
        </p>
      </div>

      <dl className="space-y-2">
        {questionnaire.questions.map((question) => (
          <div key={question.id} className="space-y-1">
            <dt className="text-xs font-medium text-[var(--text-muted)]">{question.text}</dt>
            <dd className="text-sm text-[var(--text)]">{formatAnswer(responses[question.id])}</dd>
          </div>
        ))}
      </dl>

      <p className="text-xs text-[var(--text-muted)]">
        Answered by {questionnaire.responded_by || "Unknown"} at {formatAnsweredAt(questionnaire.responded_at)}
      </p>
    </div>
  );
}
