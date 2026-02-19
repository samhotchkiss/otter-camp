import { useEffect, useMemo, useState } from "react";
import api, {
  type AdminAgentSummary,
  type KnowledgeEntry,
  type MemoryEvent,
  type TaxonomyNode,
} from "../lib/api";

type ConversationExtract = {
  id: string;
  source: string;
  extract: string;
  timestamp: string;
  confidence: number | null;
  eventType: string;
};

type TaxonomyCategory = {
  id: string;
  name: string;
  entries: number;
  detail: string;
  tone: "amber" | "orange" | "lime" | "emerald";
};

type ProjectDoc = {
  id: string;
  title: string;
  author: string;
  lastEdited: string;
  status: "current" | "archived";
  tags: string[];
};

type MemoryStat = {
  label: string;
  value: number;
  tone: "amber" | "orange" | "lime";
};

type StatsSnapshot = {
  vectorEmbeddings: number;
  entitySyntheses: number;
  fileBackedMemories: number;
  contextInjections: number;
};

const ZERO_STATS: StatsSnapshot = {
  vectorEmbeddings: 0,
  entitySyntheses: 0,
  fileBackedMemories: 0,
  contextInjections: 0,
};

function toneClasses(tone: TaxonomyCategory["tone"]): string {
  if (tone === "orange") {
    return "text-orange-400 border-orange-500/20 hover:bg-orange-500/5";
  }
  if (tone === "lime") {
    return "text-lime-400 border-lime-500/20 hover:bg-lime-500/5";
  }
  if (tone === "emerald") {
    return "text-emerald-400 border-emerald-500/20 hover:bg-emerald-500/5";
  }
  return "text-amber-400 border-amber-500/20 hover:bg-amber-500/5";
}

function statToneClasses(tone: MemoryStat["tone"]): string {
  if (tone === "orange") {
    return "border-orange-500/20 bg-orange-500/10 text-orange-400";
  }
  if (tone === "lime") {
    return "border-lime-500/20 bg-lime-500/10 text-lime-400";
  }
  return "border-amber-500/20 bg-amber-500/10 text-amber-400";
}

function toRelativeTimestamp(input: string | undefined): string {
  if (!input) {
    return "Unknown";
  }

  const parsed = new Date(input);
  if (Number.isNaN(parsed.getTime())) {
    return "Unknown";
  }

  const diffMs = Date.now() - parsed.getTime();
  if (diffMs <= 0) {
    return "Just now";
  }

  const minutes = Math.floor(diffMs / 60000);
  if (minutes < 1) {
    return "Just now";
  }
  if (minutes < 60) {
    return `${minutes} min ago`;
  }

  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours}h ago`;
  }

  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function normalizeErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return "Failed to load memory system data";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function readString(payload: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const value = payload[key];
    if (typeof value === "string" && value.trim()) {
      return value.trim();
    }
  }
  return "";
}

function readNumber(payload: Record<string, unknown>, ...keys: string[]): number | null {
  for (const key of keys) {
    const value = payload[key];
    if (typeof value === "number" && Number.isFinite(value)) {
      return value;
    }
  }
  return null;
}

function humanizeEventType(value: string): string {
  return value
    .replace(/[._]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function formatPercent(value: number | null): string {
  if (value === null || Number.isNaN(value)) {
    return "--";
  }
  const normalized = value <= 1 ? value * 100 : value;
  return `${Math.max(0, Math.min(100, Math.round(normalized)))}%`;
}

function formatDocStatus(updatedAt: string | undefined): "current" | "archived" {
  if (!updatedAt) {
    return "archived";
  }
  const parsed = new Date(updatedAt);
  if (Number.isNaN(parsed.getTime())) {
    return "archived";
  }
  const thirtyDaysMs = 30 * 24 * 60 * 60 * 1000;
  return Date.now() - parsed.getTime() <= thirtyDaysMs ? "current" : "archived";
}

function toneForIndex(index: number): TaxonomyCategory["tone"] {
  const tones: TaxonomyCategory["tone"][] = ["amber", "orange", "lime", "emerald"];
  return tones[index % tones.length];
}

export default function KnowledgePage() {
  const [agents, setAgents] = useState<AdminAgentSummary[]>([]);
  const [events, setEvents] = useState<MemoryEvent[]>([]);
  const [knowledgeEntries, setKnowledgeEntries] = useState<KnowledgeEntry[]>([]);
  const [taxonomyNodes, setTaxonomyNodes] = useState<TaxonomyNode[]>([]);
  const [taxonomyCountsByNodeID, setTaxonomyCountsByNodeID] = useState<Record<string, number>>({});
  const [stats, setStats] = useState<StatsSnapshot>(ZERO_STATS);
  const [searchQuery, setSearchQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [loadWarning, setLoadWarning] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let cancelled = false;

    async function loadKnowledge(): Promise<void> {
      setLoading(true);
      setLoadError(null);
      setLoadWarning(null);

      try {
        const [agentsResult, eventsResult, knowledgeResult, taxonomyResult] = await Promise.allSettled([
          api.adminAgents(),
          api.memoryEvents(80),
          api.knowledge(200),
          api.taxonomyNodes(),
        ]);

        if (cancelled) {
          return;
        }

        const failures = [agentsResult, eventsResult, knowledgeResult, taxonomyResult].filter(
          (result) => result.status === "rejected",
        );
        if (failures.length === 4) {
          throw new Error("Unable to load memory system endpoints");
        }
        if (failures.length > 0) {
          setLoadWarning("Some memory panels are partially unavailable right now.");
        }

        const nextAgents = agentsResult.status === "fulfilled"
          ? (Array.isArray(agentsResult.value.agents) ? agentsResult.value.agents : [])
          : [];
        const nextEvents = eventsResult.status === "fulfilled"
          ? (Array.isArray(eventsResult.value.items) ? eventsResult.value.items : [])
          : [];
        const nextKnowledge = knowledgeResult.status === "fulfilled"
          ? (Array.isArray(knowledgeResult.value.items) ? knowledgeResult.value.items : [])
          : [];
        const nextTaxonomyNodes = taxonomyResult.status === "fulfilled"
          ? (Array.isArray(taxonomyResult.value.nodes) ? taxonomyResult.value.nodes : [])
          : [];

        const memoryEntryCounts = await Promise.all(
          nextAgents.map(async (agent) => {
            const workspaceAgentID = (agent.workspace_agent_id ?? "").trim();
            if (!workspaceAgentID) {
              return 0;
            }
            try {
              const payload = await api.memoryEntries(workspaceAgentID, { limit: 200 });
              return Array.isArray(payload.items) ? payload.items.length : 0;
            } catch {
              return 0;
            }
          }),
        );

        const memoryFileCounts = await Promise.all(
          nextAgents.map(async (agent) => {
            const slug = (agent.id ?? "").trim();
            if (!slug) {
              return 0;
            }
            try {
              const payload = await api.adminAgentMemoryFiles(slug);
              return (payload.entries ?? []).filter((entry) => entry.type === "file").length;
            } catch {
              return 0;
            }
          }),
        );

        const taxonomyCountsByIDEntries = await Promise.all(
          nextTaxonomyNodes.slice(0, 12).map(async (node) => {
            try {
              const payload = await api.taxonomyNodeMemories(node.id);
              return [node.id, Array.isArray(payload.memories) ? payload.memories.length : 0] as const;
            } catch {
              return [node.id, 0] as const;
            }
          }),
        );

        if (cancelled) {
          return;
        }

        const taxonomyCountMap: Record<string, number> = {};
        for (const [nodeID, count] of taxonomyCountsByIDEntries) {
          taxonomyCountMap[nodeID] = count;
        }

        const synthEventTypes = new Set([
          "knowledge.shared",
          "knowledge.confirmed",
          "knowledge.contradicted",
        ]);

        const vectorEmbeddings = memoryEntryCounts.reduce((sum, value) => sum + value, 0);
        const entitySyntheses = nextEvents.filter((event) => synthEventTypes.has((event.event_type ?? "").trim())).length;
        const fileBackedMemories = memoryFileCounts.reduce((sum, value) => sum + value, 0);
        const fallbackFileCount = nextKnowledge.length;
        const contextInjections = nextEvents.length;

        setAgents(nextAgents);
        setEvents(nextEvents);
        setKnowledgeEntries(nextKnowledge);
        setTaxonomyNodes(nextTaxonomyNodes);
        setTaxonomyCountsByNodeID(taxonomyCountMap);
        setStats({
          vectorEmbeddings,
          entitySyntheses,
          fileBackedMemories: fileBackedMemories > 0 ? fileBackedMemories : fallbackFileCount,
          contextInjections,
        });
      } catch (error) {
        if (!cancelled) {
          setAgents([]);
          setEvents([]);
          setKnowledgeEntries([]);
          setTaxonomyNodes([]);
          setTaxonomyCountsByNodeID({});
          setStats(ZERO_STATS);
          setLoadError(normalizeErrorMessage(error));
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    }

    void loadKnowledge();
    return () => {
      cancelled = true;
    };
  }, [refreshKey]);

  const agentNameByWorkspaceID = useMemo(() => {
    const out: Record<string, string> = {};
    for (const agent of agents) {
      const workspaceID = (agent.workspace_agent_id ?? "").trim();
      if (!workspaceID) {
        continue;
      }
      const name = (agent.name ?? "").trim() || (agent.id ?? "").trim() || workspaceID;
      out[workspaceID] = name;
    }
    return out;
  }, [agents]);

  const conversationExtracts = useMemo(() => {
    return events.slice(0, 40).map<ConversationExtract>((event) => {
      const payload = isRecord(event.payload) ? event.payload : {};
      const sourceAgentID = readString(payload, "source_agent_id", "agent_id");
      const sourceName = sourceAgentID
        ? (agentNameByWorkspaceID[sourceAgentID] || sourceAgentID)
        : "System";
      const detail = readString(payload, "title", "summary", "reason", "kind")
        || humanizeEventType(event.event_type || "memory.event");
      const confidence = readNumber(payload, "quality_score", "confidence");

      return {
        id: String(event.id ?? `${event.event_type}:${event.created_at}`),
        source: `${sourceName} -> Memory System`,
        extract: detail,
        timestamp: toRelativeTimestamp(event.created_at),
        confidence,
        eventType: humanizeEventType(event.event_type || "memory.event"),
      };
    });
  }, [agentNameByWorkspaceID, events]);

  const taxonomyCategories = useMemo(() => {
    const withCounts = taxonomyNodes.map((node) => {
      const count = taxonomyCountsByNodeID[node.id] ?? 0;
      return {
        id: node.id,
        name: (node.display_name ?? "").trim() || (node.slug ?? "").trim() || "Untitled node",
        entries: count,
        detail: `Depth ${node.depth} • ${count} classified memories`,
      };
    });

    if (withCounts.length > 0) {
      return withCounts
        .sort((a, b) => b.entries - a.entries || a.name.localeCompare(b.name))
        .slice(0, 8)
        .map<TaxonomyCategory>((category, index) => ({
          ...category,
          tone: toneForIndex(index),
        }));
    }

    const fallbackByTag = new Map<string, { count: number; latestAt: string }>();
    for (const entry of knowledgeEntries) {
      const tags = Array.isArray(entry.tags) ? entry.tags : [];
      const updatedAt = (entry.updated_at ?? "").trim();
      for (const tag of tags) {
        const normalized = tag.trim();
        if (!normalized) {
          continue;
        }
        const current = fallbackByTag.get(normalized);
        if (!current) {
          fallbackByTag.set(normalized, { count: 1, latestAt: updatedAt });
          continue;
        }
        current.count += 1;
        if (updatedAt && (!current.latestAt || updatedAt > current.latestAt)) {
          current.latestAt = updatedAt;
        }
      }
    }

    return Array.from(fallbackByTag.entries())
      .sort((a, b) => b[1].count - a[1].count || a[0].localeCompare(b[0]))
      .slice(0, 8)
      .map<TaxonomyCategory>(([tag, meta], index) => ({
        id: tag,
        name: tag,
        entries: meta.count,
        detail: `Updated ${toRelativeTimestamp(meta.latestAt || undefined)}`,
        tone: toneForIndex(index),
      }));
  }, [knowledgeEntries, taxonomyCountsByNodeID, taxonomyNodes]);

  const projectDocs = useMemo(() => {
    return knowledgeEntries
      .slice()
      .sort((a, b) => {
        const aTime = new Date(a.updated_at ?? a.created_at ?? "").getTime();
        const bTime = new Date(b.updated_at ?? b.created_at ?? "").getTime();
        return (Number.isFinite(bTime) ? bTime : 0) - (Number.isFinite(aTime) ? aTime : 0);
      })
      .slice(0, 16)
      .map<ProjectDoc>((entry) => {
        const lastEditedAt = (entry.updated_at ?? entry.created_at ?? "").trim();
        const title = (entry.title ?? "").trim() || "Untitled entry";
        const author = (entry.created_by ?? "").trim() || "unknown";
        const tags = Array.isArray(entry.tags) ? entry.tags.filter((tag) => tag.trim() !== "") : [];
        return {
          id: (entry.id ?? title).trim() || title,
          title,
          author,
          lastEdited: toRelativeTimestamp(lastEditedAt),
          status: formatDocStatus(lastEditedAt),
          tags,
        };
      });
  }, [knowledgeEntries]);

  const memoryStats = useMemo<MemoryStat[]>(() => {
    return [
      { label: "Vector Embeddings", value: stats.vectorEmbeddings, tone: "amber" },
      { label: "Entity Syntheses", value: stats.entitySyntheses, tone: "orange" },
      { label: "File-Backed Memories", value: stats.fileBackedMemories, tone: "lime" },
      { label: "Context Injections", value: stats.contextInjections, tone: "amber" },
    ];
  }, [stats]);

  const normalizedQuery = searchQuery.trim().toLowerCase();
  const filteredExtracts = useMemo(
    () => conversationExtracts.filter((extract) => {
      if (!normalizedQuery) {
        return true;
      }
      return (
        extract.source.toLowerCase().includes(normalizedQuery)
        || extract.extract.toLowerCase().includes(normalizedQuery)
        || extract.eventType.toLowerCase().includes(normalizedQuery)
      );
    }),
    [conversationExtracts, normalizedQuery],
  );
  const filteredCategories = useMemo(
    () => taxonomyCategories.filter((category) => {
      if (!normalizedQuery) {
        return true;
      }
      return (
        category.name.toLowerCase().includes(normalizedQuery)
        || category.detail.toLowerCase().includes(normalizedQuery)
      );
    }),
    [normalizedQuery, taxonomyCategories],
  );
  const filteredDocs = useMemo(
    () => projectDocs.filter((doc) => {
      if (!normalizedQuery) {
        return true;
      }
      return (
        doc.title.toLowerCase().includes(normalizedQuery)
        || doc.author.toLowerCase().includes(normalizedQuery)
        || doc.tags.join(" ").toLowerCase().includes(normalizedQuery)
      );
    }),
    [normalizedQuery, projectDocs],
  );

  return (
    <div data-testid="knowledge-shell" className="space-y-6">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold text-stone-100">
            <span aria-hidden="true" className="text-orange-500">🧠</span>
            Memory System
          </h1>
          <p className="text-sm text-stone-500">Vector retrieval • Entity synthesis • File persistence</p>
        </div>
        <div className="flex items-center gap-2">
          <div className="relative">
            <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-stone-500">⌕</span>
            <input
              type="text"
              value={searchQuery}
              onChange={(event) => setSearchQuery(event.target.value)}
              placeholder="Search memory graph..."
              className="w-64 rounded-lg border border-stone-800 bg-stone-900 py-2 pl-9 pr-4 text-xs text-stone-300 outline-none placeholder:text-stone-600 focus:border-amber-500/50 focus:ring-1 focus:ring-amber-500/40"
            />
          </div>
          <button
            type="button"
            onClick={() => setRefreshKey((value) => value + 1)}
            className="rounded border border-stone-700 bg-stone-900 px-3 py-2 text-[10px] font-semibold uppercase tracking-wide text-stone-300 hover:bg-stone-800"
          >
            Refresh
          </button>
        </div>
      </div>

      {loadError ? (
        <section className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-rose-500/20 bg-rose-500/10 p-4">
          <p className="text-sm text-rose-300">{loadError}</p>
          <button
            type="button"
            onClick={() => setRefreshKey((value) => value + 1)}
            className="rounded border border-rose-400/40 bg-rose-500/10 px-3 py-1.5 text-xs font-medium text-rose-200 hover:bg-rose-500/20"
          >
            Retry
          </button>
        </section>
      ) : null}

      {loadWarning ? (
        <section className="rounded-lg border border-amber-500/20 bg-amber-500/10 p-3">
          <p className="text-xs text-amber-300">{loadWarning}</p>
        </section>
      ) : null}

      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        {memoryStats.map((stat) => (
          <article key={stat.label} className="group relative overflow-hidden rounded-lg border border-stone-800 bg-stone-900 p-4">
            <div className={`absolute inset-0 opacity-20 transition-opacity group-hover:opacity-30 ${statToneClasses(stat.tone)}`} />
            <div className="relative z-10">
              <div className="mb-2 flex items-start justify-between">
                <span className={`rounded-lg border px-2 py-1 text-xs ${statToneClasses(stat.tone)}`}>◉</span>
                <span className="text-lime-500">↗</span>
              </div>
              <p className="text-2xl font-bold tracking-tight text-stone-200">{stat.value.toLocaleString()}</p>
              <p className="mt-1 text-[10px] font-semibold uppercase tracking-wider text-stone-500">{stat.label}</p>
            </div>
          </article>
        ))}
      </div>

      <div className="grid h-[500px] grid-cols-1 gap-6 lg:grid-cols-3">
        <section className="flex flex-col overflow-hidden rounded-lg border border-stone-800 bg-stone-900 lg:col-span-2">
          <header className="flex items-center justify-between border-b border-stone-800 bg-stone-950/30 p-4">
            <h2 className="text-sm font-semibold text-stone-200">Stream: Conversation Extraction</h2>
            <span className="rounded border border-lime-500/20 bg-lime-500/10 px-2 py-0.5 text-[10px] text-lime-400">{loading ? "SYNCING" : "LIVE"}</span>
          </header>
          <div className="flex-1 space-y-2 overflow-y-auto p-2">
            {filteredExtracts.length === 0 ? (
              <p className="p-3 text-sm text-stone-500">
                {loading ? "Loading extracted memory events..." : "No extracted events found for the current filter."}
              </p>
            ) : null}

            {filteredExtracts.map((extract) => (
              <article key={extract.id} className="group rounded border border-stone-800/50 bg-stone-950/50 p-3 transition hover:border-amber-500/30">
                <div className="mb-2 flex items-start justify-between gap-2">
                  <div>
                    <div className="mb-1 flex items-center gap-2">
                      <span className="rounded bg-amber-500/10 px-1.5 text-[10px] text-amber-400">{extract.source}</span>
                      <span className="text-[10px] text-stone-600">{extract.timestamp}</span>
                    </div>
                    <p className="text-sm text-stone-300 transition group-hover:text-stone-100">"{extract.extract}"</p>
                    <p className="mt-1 text-[10px] uppercase tracking-wider text-stone-600">{extract.eventType}</p>
                  </div>
                  <div className="ml-3 text-right">
                    <p className="text-[10px] text-stone-500">CONFIDENCE</p>
                    <p className="text-xs font-bold text-lime-400">{formatPercent(extract.confidence)}</p>
                  </div>
                </div>
              </article>
            ))}
          </div>
        </section>

        <section className="flex flex-col overflow-hidden rounded-lg border border-stone-800 bg-stone-900">
          <header className="border-b border-stone-800 bg-stone-950/30 p-4">
            <h2 className="text-sm font-semibold text-stone-200">Taxonomy</h2>
          </header>
          <div className="flex-1 space-y-2 overflow-y-auto p-2">
            {filteredCategories.length === 0 ? (
              <p className="p-3 text-sm text-stone-500">
                {loading ? "Loading taxonomy..." : "No taxonomy categories found."}
              </p>
            ) : null}

            {filteredCategories.map((category) => (
              <article
                key={category.id}
                className={`flex items-center justify-between rounded border bg-stone-950/50 p-3 transition ${toneClasses(category.tone)}`}
              >
                <div>
                  <h3 className="text-xs font-medium text-stone-300">{category.name}</h3>
                  <p className="text-[10px] text-stone-600">{category.detail}</p>
                </div>
                <p className="text-lg font-bold font-mono">{category.entries}</p>
              </article>
            ))}
          </div>
        </section>
      </div>

      <section className="rounded-lg border border-stone-800 bg-stone-900">
        <header className="flex items-center justify-between border-b border-stone-800 bg-stone-950/30 px-4 py-3">
          <h2 className="text-sm font-semibold text-stone-200">Authored Documentation</h2>
          <span className="text-[10px] text-stone-500">{filteredDocs.length} items</span>
        </header>
        <div className="divide-y divide-stone-800/50">
          {filteredDocs.length === 0 ? (
            <p className="px-4 py-3 text-sm text-stone-500">
              {loading ? "Loading knowledge documents..." : "No documentation entries found."}
            </p>
          ) : null}

          {filteredDocs.map((doc) => (
            <article key={doc.id} className="group flex items-center justify-between px-4 py-3 transition hover:bg-stone-800/30">
              <div className="min-w-0">
                <h3 className="truncate text-sm font-medium text-stone-300 transition group-hover:text-amber-400">{doc.title}</h3>
                <div className="mt-1 flex items-center gap-3 text-[10px] text-stone-500">
                  <span>{doc.author}</span>
                  <span>•</span>
                  <span>Edited {doc.lastEdited}</span>
                  {doc.tags.length > 0 ? (
                    <>
                      <span>•</span>
                      <span className="truncate">{doc.tags.join(", ")}</span>
                    </>
                  ) : null}
                </div>
              </div>
              <span
                className={`rounded border px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${
                  doc.status === "current"
                    ? "border-lime-500/20 bg-lime-500/10 text-lime-400"
                    : "border-stone-700 bg-stone-800 text-stone-500"
                }`}
              >
                {doc.status}
              </span>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}
