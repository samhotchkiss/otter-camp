import { useState, useEffect, useRef, type ChangeEvent, type FormEvent } from "react";
import api from "../lib/api";

type NewTaskModalProps = {
  isOpen: boolean;
  onClose: () => void;
  onTaskCreated?: (task: { title: string; description?: string; priority?: string }) => void;
};

const PRIORITY_OPTIONS = [
  { value: "", label: "No priority", color: "bg-slate-100 dark:bg-slate-700" },
  { value: "low", label: "Low", color: "bg-slate-100 dark:bg-slate-700" },
  { value: "medium", label: "Medium", color: "bg-amber-100 dark:bg-amber-900/30" },
  { value: "high", label: "High", color: "bg-red-100 dark:bg-red-900/30" },
  { value: "critical", label: "Critical", color: "bg-purple-100 dark:bg-purple-900/30" },
];

export default function NewTaskModal({ isOpen, onClose, onTaskCreated }: NewTaskModalProps) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [priority, setPriority] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus input when modal opens
  useEffect(() => {
    if (isOpen) {
      const timer = setTimeout(() => {
        inputRef.current?.focus();
      }, 50);
      return () => clearTimeout(timer);
    }
  }, [isOpen]);

  // Reset form when closed
  useEffect(() => {
    if (!isOpen) {
      setTitle("");
      setDescription("");
      setPriority("");
      setError(null);
    }
  }, [isOpen]);

  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!title.trim()) return;

    setIsSubmitting(true);
    setError(null);
    
    try {
      const createdTask = await api.createTask({
        title: title.trim(),
        description: description.trim() || undefined,
        priority: priority || undefined,
      });
      
      onTaskCreated?.({
        title: createdTask.title,
        description: description.trim() || undefined,
        priority: createdTask.priority,
      });
      onClose();
    } catch (err) {
      console.error("Failed to create task:", err);
      setError(err instanceof Error ? err.message : "Failed to create task");
    } finally {
      setIsSubmitting(false);
    }
  };

  if (!isOpen) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 px-4 py-6 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-label="Create new task"
      onClick={onClose}
    >
      <div
        className="w-full max-w-lg overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-2xl dark:border-slate-800 dark:bg-slate-900"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-slate-200 px-6 py-4 dark:border-slate-800">
          <div className="flex items-center gap-3">
            <span className="text-xl">📝</span>
            <h2 className="text-lg font-semibold text-slate-900 dark:text-white">New Task</h2>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-2 text-slate-400 transition hover:bg-slate-100 hover:text-slate-600 dark:hover:bg-slate-800 dark:hover:text-slate-300"
            aria-label="Close"
          >
            <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="p-6">
          <div className="space-y-4">
            {/* Title */}
            <div>
              <label htmlFor="task-title" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Title <span className="text-red-500">*</span>
              </label>
              <input
                ref={inputRef}
                id="task-title"
                type="text"
                value={title}
                onChange={(e: ChangeEvent<HTMLInputElement>) => setTitle(e.target.value)}
                placeholder="What needs to be done?"
                className="mt-1 w-full rounded-lg border border-slate-300 bg-white px-4 py-2 text-slate-900 placeholder-slate-400 focus:border-sky-500 focus:outline-none focus:ring-1 focus:ring-sky-500 dark:border-slate-600 dark:bg-slate-800 dark:text-white dark:placeholder-slate-500"
                required
              />
            </div>

            {/* Description */}
            <div>
              <label htmlFor="task-description" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Description
              </label>
              <textarea
                id="task-description"
                value={description}
                onChange={(e: ChangeEvent<HTMLTextAreaElement>) => setDescription(e.target.value)}
                placeholder="Add more details..."
                rows={3}
                className="mt-1 w-full rounded-lg border border-slate-300 bg-white px-4 py-2 text-slate-900 placeholder-slate-400 focus:border-sky-500 focus:outline-none focus:ring-1 focus:ring-sky-500 dark:border-slate-600 dark:bg-slate-800 dark:text-white dark:placeholder-slate-500"
              />
            </div>

            {/* Priority */}
            <div>
              <label htmlFor="task-priority" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                Priority
              </label>
              <div className="mt-2 flex flex-wrap gap-2">
                {PRIORITY_OPTIONS.map((opt) => (
                  <button
                    key={opt.value}
                    type="button"
                    onClick={() => setPriority(opt.value)}
                    className={`rounded-full px-3 py-1 text-sm font-medium transition ${
                      priority === opt.value
                        ? "ring-2 ring-sky-500 ring-offset-2 dark:ring-offset-slate-900"
                        : ""
                    } ${opt.color} ${
                      opt.value === "low" || opt.value === ""
                        ? "text-slate-700 dark:text-slate-300"
                        : opt.value === "medium"
                        ? "text-amber-700 dark:text-amber-300"
                        : opt.value === "high"
                        ? "text-red-700 dark:text-red-300"
                        : "text-purple-700 dark:text-purple-300"
                    }`}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </div>
          </div>

          {/* Error message */}
          {error && (
            <div className="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-600 dark:bg-red-900/20 dark:text-red-400">
              {error}
            </div>
          )}

          {/* Footer */}
          <div className="mt-6 flex items-center justify-end gap-3">
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg px-4 py-2 text-sm font-medium text-slate-600 transition hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!title.trim() || isSubmitting}
              className="rounded-lg bg-sky-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-slate-900"
            >
              {isSubmitting ? "Creating..." : "Create Task"}
            </button>
          </div>

          {/* Keyboard hint */}
          <p className="mt-4 text-center text-xs text-slate-400 dark:text-slate-500">
            Press <kbd className="rounded bg-slate-100 px-1.5 py-0.5 text-xs dark:bg-slate-800">Esc</kbd> to cancel
          </p>
        </form>
      </div>
    </div>
  );
}
