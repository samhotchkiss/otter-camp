import { FormEvent, useMemo, useState } from "react";
import { apiFetch } from "../../lib/api";

type AddAgentModalProps = {
  isOpen: boolean;
  onClose: () => void;
  onCreated: () => void;
};

const MODEL_OPTIONS = [
  "gpt-5.2-codex",
  "claude-opus-4-6",
  "gpt-4.1",
];

export default function AddAgentModal({ isOpen, onClose, onCreated }: AddAgentModalProps) {
  const [slot, setSlot] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [model, setModel] = useState(MODEL_OPTIONS[0]);
  const [heartbeatEvery, setHeartbeatEvery] = useState("");
  const [channel, setChannel] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const canSubmit = useMemo(
    () => slot.trim().length > 0 && displayName.trim().length > 0 && model.trim().length > 0 && !isSubmitting,
    [slot, displayName, model, isSubmitting],
  );

  const reset = () => {
    setSlot("");
    setDisplayName("");
    setModel(MODEL_OPTIONS[0]);
    setHeartbeatEvery("");
    setChannel("");
    setIsSubmitting(false);
    setError(null);
  };

  const handleClose = () => {
    reset();
    onClose();
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!canSubmit) {
      return;
    }

    setIsSubmitting(true);
    setError(null);
    try {
      await apiFetch<{ ok?: boolean }>("/api/admin/agents", {
        method: "POST",
        body: JSON.stringify({
          slot: slot.trim(),
          display_name: displayName.trim(),
          model: model.trim(),
          heartbeat_every: heartbeatEvery.trim() || undefined,
          channel: channel.trim() || undefined,
        }),
      });

      onCreated();
      handleClose();
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : "Failed to create agent");
    } finally {
      setIsSubmitting(false);
    }
  };

  if (!isOpen) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Add Agent"
        className="w-full max-w-xl rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-xl"
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-xl font-semibold text-[var(--text)]">Add Agent</h2>
          <button
            type="button"
            onClick={handleClose}
            className="rounded-lg border border-[var(--border)] px-3 py-1 text-sm text-[var(--text-muted)] hover:text-[var(--text)]"
          >
            Close
          </button>
        </div>
        <p className="mb-4 text-xs text-[var(--text-muted)]">
          Creates a managed OtterCamp identity + memory scaffold for chameleon routing.
        </p>

        <form className="space-y-4" onSubmit={handleSubmit}>
          <label className="block text-sm text-[var(--text-muted)]" htmlFor="add-agent-slot">
            Slot
            <input
              id="add-agent-slot"
              value={slot}
              onChange={(event) => setSlot(event.target.value)}
              placeholder="research"
              className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
            />
          </label>

          <label className="block text-sm text-[var(--text-muted)]" htmlFor="add-agent-display-name">
            Display Name
            <input
              id="add-agent-display-name"
              value={displayName}
              onChange={(event) => setDisplayName(event.target.value)}
              placeholder="Riley"
              className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
            />
          </label>

          <label className="block text-sm text-[var(--text-muted)]" htmlFor="add-agent-model">
            Model
            <select
              id="add-agent-model"
              value={model}
              onChange={(event) => setModel(event.target.value)}
              className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
            >
              {MODEL_OPTIONS.map((option) => (
                <option key={option} value={option}>
                  {option}
                </option>
              ))}
            </select>
          </label>

          <label className="block text-sm text-[var(--text-muted)]" htmlFor="add-agent-heartbeat">
            Heartbeat (optional)
            <input
              id="add-agent-heartbeat"
              value={heartbeatEvery}
              onChange={(event) => setHeartbeatEvery(event.target.value)}
              placeholder="15m"
              className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
            />
          </label>

          <label className="block text-sm text-[var(--text-muted)]" htmlFor="add-agent-channel">
            Channel (optional)
            <input
              id="add-agent-channel"
              value={channel}
              onChange={(event) => setChannel(event.target.value)}
              placeholder="slack:#engineering"
              className="mt-1 w-full rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
            />
          </label>

          {error && (
            <div className="rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-700">{error}</div>
          )}

          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={handleClose}
              className="rounded-lg border border-[var(--border)] px-3 py-2 text-sm text-[var(--text-muted)] hover:text-[var(--text)]"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!canSubmit}
              className="rounded-lg border border-[#C9A86C] bg-[#C9A86C]/20 px-3 py-2 text-sm font-medium text-[#C9A86C] disabled:cursor-not-allowed disabled:opacity-60"
            >
              {isSubmitting ? "Creating..." : "Create Agent"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
