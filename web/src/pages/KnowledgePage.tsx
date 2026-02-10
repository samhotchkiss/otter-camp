/**
 * KnowledgePage - Second Brain / Knowledge Base
 * Design spec: Jeff G
 * 
 * Features:
 * - Tag filtering
 * - Card grid layout
 * - Entry detail modal
 */

import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { apiFetch } from '../lib/api';

interface KnowledgeEntry {
  id: string;
  title: string;
  content: string;
  tags: string[];
  created_by: string;
  created_at: string;
  updated_at: string;
}

interface KnowledgeListResponse {
  items: KnowledgeEntry[];
  total: number;
}

interface MemoryEvaluationSummary {
  id: string;
  created_at: string;
  passed: boolean;
  failed_gates?: string[];
  metrics?: {
    precision_at_k?: number;
    false_injection_rate?: number;
    recovery_success_rate?: number;
    p95_latency_ms?: number;
  };
}

function timeAgo(dateString: string): string {
  const date = new Date(dateString);
  const now = new Date();
  const seconds = Math.floor((now.getTime() - date.getTime()) / 1000);
  
  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  if (seconds < 604800) return `${Math.floor(seconds / 86400)}d ago`;
  return date.toLocaleDateString();
}

export default function KnowledgePage() {
  const [entries, setEntries] = useState<KnowledgeEntry[]>([]);
  const [selectedTag, setSelectedTag] = useState<string | null>(null);
  const [selectedEntry, setSelectedEntry] = useState<KnowledgeEntry | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [evaluation, setEvaluation] = useState<MemoryEvaluationSummary | null>(null);
  const [evaluationLoading, setEvaluationLoading] = useState(true);
  const [evaluationError, setEvaluationError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setLoadError(null);

    void apiFetch<KnowledgeListResponse>('/api/knowledge')
      .then((payload) => {
        if (!active) return;
        const normalizedEntries = (payload.items ?? []).map((entry) => ({
          ...entry,
          tags: entry.tags ?? [],
        }));
        setEntries(normalizedEntries);
      })
      .catch((error: unknown) => {
        if (!active) return;
        const message = error instanceof Error ? error.message : 'Failed to load knowledge entries';
        setLoadError(message);
        setEntries([]);
      })
      .finally(() => {
        if (!active) return;
        setLoading(false);
      });

    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    let active = true;
    setEvaluationLoading(true);
    setEvaluationError(null);

    void apiFetch<{ run?: MemoryEvaluationSummary }>('/api/memory/evaluations/latest')
      .then((payload) => {
        if (!active) return;
        setEvaluation(payload?.run ?? null);
      })
      .catch((error: unknown) => {
        if (!active) return;
        const message = error instanceof Error ? error.message : 'Failed to load memory evaluation summary';
        setEvaluationError(message);
        setEvaluation(null);
      })
      .finally(() => {
        if (!active) return;
        setEvaluationLoading(false);
      });

    return () => {
      active = false;
    };
  }, []);

  // Get all unique tags
  const allTags = Array.from(
    new Set(entries.flatMap((e) => e.tags))
  ).sort();

  // Filter entries by tag (search handled by magic bar)
  const filteredEntries = entries.filter((entry) => {
    return !selectedTag || entry.tags.includes(selectedTag);
  });

  return (
    <div className="knowledge-page">
      {/* Page Header */}
      <header className="page-header">
        <div className="page-header-left">
          <h1 className="page-title">Knowledge Base</h1>
          <span className="entry-count">{entries.length} entries</span>
        </div>
        <button className="btn btn-primary">+ New Entry</button>
      </header>

      <section className="rounded-xl border border-[var(--border)] bg-[var(--surface)] p-4">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-sm font-semibold text-[var(--text)]">Memory Evaluation</h2>
          <Link to="/knowledge/evaluation" className="text-xs font-medium text-[#C9A86C] hover:underline">
            View dashboard
          </Link>
        </div>
        {evaluationLoading && <p className="mt-2 text-sm text-[var(--text-muted)]">Loading latest evaluation…</p>}
        {!evaluationLoading && evaluationError && (
          <p className="mt-2 text-sm text-rose-500">Evaluation unavailable: {evaluationError}</p>
        )}
        {!evaluationLoading && !evaluationError && !evaluation && (
          <p className="mt-2 text-sm text-[var(--text-muted)]">No evaluation runs recorded yet.</p>
        )}
        {!evaluationLoading && !evaluationError && evaluation && (
          <div className="mt-3 grid gap-2 sm:grid-cols-4">
            <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-2">
              <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Status</p>
              <p className={`mt-1 text-sm font-medium ${evaluation.passed ? 'text-emerald-600' : 'text-rose-500'}`}>
                {evaluation.passed ? 'pass' : 'fail'}
              </p>
            </div>
            <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-2">
              <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Precision@k</p>
              <p className="mt-1 text-sm font-medium text-[var(--text)]">
                {typeof evaluation.metrics?.precision_at_k === 'number'
                  ? evaluation.metrics.precision_at_k.toFixed(2)
                  : 'n/a'}
              </p>
            </div>
            <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-2">
              <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">False inject</p>
              <p className="mt-1 text-sm font-medium text-[var(--text)]">
                {typeof evaluation.metrics?.false_injection_rate === 'number'
                  ? evaluation.metrics.false_injection_rate.toFixed(2)
                  : 'n/a'}
              </p>
            </div>
            <div className="rounded-lg border border-[var(--border)] bg-[var(--surface-alt)] p-2">
              <p className="text-xs uppercase tracking-wide text-[var(--text-muted)]">Recovery</p>
              <p className="mt-1 text-sm font-medium text-[var(--text)]">
                {typeof evaluation.metrics?.recovery_success_rate === 'number'
                  ? evaluation.metrics.recovery_success_rate.toFixed(2)
                  : 'n/a'}
              </p>
            </div>
          </div>
        )}
      </section>

      {/* Tag Filters (search handled by magic bar ⌘K) */}
      <div className="knowledge-controls">
        <div className="tag-filters">
          <button
            className={`tag-pill ${!selectedTag ? 'active' : ''}`}
            onClick={() => setSelectedTag(null)}
          >
            All
          </button>
          {allTags.map((tag) => (
            <button
              key={tag}
              className={`tag-pill ${selectedTag === tag ? 'active' : ''}`}
              onClick={() => setSelectedTag(selectedTag === tag ? null : tag)}
            >
              {tag}
            </button>
          ))}
        </div>
      </div>

      {/* Entry Grid */}
      <div className="knowledge-grid">
        {loading && (
          <div className="knowledge-empty">
            <p>Loading knowledge entries…</p>
          </div>
        )}
        {!loading && loadError && (
          <div className="knowledge-empty">
            <p>Unable to load knowledge entries</p>
            <p className="empty-hint">{loadError}</p>
          </div>
        )}
        {filteredEntries.map((entry) => (
          <article
            key={entry.id}
            className="entry-card"
            onClick={() => setSelectedEntry(entry)}
          >
            <div className="entry-card-header">
              <h3 className="entry-title">{entry.title}</h3>
              <span className="entry-author">{entry.created_by}</span>
            </div>
            <p className="entry-preview">{entry.content}</p>
            <div className="entry-card-footer">
              <div className="entry-tags">
                {entry.tags.slice(0, 3).map((tag) => (
                  <span key={tag} className="tag">
                    {tag}
                  </span>
                ))}
              </div>
              <span className="entry-date">{timeAgo(entry.updated_at)}</span>
            </div>
          </article>
        ))}
      </div>

      {/* Empty State */}
      {!loading && !loadError && filteredEntries.length === 0 && (
        <div className="knowledge-empty">
          <span className="empty-icon">📚</span>
          <p>No entries found</p>
          <p className="empty-hint">
            {selectedTag
              ? 'Try selecting a different tag'
              : 'Create your first knowledge entry'}
          </p>
        </div>
      )}

      {/* Entry Detail Modal */}
      {selectedEntry && (
        <div className="modal-overlay" onClick={() => setSelectedEntry(null)}>
          <div className="entry-modal" onClick={(e) => e.stopPropagation()}>
            <header className="entry-modal-header">
              <h2>{selectedEntry.title}</h2>
              <button
                className="modal-close"
                onClick={() => setSelectedEntry(null)}
              >
                ✕
              </button>
            </header>
            <div className="entry-modal-meta">
              <span>Created by {selectedEntry.created_by}</span>
              <span>•</span>
              <span>Updated {timeAgo(selectedEntry.updated_at)}</span>
            </div>
            <div className="entry-modal-tags">
              {selectedEntry.tags.map((tag) => (
                <span key={tag} className="tag">
                  {tag}
                </span>
              ))}
            </div>
            <div className="entry-modal-content">
              <pre>{selectedEntry.content}</pre>
            </div>
            <div className="entry-modal-actions">
              <button className="btn btn-secondary">Edit</button>
              <button className="btn btn-ghost">Delete</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
