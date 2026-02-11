type AgentWelcomeProps = {
  name: string;
  avatar: string;
  roleDescription: string;
  onStartChat: () => void;
  onCreateAnother: () => void;
};

export default function AgentWelcome({
  name,
  avatar,
  roleDescription,
  onStartChat,
  onCreateAnother,
}: AgentWelcomeProps) {
  return (
    <section className="rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 text-center">
      <div className="mb-4 flex justify-center">
        <div className="h-20 w-20 overflow-hidden rounded-full border border-[var(--border)] bg-[var(--surface-alt)]">
          {avatar ? (
            <img src={avatar} alt="" className="h-full w-full object-cover" />
          ) : (
            <div className="flex h-full w-full items-center justify-center text-2xl text-[var(--text-muted)]">O</div>
          )}
        </div>
      </div>

      <h2 className="text-2xl font-semibold text-[var(--text)]">{name} is ready to go</h2>
      <p className="mt-2 text-sm text-[var(--text-muted)]">{roleDescription}</p>
      <p className="mt-3 text-sm text-[var(--text-muted)]">Send them their first message to kick off onboarding.</p>

      <div className="mt-6 flex flex-wrap justify-center gap-2">
        <button
          type="button"
          onClick={onStartChat}
          className="rounded border border-[#C9A86C] bg-[#C9A86C]/20 px-3 py-2 text-sm font-medium text-[#C9A86C]"
        >
          Send First Message
        </button>
        <button
          type="button"
          onClick={onCreateAnother}
          className="rounded border border-[var(--border)] px-3 py-2 text-sm text-[var(--text-muted)]"
        >
          Create Another Agent
        </button>
      </div>
    </section>
  );
}
