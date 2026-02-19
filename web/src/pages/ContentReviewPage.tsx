import { useEffect, useMemo, useState } from "react";
import { useParams } from "react-router-dom";

import DocumentWorkspace from "../components/content-review/DocumentWorkspace";

function decodeDocumentPath(rawPath?: string): string {
  const candidate = (rawPath || "").trim();
  if (!candidate) {
    return "untitled.md";
  }
  try {
    return decodeURIComponent(candidate);
  } catch {
    return candidate;
  }
}

function defaultDocumentContent(path: string): string {
  return [
    `# Review: ${path}`,
    "",
    "This route is an adapter for Figma alias paths.",
    "Detailed content-review redesign is tracked in later specs.",
  ].join("\n");
}

export default function ContentReviewPage() {
  const { documentId } = useParams<{ documentId?: string }>();
  const path = useMemo(() => decodeDocumentPath(documentId), [documentId]);
  const baseContent = useMemo(() => defaultDocumentContent(path), [path]);
  const [content, setContent] = useState(baseContent);

  useEffect(() => {
    setContent(baseContent);
  }, [baseContent]);

  return (
    <section
      className="min-w-0 space-y-5"
      data-testid="content-review-page-shell"
      aria-labelledby="content-review-page-title"
    >
      <header
        className="flex flex-col gap-2 rounded-2xl border border-slate-200 bg-white/80 p-4 shadow-sm sm:flex-row sm:items-start sm:justify-between dark:border-slate-800 dark:bg-slate-900/50"
        data-testid="content-review-route-header"
      >
        <div className="min-w-0">
          <p className="text-xs font-semibold uppercase tracking-[0.3em] text-indigo-500">
            Review Route Adapter
          </p>
          <h1 id="content-review-page-title" className="page-title">Content Review</h1>
          <p className="page-subtitle break-all" data-testid="content-review-route-path">
            {path}
          </p>
        </div>
      </header>
      <DocumentWorkspace
        path={path}
        content={content}
        reviewerName="Otter reviewer"
        onContentChange={setContent}
      />
    </section>
  );
}
