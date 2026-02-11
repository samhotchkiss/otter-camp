import type { AgentProfile } from "../../data/agent-profiles";

type AgentProfileCardProps = {
  profile: AgentProfile;
  isSelected: boolean;
  onSelect: (profileID: string) => void;
};

export default function AgentProfileCard({ profile, isSelected, onSelect }: AgentProfileCardProps) {
  return (
    <button
      type="button"
      aria-pressed={isSelected}
      onClick={() => onSelect(profile.id)}
      className={`w-full rounded-2xl border p-4 text-left transition ${
        isSelected
          ? "border-[#C9A86C] bg-[#1a1f29]"
          : "border-[var(--border)] bg-[var(--surface)] hover:border-[#C9A86C]/60"
      }`}
    >
      <div className="mb-3 flex items-center gap-3">
        <div className="h-12 w-12 overflow-hidden rounded-full border border-[var(--border)] bg-[var(--surface-alt)]">
          {profile.defaultAvatar ? (
            <img src={profile.defaultAvatar} alt="" className="h-full w-full object-cover" />
          ) : (
            <div className="flex h-full w-full items-center justify-center text-lg text-[var(--text-muted)]">+</div>
          )}
        </div>
        <div>
          <div className="text-base font-semibold text-[var(--text)]">{profile.name}</div>
          <div className="text-xs text-[var(--text-muted)]">{profile.roleDescription}</div>
        </div>
      </div>

      <div className="mb-2 inline-flex rounded-full border border-[var(--border)] px-2 py-0.5 text-xs text-[var(--text-muted)]">
        {profile.roleCategory}
      </div>
      <p className="mb-2 text-sm text-[var(--text)]">{profile.tagline}</p>
      <p className="text-xs leading-relaxed text-[var(--text-muted)]">{profile.personalityPreview}</p>
    </button>
  );
}
