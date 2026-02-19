type ConversationExtract = {
  id: number;
  source: string;
  extract: string;
  timestamp: string;
  confidence: number;
};

type TaxonomyCategory = {
  name: string;
  entries: number;
  lastUpdated: string;
  tone: "amber" | "orange" | "lime" | "emerald";
};

type ProjectDoc = {
  title: string;
  author: string;
  lastEdited: string;
  status: "current" | "archived";
};

type MemoryStat = {
  label: string;
  value: string;
  tone: "amber" | "orange" | "lime";
};

const CONVERSATION_EXTRACTS: ConversationExtract[] = [
  {
    id: 1,
    source: "Agent-042 -> Orchestrator",
    extract: "User prefers React hooks over class components",
    timestamp: "5 min ago",
    confidence: 0.95,
  },
  {
    id: 2,
    source: "Agent-127 -> Memory System",
    extract: "API rate limit set to 1000 req/min per customer",
    timestamp: "12 min ago",
    confidence: 0.98,
  },
  {
    id: 3,
    source: "Agent-089 -> Staffing Manager",
    extract: "Database queries optimized using indexing strategy",
    timestamp: "23 min ago",
    confidence: 0.92,
  },
  {
    id: 4,
    source: "Orchestrator -> Agent-042",
    extract: "Customer Portal requires mobile-first responsive design",
    timestamp: "1h ago",
    confidence: 0.96,
  },
];

const TAXONOMY_CATEGORIES: TaxonomyCategory[] = [
  { name: "Technical Preferences", entries: 45, lastUpdated: "2 min ago", tone: "amber" },
  { name: "Project Requirements", entries: 78, lastUpdated: "15 min ago", tone: "orange" },
  { name: "Code Patterns", entries: 123, lastUpdated: "1h ago", tone: "lime" },
  { name: "System Architecture", entries: 34, lastUpdated: "3h ago", tone: "emerald" },
  { name: "Agent Behaviors", entries: 67, lastUpdated: "30 min ago", tone: "amber" },
];

const PROJECT_DOCS: ProjectDoc[] = [
  { title: "Customer Portal - Authentication Flow", author: "Memory System", lastEdited: "10 min ago", status: "current" },
  { title: "API Gateway - Security Best Practices", author: "Agent-127", lastEdited: "1h ago", status: "current" },
  { title: "Database Optimization Guidelines", author: "Agent-089", lastEdited: "2h ago", status: "current" },
  { title: "Deployment Pipeline Configuration", author: "Orchestrator", lastEdited: "5h ago", status: "archived" },
];

const MEMORY_STATS: MemoryStat[] = [
  { label: "Vector Embeddings", value: "12,847", tone: "amber" },
  { label: "Entity Syntheses", value: "342", tone: "orange" },
  { label: "File-Backed Memories", value: "1,204", tone: "lime" },
  { label: "Context Injections", value: "3,891", tone: "amber" },
];

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

export default function KnowledgePage() {
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
        <div className="relative">
          <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-stone-500">⌕</span>
          <input
            type="text"
            placeholder="Search memory graph..."
            className="w-64 rounded-lg border border-stone-800 bg-stone-900 py-2 pl-9 pr-4 text-xs text-stone-300 outline-none placeholder:text-stone-600 focus:border-amber-500/50 focus:ring-1 focus:ring-amber-500/40"
          />
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        {MEMORY_STATS.map((stat) => (
          <article key={stat.label} className="group relative overflow-hidden rounded-lg border border-stone-800 bg-stone-900 p-4">
            <div className={`absolute inset-0 opacity-20 transition-opacity group-hover:opacity-30 ${statToneClasses(stat.tone)}`} />
            <div className="relative z-10">
              <div className="mb-2 flex items-start justify-between">
                <span className={`rounded-lg border px-2 py-1 text-xs ${statToneClasses(stat.tone)}`}>◉</span>
                <span className="text-lime-500">↗</span>
              </div>
              <p className="text-2xl font-bold tracking-tight text-stone-200">{stat.value}</p>
              <p className="mt-1 text-[10px] font-semibold uppercase tracking-wider text-stone-500">{stat.label}</p>
            </div>
          </article>
        ))}
      </div>

      <div className="grid h-[500px] grid-cols-1 gap-6 lg:grid-cols-3">
        <section className="flex flex-col overflow-hidden rounded-lg border border-stone-800 bg-stone-900 lg:col-span-2">
          <header className="flex items-center justify-between border-b border-stone-800 bg-stone-950/30 p-4">
            <h2 className="text-sm font-semibold text-stone-200">Stream: Conversation Extraction</h2>
            <span className="rounded border border-lime-500/20 bg-lime-500/10 px-2 py-0.5 text-[10px] text-lime-400">LIVE</span>
          </header>
          <div className="flex-1 space-y-2 overflow-y-auto p-2">
            {CONVERSATION_EXTRACTS.map((extract) => (
              <article key={extract.id} className="group rounded border border-stone-800/50 bg-stone-950/50 p-3 transition hover:border-amber-500/30">
                <div className="mb-2 flex items-start justify-between gap-2">
                  <div>
                    <div className="mb-1 flex items-center gap-2">
                      <span className="rounded bg-amber-500/10 px-1.5 text-[10px] text-amber-400">{extract.source}</span>
                      <span className="text-[10px] text-stone-600">{extract.timestamp}</span>
                    </div>
                    <p className="text-sm text-stone-300 transition group-hover:text-stone-100">"{extract.extract}"</p>
                  </div>
                  <div className="ml-3 text-right">
                    <p className="text-[10px] text-stone-500">CONFIDENCE</p>
                    <p className="text-xs font-bold text-lime-400">{Math.round(extract.confidence * 100)}%</p>
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
            {TAXONOMY_CATEGORIES.map((category) => (
              <article
                key={category.name}
                className={`flex cursor-pointer items-center justify-between rounded border bg-stone-950/50 p-3 transition ${toneClasses(category.tone)}`}
              >
                <div>
                  <h3 className="text-xs font-medium text-stone-300">{category.name}</h3>
                  <p className="text-[10px] text-stone-600">Updated {category.lastUpdated}</p>
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
          <button className="text-[10px] text-stone-500 transition hover:text-stone-300" type="button">Export All</button>
        </header>
        <div className="divide-y divide-stone-800/50">
          {PROJECT_DOCS.map((doc) => (
            <article key={doc.title} className="group flex items-center justify-between px-4 py-3 transition hover:bg-stone-800/30">
              <div>
                <h3 className="text-sm font-medium text-stone-300 transition group-hover:text-amber-400">{doc.title}</h3>
                <div className="mt-1 flex items-center gap-3 text-[10px] text-stone-500">
                  <span>{doc.author}</span>
                  <span>•</span>
                  <span>Edited {doc.lastEdited}</span>
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
