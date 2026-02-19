import { useMemo } from "react";
import { NavLink, useParams } from "react-router-dom";

type ReviewComment = {
  id: string;
  lineNumber: number;
  author: string;
  authorType: "user" | "agent";
  content: string;
  timestamp: string;
  resolved: boolean;
};

const DEFAULT_DOCUMENT_PATH = "docs/rate-limiting-implementation.md";

const REVIEW_COMMENTS: ReviewComment[] = [
  {
    id: "c1",
    lineNumber: 5,
    author: "You",
    authorType: "user",
    content: "Should we mention the Redis dependency here? It's a critical infrastructure requirement.",
    timestamp: "10m ago",
    resolved: false,
  },
  {
    id: "c2",
    lineNumber: 11,
    author: "Agent-127",
    authorType: "agent",
    content: "This example should reference the token bucket implementation.",
    timestamp: "15m ago",
    resolved: false,
  },
  {
    id: "c3",
    lineNumber: 18,
    author: "You",
    authorType: "user",
    content: "Looks good. Ready to publish once unresolved comments are addressed.",
    timestamp: "2m ago",
    resolved: true,
  },
];

const MARKDOWN_CONTENT = `# API Rate Limiting Implementation
## Overview
This document outlines the Redis-backed token bucket implementation for the API Gateway.

## Proposed Solution
Implement a distributed token bucket using Redis to enforce limits across all gateway instances.

## Configuration
- RATE_LIMIT_BUCKET_SIZE
- RATE_LIMIT_REFILL_RATE
- REDIS_URL

## Testing Strategy
1. Unit tests for bucket math
2. Integration tests against Redis
3. Load tests for latency/throughput

## Rollout Plan
1. Stage deployment
2. Canary rollout (10% -> 50% -> 100%)
3. Continuous monitoring`;

function decodeDocumentPath(rawPath?: string): string {
  const candidate = (rawPath || "").trim();
  if (!candidate) {
    return DEFAULT_DOCUMENT_PATH;
  }
  try {
    return decodeURIComponent(candidate);
  } catch {
    return candidate;
  }
}

export default function ContentReviewPage() {
  const { documentId } = useParams<{ documentId?: string }>();
  const path = useMemo(() => decodeDocumentPath(documentId), [documentId]);
  const lines = useMemo(() => MARKDOWN_CONTENT.split("\n"), []);
  const unresolvedCount = REVIEW_COMMENTS.filter((comment) => !comment.resolved).length;
  const resolvedCount = REVIEW_COMMENTS.filter((comment) => comment.resolved).length;

  return (
    <section
      className="min-w-0 space-y-4 md:space-y-6"
      data-testid="content-review-page-shell"
      aria-labelledby="content-review-page-title"
    >
      <header className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <NavLink
            to="/issue/ISS-209"
            className="rounded-md p-2 text-stone-400 transition-colors hover:bg-stone-800 hover:text-stone-200"
            aria-label="Back to issue"
          >
            ←
          </NavLink>
          <div>
            <h1 id="content-review-page-title" className="text-xl font-bold text-stone-100 md:text-2xl">
              Content Review
            </h1>
            <p className="text-sm text-stone-400" data-testid="content-review-route-path">{path}</p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <button
            type="button"
            className="rounded-md bg-stone-800 px-4 py-2 text-sm font-medium text-stone-300 transition-colors hover:bg-stone-700"
          >
            Request Changes
          </button>
          <button
            type="button"
            className="rounded-md bg-lime-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-lime-700"
          >
            Approve
          </button>
        </div>
      </header>

      <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <div className="rounded-lg border border-stone-800 bg-stone-900 p-3">
          <p className="text-xs uppercase tracking-wider text-stone-500">Comments</p>
          <p className="mt-1 text-xl font-bold text-stone-100">{REVIEW_COMMENTS.length}</p>
        </div>
        <div className="rounded-lg border border-amber-700/40 bg-amber-950/30 p-3">
          <p className="text-xs uppercase tracking-wider text-amber-400">Unresolved</p>
          <p className="mt-1 text-xl font-bold text-amber-300">{unresolvedCount}</p>
        </div>
        <div className="rounded-lg border border-lime-700/40 bg-lime-950/30 p-3">
          <p className="text-xs uppercase tracking-wider text-lime-400">Resolved</p>
          <p className="mt-1 text-xl font-bold text-lime-300">{resolvedCount}</p>
        </div>
        <div className="rounded-lg border border-stone-800 bg-stone-900 p-3">
          <p className="text-xs uppercase tracking-wider text-stone-500">Lines</p>
          <p className="mt-1 text-xl font-bold text-stone-100">{lines.length}</p>
        </div>
      </section>

      <div className="grid grid-cols-1 gap-4 md:gap-6 lg:grid-cols-3">
        <section className="lg:col-span-2">
          <div className="overflow-hidden rounded-lg border border-stone-800 bg-stone-900">
            <div className="flex items-center justify-between border-b border-stone-800 bg-stone-950 px-4 py-3">
              <h2 className="text-sm font-semibold text-stone-200">Document</h2>
              <span className="text-xs text-stone-500">Last edited 15m ago by Agent-007</span>
            </div>
            <div className="divide-y divide-stone-800/60">
              {lines.map((line, index) => {
                const lineNumber = index + 1;
                const commentsOnLine = REVIEW_COMMENTS.filter((comment) => comment.lineNumber === lineNumber);
                const hasUnresolved = commentsOnLine.some((comment) => !comment.resolved);

                return (
                  <div key={`line-${lineNumber}`} className="flex items-start">
                    <div className="w-12 shrink-0 border-r border-stone-800 bg-stone-950/60 px-2 py-2 text-right font-mono text-xs text-stone-600">
                      {lineNumber}
                    </div>
                    <div className="flex-1 px-4 py-2 font-mono text-sm text-stone-300">{line || "\u00A0"}</div>
                    <div className="w-12 shrink-0 border-l border-stone-800 px-2 py-2 text-center">
                      {commentsOnLine.length > 0 ? (
                        <span
                          className={`inline-flex h-5 min-w-5 items-center justify-center rounded-full px-1 text-[10px] font-semibold ${
                            hasUnresolved ? "bg-amber-500/20 text-amber-300" : "bg-lime-500/20 text-lime-300"
                          }`}
                        >
                          {commentsOnLine.length}
                        </span>
                      ) : (
                        <span className="text-xs text-stone-700">·</span>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        </section>

        <aside>
          <div className="sticky top-6 rounded-lg border border-stone-800 bg-stone-900">
            <div className="border-b border-stone-800 bg-stone-950 px-4 py-3">
              <h2 className="text-sm font-semibold text-stone-200">All Comments</h2>
            </div>
            <div className="max-h-[560px] space-y-3 overflow-y-auto p-4">
              {REVIEW_COMMENTS.map((comment) => (
                <article
                  key={comment.id}
                  className={`rounded-lg border p-3 ${
                    comment.resolved ? "border-lime-900/40 bg-lime-950/20" : "border-stone-800 bg-stone-950"
                  }`}
                >
                  <div className="mb-1 flex items-center gap-2">
                    <span className="rounded-full bg-stone-700 px-2 py-0.5 text-[10px] font-semibold text-stone-200">
                      {comment.authorType === "agent" ? "AG" : "U"}
                    </span>
                    <span className="text-xs font-medium text-stone-200">{comment.author}</span>
                    <span className="text-xs text-stone-600">{comment.timestamp}</span>
                  </div>
                  <p className="text-xs text-stone-400">Line {comment.lineNumber}</p>
                  <p className="mt-2 text-sm text-stone-300">{comment.content}</p>
                </article>
              ))}
            </div>
          </div>
        </aside>
      </div>
    </section>
  );
}
