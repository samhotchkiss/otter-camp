import { useMemo, useState } from "react";
import AgentCustomizer, { type AgentCustomizerSubmitPayload } from "../components/agents/AgentCustomizer";
import AgentProfileCard from "../components/agents/AgentProfileCard";
import AgentWelcome from "../components/agents/AgentWelcome";
import {
  AGENT_PROFILES,
  ROLE_CATEGORIES,
  START_FROM_SCRATCH_PROFILE,
  filterAgentProfiles,
  type AgentProfile,
  type AgentRoleCategory,
} from "../data/agent-profiles";
import { apiFetch } from "../lib/api";

type FlowStep = "browse" | "customize" | "welcome";

type CreatedAgentState = {
  name: string;
  avatar: string;
  roleDescription: string;
};

export default function AgentsNewPage() {
  const [step, setStep] = useState<FlowStep>("browse");
  const [searchQuery, setSearchQuery] = useState("");
  const [roleCategory, setRoleCategory] = useState<AgentRoleCategory | "all">("all");
  const [selectedProfile, setSelectedProfile] = useState<AgentProfile | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [createdAgent, setCreatedAgent] = useState<CreatedAgentState | null>(null);

  const visibleProfiles = useMemo(() => {
    return filterAgentProfiles(AGENT_PROFILES, { roleCategory, query: searchQuery });
  }, [roleCategory, searchQuery]);

  const browseProfiles = useMemo(() => {
    return [...visibleProfiles, START_FROM_SCRATCH_PROFILE];
  }, [visibleProfiles]);

  const handleSelectProfile = (profileID: string) => {
    const profile = browseProfiles.find((candidate) => candidate.id === profileID);
    if (!profile) {
      return;
    }
    setSelectedProfile(profile);
    setError(null);
    setStep("customize");
  };

  const handleSubmit = async (payload: AgentCustomizerSubmitPayload) => {
    setSubmitting(true);
    setError(null);
    try {
      await apiFetch<{ ok?: boolean }>("/api/admin/agents", {
        method: "POST",
        body: JSON.stringify({
          displayName: payload.displayName,
          profileId: payload.profileId === START_FROM_SCRATCH_PROFILE.id ? undefined : payload.profileId,
          soul: payload.soul,
          identity: payload.identity,
          model: payload.model,
          avatar: payload.avatar || undefined,
        }),
      });

      setCreatedAgent({
        name: payload.displayName,
        avatar: payload.avatar,
        roleDescription: payload.roleDescription,
      });
      setStep("welcome");
    } catch (submitErr) {
      setError(submitErr instanceof Error ? submitErr.message : "Failed to create agent");
    } finally {
      setSubmitting(false);
    }
  };

  if (step === "welcome" && createdAgent) {
    return (
      <div className="mx-auto w-full max-w-4xl py-8">
        <AgentWelcome
          name={createdAgent.name}
          avatar={createdAgent.avatar}
          roleDescription={createdAgent.roleDescription}
          onStartChat={() => {
            window.location.assign("/agents");
          }}
          onCreateAnother={() => {
            setCreatedAgent(null);
            setSelectedProfile(null);
            setSearchQuery("");
            setRoleCategory("all");
            setError(null);
            setStep("browse");
          }}
        />
      </div>
    );
  }

  if (step === "customize" && selectedProfile) {
    return (
      <div className="mx-auto w-full max-w-4xl py-8">
        <AgentCustomizer
          profile={selectedProfile}
          onSubmit={handleSubmit}
          onBack={() => {
            setError(null);
            setStep("browse");
          }}
          submitting={submitting}
          error={error}
        />
      </div>
    );
  }

  return (
    <section className="mx-auto w-full max-w-6xl py-8">
      <header className="mb-6">
        <h1 className="text-3xl font-semibold text-[var(--text)]">Hire an Agent</h1>
        <p className="mt-2 text-sm text-[var(--text-muted)]">
          Browse starter profiles, pick a teammate, then customize their voice before launch.
        </p>
      </header>

      <div className="mb-6 grid gap-3 md:grid-cols-[2fr,1fr]">
        <label className="block text-sm text-[var(--text-muted)]">
          Search Profiles
          <input
            value={searchQuery}
            onChange={(event) => setSearchQuery(event.target.value)}
            placeholder="Search by role, vibe, or keyword"
            className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-[var(--text)]"
          />
        </label>

        <label className="block text-sm text-[var(--text-muted)]">
          Role Category
          <select
            value={roleCategory}
            onChange={(event) => setRoleCategory(event.target.value as AgentRoleCategory | "all")}
            className="mt-1 w-full rounded border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-[var(--text)]"
          >
            <option value="all">All Categories</option>
            {ROLE_CATEGORIES.map((category) => (
              <option key={category} value={category}>
                {category}
              </option>
            ))}
          </select>
        </label>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {browseProfiles.map((profile) => (
          <AgentProfileCard
            key={profile.id}
            profile={profile}
            isSelected={false}
            onSelect={handleSelectProfile}
          />
        ))}
      </div>
    </section>
  );
}
