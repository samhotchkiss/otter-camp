import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import MessageMarkdown from "./MessageMarkdown";

export type TaskMessageInputProps = {
  value: string;
  onChange: (next: string) => void;
  onSend: () => void;
  disabled?: boolean;
  isSending?: boolean;
  placeholder?: string;
};

type EditorMode = "write" | "preview";

export default function TaskMessageInput({
  value,
  onChange,
  onSend,
  disabled = false,
  isSending = false,
  placeholder = "Write a message (Markdown supported)…",
}: TaskMessageInputProps) {
  const [mode, setMode] = useState<EditorMode>("write");
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const autosize = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    textarea.style.height = "auto";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 160)}px`;
  }, []);

  useEffect(() => {
    autosize();
  }, [autosize, value]);

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      onSend();
    }
  };

  const handleSubmit = (event: FormEvent) => {
    event.preventDefault();
    onSend();
  };

  const canSend = value.trim().length > 0 && !disabled && !isSending;

  return (
    <form onSubmit={handleSubmit} className="border-t border-slate-200 bg-white/70 px-4 py-3 dark:border-slate-800 dark:bg-slate-900/40">
      <div className="mb-2 flex items-center justify-between">
        <div className="flex items-center gap-1 rounded-lg bg-slate-100 p-1 text-xs dark:bg-slate-800/60">
          <button
            type="button"
            onClick={() => setMode("write")}
            className={`rounded-md px-2 py-1 font-medium transition ${
              mode === "write"
                ? "bg-white text-slate-900 shadow-sm dark:bg-slate-900 dark:text-slate-100"
                : "text-slate-600 hover:text-slate-900 dark:text-slate-300 dark:hover:text-slate-100"
            }`}
          >
            Write
          </button>
          <button
            type="button"
            onClick={() => setMode("preview")}
            className={`rounded-md px-2 py-1 font-medium transition ${
              mode === "preview"
                ? "bg-white text-slate-900 shadow-sm dark:bg-slate-900 dark:text-slate-100"
                : "text-slate-600 hover:text-slate-900 dark:text-slate-300 dark:hover:text-slate-100"
            }`}
          >
            Preview
          </button>
        </div>
        <p className="text-[11px] text-slate-500 dark:text-slate-400">
          Markdown • <span className="font-medium">Cmd/Ctrl + Enter</span> to send
        </p>
      </div>

      {mode === "write" ? (
        <textarea
          ref={textareaRef}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={handleKeyDown}
          onInput={autosize}
          placeholder={placeholder}
          rows={1}
          disabled={disabled || isSending}
          className="w-full resize-none rounded-xl border border-slate-300 bg-white px-3 py-2 text-sm text-slate-900 placeholder:text-slate-400 shadow-sm transition focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 disabled:cursor-not-allowed disabled:opacity-60 dark:border-slate-700 dark:bg-slate-950/40 dark:text-slate-100 dark:placeholder:text-slate-500 dark:focus:border-sky-400"
        />
      ) : (
        <div className="min-h-[72px] rounded-xl border border-slate-200 bg-slate-50 px-3 py-2 text-sm text-slate-800 dark:border-slate-800 dark:bg-slate-950/30 dark:text-slate-200">
          {value.trim().length > 0 ? (
            <MessageMarkdown markdown={value} className="space-y-2" />
          ) : (
            <p className="text-slate-500 dark:text-slate-400">
              Nothing to preview yet.
            </p>
          )}
        </div>
      )}

      <div className="mt-3 flex items-center justify-end">
        <button
          type="submit"
          disabled={!canSend}
          className="inline-flex items-center justify-center gap-2 rounded-xl bg-sky-600 px-4 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500 focus:ring-offset-2 focus:ring-offset-white disabled:cursor-not-allowed disabled:opacity-60 dark:focus:ring-offset-slate-900"
        >
          {isSending ? (
            <>
              <span className="h-4 w-4 animate-spin rounded-full border-2 border-white/30 border-t-white" />
              Sending…
            </>
          ) : (
            "Send"
          )}
        </button>
      </div>
    </form>
  );
}

