import { useMemo, useState, type FormEvent } from "react";
import type {
  MessageQuestionnaire,
  MessageQuestionnaireQuestion,
} from "./messaging/types";

type QuestionnaireProps = {
  questionnaire: MessageQuestionnaire;
  onSubmit: (responses: Record<string, unknown>) => Promise<void> | void;
  disabled?: boolean;
};

function normalizeSelectOptions(options: string[] | undefined): string[] {
  if (!Array.isArray(options)) {
    return [];
  }
  const seen = new Set<string>();
  const out: string[] = [];
  for (const option of options) {
    const trimmed = option.trim();
    if (!trimmed || seen.has(trimmed)) {
      continue;
    }
    seen.add(trimmed);
    out.push(trimmed);
  }
  return out;
}

function normalizeQuestionValue(
  question: MessageQuestionnaireQuestion,
  rawValue: unknown,
): { hasValue: boolean; value?: unknown; error?: string } {
  const options = normalizeSelectOptions(question.options);
  switch (question.type) {
    case "text":
    case "textarea":
    case "date":
    case "select": {
      if (typeof rawValue !== "string") {
        return { hasValue: false };
      }
      const trimmed = rawValue.trim();
      if (!trimmed) {
        return { hasValue: false };
      }
      if (question.type === "select" && options.length > 0 && !options.includes(trimmed)) {
        return { hasValue: false, error: "Select a valid option." };
      }
      return { hasValue: true, value: trimmed };
    }
    case "boolean": {
      if (rawValue === true || rawValue === "true") {
        return { hasValue: true, value: true };
      }
      if (rawValue === false || rawValue === "false") {
        return { hasValue: true, value: false };
      }
      return { hasValue: false };
    }
    case "multiselect": {
      if (!Array.isArray(rawValue)) {
        return { hasValue: false };
      }
      const selected = rawValue
        .filter((entry): entry is string => typeof entry === "string")
        .map((entry) => entry.trim())
        .filter((entry) => entry.length > 0)
        .filter((entry) => options.length === 0 || options.includes(entry));
      const deduped = [...new Set(selected)];
      if (deduped.length === 0) {
        return { hasValue: false };
      }
      return { hasValue: true, value: deduped };
    }
    case "number": {
      if (typeof rawValue === "number" && Number.isFinite(rawValue)) {
        return { hasValue: true, value: rawValue };
      }
      if (typeof rawValue !== "string") {
        return { hasValue: false };
      }
      const trimmed = rawValue.trim();
      if (!trimmed) {
        return { hasValue: false };
      }
      const parsed = Number(trimmed);
      if (!Number.isFinite(parsed)) {
        return { hasValue: false, error: "Enter a valid number." };
      }
      return { hasValue: true, value: parsed };
    }
    default:
      return { hasValue: false };
  }
}

function initialValues(questionnaire: MessageQuestionnaire): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  for (const question of questionnaire.questions) {
    if (question.default === undefined) {
      continue;
    }
    if (question.type === "multiselect" && Array.isArray(question.default)) {
      values[question.id] = question.default;
      continue;
    }
    values[question.id] = question.default;
  }
  return values;
}

export default function Questionnaire({
  questionnaire,
  onSubmit,
  disabled = false,
}: QuestionnaireProps) {
  const [values, setValues] = useState<Record<string, unknown>>(() => initialValues(questionnaire));
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const title = useMemo(
    () => questionnaire.title?.trim() || "Questionnaire",
    [questionnaire.title],
  );

  const setFieldValue = (questionID: string, value: unknown) => {
    setError(null);
    setValues((prev) => ({
      ...prev,
      [questionID]: value,
    }));
  };

  const toggleMultiselectValue = (questionID: string, option: string) => {
    setError(null);
    setValues((prev) => {
      const current = Array.isArray(prev[questionID]) ? prev[questionID] as string[] : [];
      const selected = current.includes(option)
        ? current.filter((entry) => entry !== option)
        : [...current, option];
      return {
        ...prev,
        [questionID]: selected,
      };
    });
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (submitting || disabled) {
      return;
    }

    const payload: Record<string, unknown> = {};
    for (const question of questionnaire.questions) {
      const normalized = normalizeQuestionValue(question, values[question.id]);
      if (normalized.error) {
        setError(normalized.error);
        return;
      }
      if (question.required && !normalized.hasValue) {
        setError(`"${question.text}" is required.`);
        return;
      }
      if (normalized.hasValue) {
        payload[question.id] = normalized.value;
      }
    }

    setError(null);
    setSubmitting(true);
    try {
      await onSubmit(payload);
    } catch (submitErr) {
      setError(submitErr instanceof Error ? submitErr.message : "Failed to submit questionnaire.");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form
      onSubmit={handleSubmit}
      className="space-y-3 rounded-xl border border-[var(--border)]/80 bg-[var(--surface)] p-3"
    >
      <div>
        <p className="text-sm font-semibold text-[var(--text)]">{title}</p>
        <p className="text-xs text-[var(--text-muted)]">
          Asked by {questionnaire.author}
        </p>
      </div>

      {questionnaire.questions.map((question) => {
        const questionID = `${questionnaire.id}-${question.id}`;
        const options = normalizeSelectOptions(question.options);
        const currentValue = values[question.id];

        if (question.type === "textarea") {
          return (
            <label key={question.id} htmlFor={questionID} className="block text-sm text-[var(--text)]">
              <span className="mb-1 block">
                {question.text}
                {question.required ? " *" : ""}
              </span>
              <textarea
                id={questionID}
                aria-label={question.text}
                value={typeof currentValue === "string" ? currentValue : ""}
                onChange={(event) => setFieldValue(question.id, event.target.value)}
                placeholder={question.placeholder || ""}
                disabled={disabled || submitting}
                rows={3}
                className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
              />
            </label>
          );
        }

        if (question.type === "boolean") {
          const value = currentValue === true || currentValue === "true"
            ? "true"
            : currentValue === false || currentValue === "false"
              ? "false"
              : "";
          return (
            <label key={question.id} htmlFor={questionID} className="block text-sm text-[var(--text)]">
              <span className="mb-1 block">
                {question.text}
                {question.required ? " *" : ""}
              </span>
              <select
                id={questionID}
                aria-label={question.text}
                value={value}
                onChange={(event) => setFieldValue(question.id, event.target.value)}
                disabled={disabled || submitting}
                className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
              >
                <option value="">Select...</option>
                <option value="true">Yes</option>
                <option value="false">No</option>
              </select>
            </label>
          );
        }

        if (question.type === "multiselect") {
          const selected = Array.isArray(currentValue) ? currentValue as string[] : [];
          return (
            <fieldset key={question.id} className="space-y-1">
              <legend className="text-sm text-[var(--text)]">
                {question.text}
                {question.required ? " *" : ""}
              </legend>
              <div className="space-y-1">
                {options.map((option) => (
                  <label key={option} className="flex items-center gap-2 text-sm text-[var(--text)]">
                    <input
                      type="checkbox"
                      aria-label={option}
                      checked={selected.includes(option)}
                      onChange={() => toggleMultiselectValue(question.id, option)}
                      disabled={disabled || submitting}
                    />
                    <span>{option}</span>
                  </label>
                ))}
              </div>
            </fieldset>
          );
        }

        if (question.type === "select") {
          return (
            <label key={question.id} htmlFor={questionID} className="block text-sm text-[var(--text)]">
              <span className="mb-1 block">
                {question.text}
                {question.required ? " *" : ""}
              </span>
              <select
                id={questionID}
                aria-label={question.text}
                value={typeof currentValue === "string" ? currentValue : ""}
                onChange={(event) => setFieldValue(question.id, event.target.value)}
                disabled={disabled || submitting}
                className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
              >
                <option value="">Select...</option>
                {options.map((option) => (
                  <option key={option} value={option}>{option}</option>
                ))}
              </select>
            </label>
          );
        }

        const inputType = question.type === "number" || question.type === "date"
          ? question.type
          : "text";
        return (
          <label key={question.id} htmlFor={questionID} className="block text-sm text-[var(--text)]">
            <span className="mb-1 block">
              {question.text}
              {question.required ? " *" : ""}
            </span>
            <input
              id={questionID}
              type={inputType}
              aria-label={question.text}
              value={
                typeof currentValue === "number"
                  ? String(currentValue)
                  : typeof currentValue === "string"
                    ? currentValue
                    : ""
              }
              onChange={(event) => setFieldValue(question.id, event.target.value)}
              placeholder={question.placeholder || ""}
              disabled={disabled || submitting}
              className="w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
            />
          </label>
        );
      })}

      {error ? (
        <p className="text-xs text-[var(--red)]">{error}</p>
      ) : null}

      <button
        type="submit"
        disabled={disabled || submitting}
        className="inline-flex rounded-lg bg-[var(--accent)] px-3 py-1.5 text-xs font-semibold text-[#1A1918] transition hover:bg-[var(--accent-hover)] disabled:cursor-not-allowed disabled:opacity-60"
      >
        {submitting ? "Submitting..." : "Submit"}
      </button>
    </form>
  );
}
