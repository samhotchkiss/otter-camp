/**
 * KnowledgePage - Second Brain / Knowledge Base
 * Design spec: Jeff G
 * 
 * Features:
 * - Searchable entry list
 * - Tag filtering
 * - Card grid layout
 * - Entry detail modal
 */

import { useState, useEffect } from 'react';
import { api } from '../lib/api';

interface KnowledgeEntry {
  id: string;
  title: string;
  content: string;
  tags: string[];
  created_by: string;
  created_at: string;
  updated_at: string;
}

// Demo entries for MVP
const DEMO_ENTRIES: KnowledgeEntry[] = [
  {
    id: 'kb-001',
    title: "Sam's email preferences",
    content: "- Respond same-day to investors\n- Archive newsletters automatically\n- Flag anything from family\n- VIP list: investors, advisors, close friends",
    tags: ['preferences', 'email', 'penny'],
    created_by: 'Penny',
    created_at: '2026-02-02T10:00:00Z',
    updated_at: '2026-02-02T10:00:00Z',
  },
  {
    id: 'kb-002',
    title: 'Meeting scheduling rules',
    content: "- No meetings before 10am\n- Batch meetings on Tuesdays and Thursdays\n- Always leave 15min buffer between calls\n- Lunch is sacred: 12-1pm blocked",
    tags: ['preferences', 'calendar', 'max'],
    created_by: 'Max',
    created_at: '2026-02-01T14:00:00Z',
    updated_at: '2026-02-01T14:00:00Z',
  },
  {
    id: 'kb-003',
    title: 'ItsAlive deployment process',
    content: "1. Run tests locally\n2. Push to staging branch\n3. Wait for CI green\n4. Get Ivy's approval\n5. Merge to main\n6. Monitor for 30 minutes",
    tags: ['process', 'itsalive', 'deployment'],
    created_by: 'Derek',
    created_at: '2026-01-28T09:00:00Z',
    updated_at: '2026-02-03T16:00:00Z',
  },
  {
    id: 'kb-004',
    title: 'Content voice guidelines',
    content: "- First person, conversational\n- No jargon unless necessary\n- Short paragraphs (3-4 sentences max)\n- Always include a takeaway\n- Humor is welcome but not forced",
    tags: ['content', 'writing', 'stone'],
    created_by: 'Stone',
    created_at: '2026-01-25T11:00:00Z',
    updated_at: '2026-01-25T11:00:00Z',
  },
  {
    id: 'kb-005',
    title: 'Social media posting times',
    content: "**Twitter/X:**\n- Best: 9am, 12pm, 5pm MST\n- Avoid weekends\n\n**LinkedIn:**\n- Best: Tuesday-Thursday, 8-10am\n- Long-form on Wednesdays",
    tags: ['social', 'nova', 'schedule'],
    created_by: 'Nova',
    created_at: '2026-01-20T08:00:00Z',
    updated_at: '2026-02-01T08:00:00Z',
  },
  {
    id: 'kb-006',
    title: 'Market alert thresholds',
    content: "- Portfolio swing > 2%: alert immediately\n- Watchlist item moves > 5%: notify\n- Major index drops > 3%: full briefing\n- Crypto volatility: only BTC/ETH, >10% moves",
    tags: ['markets', 'alerts', 'beau'],
    created_by: 'Beau H',
    created_at: '2026-01-15T07:00:00Z',
    updated_at: '2026-01-30T07:00:00Z',
  },
];

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
  const [entries, setEntries] = useState<KnowledgeEntry[]>(DEMO_ENTRIES);
  const [selectedTag, setSelectedTag] = useState<string | null>(null);
  const [selectedEntry, setSelectedEntry] = useState<KnowledgeEntry | null>(null);

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
      {filteredEntries.length === 0 && (
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
