import { useState, useEffect, useRef, type ChangeEvent, type FormEvent } from "react";

type NewTaskModalProps = {
  isOpen: boolean;
  onClose: () => void;
  onTaskCreated?: (task: { title: string; description?: string; priority?: string }) => void;
};

const PRIORITY_OPTIONS = [
  { value: "", label: "No priority", color: "bg-otter-surface-alt dark:bg-otter-dark-surface-alt" },
  { value: "low", label: "Low", color: "bg-otter-surface-alt dark:bg-otter-dark-surface-alt" },
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
    }
  }, [isOpen]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!title.trim()) return;

    setIsSubmitting(true);
    
    try {
      // TODO: Replace with actual API call
      const task = {
        title: title.trim(),
        description: description.trim() || undefined,
        priority: priority || undefined,
      };
      
      // Simulate API call
      await new Promise((resolve) => setTimeout(resolve, 300));
      
      onTaskCreated?.(task);
      onClose();
    } catch (error) {
      console.error("Failed to create task:", error);
    } finally {
      setIsSubmitting(false);
    }
  };

  if (!isOpen) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-otter-dark-bg/70 px-4 py-6 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-label="Create new task"
      onClick={onClose}
    >
      <div
        className="w-full max-w-lg overflow-hidden rounded-2xl border border-otter-border bg-white shadow-2xl dark:border-otter-dark-border dark:bg-otter-dark-bg"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-otter-border px-6 py-4 dark:border-otter-dark-border">
          <div className="flex items-center gap-3">
            <span className="text-xl">📝</span>
            <h2 className="text-lg font-semibold text-otter-text dark:text-white">New Task</h2>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-2 text-otter-muted transition hover:bg-otter-surface-alt hover:text-otter-muted dark:hover:bg-otter-dark-surface-alt dark:hover:text-otter-dark-muted"
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
              <label htmlFor="task-title" className="block text-sm font-medium text-otter-text dark:text-otter-dark-muted">
                Title <span className="text-red-500">*</span>
              </label>
              <input
                ref={inputRef}
                id="task-title"
                type="text"
                value={title}
                onChange={(e: ChangeEvent<HTMLInputElement>) => setTitle(e.target.value)}
                placeholder="What needs to be done?"
                className="mt-1 w-full rounded-lg border border-otter-border bg-white px-4 py-2 text-otter-text placeholder-slate-400 focus:border-otter-dark-accent focus:outline-none focus:ring-1 focus:ring-otter-dark-accent dark:border-otter-dark-border dark:bg-otter-dark-surface dark:text-white dark:placeholder-slate-500"
                required
              />
            </div>

            {/* Description */}
            <div>
              <label htmlFor="task-description" className="block text-sm font-medium text-otter-text dark:text-otter-dark-muted">
                Description
              </label>
              <textarea
                id="task-description"
                value={description}
                onChange={(e: ChangeEvent<HTMLTextAreaElement>) => setDescription(e.target.value)}
                placeholder="Add more details..."
                rows={3}
                className="mt-1 w-full rounded-lg border border-otter-border bg-white px-4 py-2 text-otter-text placeholder-slate-400 focus:border-otter-dark-accent focus:outline-none focus:ring-1 focus:ring-otter-dark-accent dark:border-otter-dark-border dark:bg-otter-dark-surface dark:text-white dark:placeholder-slate-500"
              />
            </div>

            {/* Priority */}
            <div>
              <label htmlFor="task-priority" className="block text-sm font-medium text-otter-text dark:text-otter-dark-muted">
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
                        ? "ring-2 ring-otter-dark-accent ring-offset-2 dark:ring-offset-slate-900"
                        : ""
                    } ${opt.color} ${
                      opt.value === "low" || opt.value === ""
                        ? "text-otter-text dark:text-otter-dark-muted"
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

          {/* Footer */}
          <div className="mt-6 flex items-center justify-end gap-3">
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg px-4 py-2 text-sm font-medium text-otter-muted transition hover:bg-otter-surface-alt dark:text-otter-dark-muted dark:hover:bg-otter-dark-surface-alt"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!title.trim() || isSubmitting}
              className="rounded-lg bg-sky-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-sky-500 focus:outline-none focus:ring-2 focus:ring-otter-dark-accent focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-slate-900"
            >
              {isSubmitting ? "Creating..." : "Create Task"}
            </button>
          </div>

          {/* Keyboard hint */}
          <p className="mt-4 text-center text-xs text-otter-muted dark:text-otter-dark-muted">
            Press <kbd className="rounded bg-otter-surface-alt px-1.5 py-0.5 text-xs dark:bg-otter-dark-surface">Esc</kbd> to cancel
          </p>
        </form>
      </div>
    </div>
  );
}
