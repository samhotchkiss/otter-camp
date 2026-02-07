import ReviewCommentThread from "./ReviewCommentThread";
import type { DiffFile } from "./types";

type ReviewDiffUnifiedProps = {
  file: DiffFile;
};

const LINE_BG: Record<string, string> = {
  add: "bg-emerald-50",
  del: "bg-rose-50",
  context: "bg-otter-surface",
};

const LINE_TEXT: Record<string, string> = {
  add: "text-emerald-700",
  del: "text-rose-700",
  context: "text-otter-muted",
};

export default function ReviewDiffUnified({ file }: ReviewDiffUnifiedProps) {
  const threadMap = new Map(
    (file.commentThreads ?? [])
      .filter((thread) => thread.side === "unified")
      .map((thread) => [thread.lineNumber, thread])
  );

  return (
    <section className="rounded-lg border border-otter-border bg-otter-surface shadow-sm">
      <header className="flex items-center justify-between border-b border-otter-border bg-otter-surface-alt px-4 py-3">
        <div>
          <h3 className="text-sm font-semibold text-otter-text">{file.path}</h3>
          {file.previousPath ? (
            <p className="text-xs text-otter-muted">Renamed from {file.previousPath}</p>
          ) : null}
        </div>
        <div className="flex items-center gap-3 text-xs text-otter-muted">
          <span className="text-emerald-700">+{file.additions}</span>
          <span className="text-rose-700">-{file.deletions}</span>
        </div>
      </header>
      <div className="divide-y divide-otter-border">
        {file.hunks.map((hunk) => (
          <div key={hunk.id}>
            <div className="bg-otter-surface-alt px-4 py-2 text-xs font-mono text-otter-muted">
              {hunk.header}
            </div>
            <div className="grid grid-cols-[64px_64px_minmax(0,1fr)] font-mono text-xs">
              {hunk.lines.map((line) => {
                const thread = line.newNumber ? threadMap.get(line.newNumber) : undefined;
                return (
                  <div key={line.id} className="contents">
                    <div
                      className={`border-r border-otter-border px-3 py-1 text-right ${LINE_TEXT[line.type]}`}
                    >
                      {line.oldNumber ?? ""}
                    </div>
                    <div
                      className={`border-r border-otter-border px-3 py-1 text-right ${LINE_TEXT[line.type]}`}
                    >
                      {line.newNumber ?? ""}
                    </div>
                    <div className={`px-3 py-1 ${LINE_BG[line.type]}`}>
                      <span className={`${LINE_TEXT[line.type]} ${line.isDimmed ? "opacity-60" : ""}`}>
                        {line.type === "add" ? "+" : line.type === "del" ? "-" : " "}
                        {line.content}
                      </span>
                    </div>
                    {thread ? (
                      <div className="col-span-3 border-t border-otter-border px-6 py-3">
                        <ReviewCommentThread thread={thread} />
                      </div>
                    ) : null}
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}
