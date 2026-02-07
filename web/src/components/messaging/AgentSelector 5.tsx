import { memo, useCallback, useId, useMemo, useState } from "react";
import AgentStatusIndicator from "./AgentStatusIndicator";
import type { Agent, AgentStatus } from "./types";
import { getInitials } from "./utils";

const STATUS_TEXT_STYLES: Record<AgentStatus, string> = {
  online: "text-emerald-400",
  busy: "text-amber-400",
  offline: "text-slate-500",
};

function AgentAvatar({ agent }: { agent: Agent }) {
  return (
    <div className="relative">
      {agent.avatarUrl ? (
        <img
          src={agent.avatarUrl}
          alt={agent.name}
          loading="lazy"
          decoding="async"
          className="h-10 w-10 rounded-xl object-cover ring-2 ring-emerald-500/20"
        />
      ) : (
        <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-emerald-500/15 text-sm font-semibold text-emerald-200 ring-2 ring-emerald-500/20">
          {getInitials(agent.name)}
        </div>
      )}
      <AgentStatusIndicator
        status={agent.status}
        size="xs"
        className="absolute bottom-0 right-0 border-2 border-slate-950"
      />
    </div>
  );
}

type AgentListItemProps = {
  agent: Agent;
  selected: boolean;
  onSelect?: (agent: Agent) => void;
};

const AgentListItem = memo(function AgentListItem({
  agent,
  selected,
  onSelect,
}: AgentListItemProps) {
  const handleClick = useCallback(() => {
    onSelect?.(agent);
  }, [agent, onSelect]);

  const className = useMemo(() => {
    const base =
      "flex w-full items-center gap-3 rounded-xl border px-3 py-2 text-left transition focus:outline-none focus:ring-2 focus:ring-emerald-500 focus:ring-offset-2 focus:ring-offset-slate-950";
    if (selected) {
      return `${base} border-emerald-500/50 bg-emerald-500/10`;
    }
    return `${base} border-slate-800 bg-slate-900/40 hover:border-slate-700 hover:bg-slate-900/70`;
  }, [selected]);

  return (
    <button
      type="button"
      onClick={handleClick}
      className={className}
      aria-current={selected ? "true" : undefined}
    >
      <AgentAvatar agent={agent} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-2">
          <span className="truncate font-medium text-slate-200">
            {agent.name}
          </span>
          <span className={`text-xs capitalize ${STATUS_TEXT_STYLES[agent.status]}`}>
            {agent.status}
          </span>
        </div>
        {agent.role ? (
          <p className="mt-0.5 truncate text-xs text-slate-500">{agent.role}</p>
        ) : null}
      </div>
    </button>
  );
});

export type AgentSelectorProps = {
  agents: Agent[];
  selectedAgentId?: string;
  onSelect?: (agent: Agent) => void;
  placeholder?: string;
  emptyText?: string;
  searchQuery?: string;
  onSearchQueryChange?: (value: string) => void;
  className?: string;
};

function AgentSelectorComponent({
  agents,
  selectedAgentId,
  onSelect,
  placeholder = "Search agents...",
  emptyText = "No agents found.",
  searchQuery,
  onSearchQueryChange,
  className = "",
}: AgentSelectorProps) {
  const inputId = useId();
  const [internalQuery, setInternalQuery] = useState("");
  const isControlled = searchQuery !== undefined;
  const query = isControlled ? searchQuery : internalQuery;

  const setQuery = useCallback(
    (value: string) => {
      if (isControlled) {
        onSearchQueryChange?.(value);
      } else {
        setInternalQuery(value);
      }
    },
    [isControlled, onSearchQueryChange],
  );

  const filteredAgents = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return agents;
    return agents.filter((agent) => {
      const haystack = `${agent.name} ${agent.role ?? ""}`.toLowerCase();
      return haystack.includes(q);
    });
  }, [agents, query]);

  return (
    <div className={`flex h-full flex-col overflow-hidden ${className}`}>
      <div className="border-b border-slate-800 px-3 py-3">
        <label className="sr-only" htmlFor={inputId}>
          Search agents
        </label>
        <div className="relative">
          <input
            id={inputId}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={placeholder}
            className="w-full rounded-xl border border-slate-800 bg-slate-900/60 px-9 py-2 text-sm text-slate-200 placeholder:text-slate-600 focus:border-emerald-500 focus:outline-none focus:ring-1 focus:ring-emerald-500"
          />
          <svg
            xmlns="http://www.w3.org/2000/svg"
            viewBox="0 0 20 20"
            fill="currentColor"
            className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-600"
            aria-hidden="true"
          >
            <path
              fillRule="evenodd"
              d="M9 3.5a5.5 5.5 0 1 0 3.148 10.012l3.67 3.67a.75.75 0 1 0 1.06-1.06l-3.67-3.67A5.5 5.5 0 0 0 9 3.5ZM5 9a4 4 0 1 1 8 0 4 4 0 0 1-8 0Z"
              clipRule="evenodd"
            />
          </svg>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-3">
        {filteredAgents.length === 0 ? (
          <div className="rounded-xl border border-slate-800 bg-slate-900/40 p-4 text-sm text-slate-500">
            {emptyText}
          </div>
        ) : (
          <div className="space-y-2">
            {filteredAgents.map((agent) => (
              <AgentListItem
                key={agent.id}
                agent={agent}
                selected={agent.id === selectedAgentId}
                onSelect={onSelect}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

const AgentSelector = memo(AgentSelectorComponent);

export default AgentSelector;
