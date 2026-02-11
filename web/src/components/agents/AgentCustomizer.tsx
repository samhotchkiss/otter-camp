import { FormEvent, useMemo, useState } from "react";
import { renderProfileTemplate, type AgentProfile } from "../../data/agent-profiles";

export type AgentCustomizerSubmitPayload = {
  displayName: string;
  profileId: string;
  roleDescription: string;
  model: string;
  avatar: string;
  soul: string;
  identity: string;
};

type AgentCustomizerProps = {
  profile: AgentProfile;
  onSubmit: (payload: AgentCustomizerSubmitPayload) => void;
  onBack: () => void;
  submitting: boolean;
  error?: string | null;
};

export default function AgentCustomizer({ profile, onSubmit, onBack, submitting, error }: AgentCustomizerProps) {
  const [displayName, setDisplayName] = useState(profile.name);
  const [roleDescription, setRoleDescription] = useState(profile.roleDescription);
  const [model, setModel] = useState(profile.defaultModel);
  const [avatar, setAvatar] = useState(profile.defaultAvatar);
  const [soul, setSoul] = useState(renderProfileTemplate(profile.defaultSoul, profile.name));
  const [identity, setIdentity] = useState(renderProfileTemplate(profile.defaultIdentity, profile.name));

  const canSubmit = useMemo(() => {
    return displayName.trim().length > 0 && model.trim().length > 0 && !submitting;
  }, [displayName, model, submitting]);

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!canSubmit) {
      return;
    }

    onSubmit({
      displayName: displayName.trim(),
      profileId: profile.id,
      roleDescription: roleDescription.trim(),
      model: model.trim(),
      avatar: avatar.trim(),
      soul: soul.trim(),
      identity: identity.trim(),
    });
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-4 rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold text-[var(--text)]">Customize {profile.name}</h2>
        <button
          type="button"
          onClick={onBack}
          className="rounded border border-[var(--border)] px-2 py-1 text-xs text-[var(--text-muted)]"
        >
          Back
        </button>
      </div>

      <label className="block text-sm text-[var(--text-muted)]">
        Name
        <input
          value={displayName}
          onChange={(event) => setDisplayName(event.target.value)}
          className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
        />
      </label>

      <label className="block text-sm text-[var(--text-muted)]">
        Role
        <input
          value={roleDescription}
          onChange={(event) => setRoleDescription(event.target.value)}
          className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
        />
      </label>

      <label className="block text-sm text-[var(--text-muted)]">
        Model
        <input
          value={model}
          onChange={(event) => setModel(event.target.value)}
          className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
        />
      </label>

      <label className="block text-sm text-[var(--text-muted)]">
        Avatar
        <input
          value={avatar}
          onChange={(event) => setAvatar(event.target.value)}
          className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
        />
      </label>

      <label className="block text-sm text-[var(--text-muted)]">
        SOUL.md
        <textarea
          value={soul}
          onChange={(event) => setSoul(event.target.value)}
          rows={6}
          className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
        />
      </label>

      <label className="block text-sm text-[var(--text-muted)]">
        IDENTITY.md
        <textarea
          value={identity}
          onChange={(event) => setIdentity(event.target.value)}
          rows={6}
          className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--surface-alt)] px-3 py-2 text-[var(--text)]"
        />
      </label>

      {error && <div className="rounded border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-700">{error}</div>}

      <div className="flex justify-end">
        <button
          type="submit"
          disabled={!canSubmit}
          className="rounded border border-[#C9A86C] bg-[#C9A86C]/20 px-3 py-2 text-sm font-medium text-[#C9A86C] disabled:opacity-50"
        >
          {submitting ? "Creating..." : "Create Agent"}
        </button>
      </div>
    </form>
  );
}
