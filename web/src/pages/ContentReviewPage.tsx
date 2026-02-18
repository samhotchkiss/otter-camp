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
    <section className="space-y-4" data-testid="content-review-route-adapter">
      <header>
        <h1 className="page-title">Content Review</h1>
        <p className="page-subtitle">{path}</p>
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
