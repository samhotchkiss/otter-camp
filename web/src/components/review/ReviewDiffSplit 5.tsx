import ReviewCommentThread from "./ReviewCommentThread";
import type { DiffFile, DiffLine } from "./types";

type ReviewDiffSplitProps = {
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

function getLineKey(line: DiffLine, side: "left" | "right"): number | undefined {
  return side === "left" ? line.oldNumber : line.newNumber;
}

function getLineContent(line: DiffLine, side: "left" | "right"): string {
  if (line.type === "add" && side === "left") return "";
  if (line.type === "del" && side === "right") return "";
  return `${line.type === "add" ? "+" : line.type === "del" ? "-" : " "}${line.content}`;
}

function getLineType(line: DiffLine, side: "left" | "right"): "context" | "add" | "del" {
  if (line.type === "add" && side === "left") return "context";
  if (line.type === "del" && side === "right") return "context";
  return line.type;
}

export default function ReviewDiffSplit({ file }: ReviewDiffSplitProps) {
  const threadMap = new Map(
    (file.commentThreads ?? [])
      .filter((thread) => thread.side !== "unified")
      .map((thread) => [`${thread.side}:${thread.lineNumber}`, thread])
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
            <div className="grid grid-cols-[64px_minmax(0,1fr)_64px_minmax(0,1fr)] font-mono text-xs">
              {hunk.lines.map((line) => {
                const leftLineNumber = getLineKey(line, "left");
                const rightLineNumber = getLineKey(line, "right");
                const leftThread = leftLineNumber
                  ? threadMap.get(`left:${leftLineNumber}`)
                  : undefined;
                const rightThread = rightLineNumber
                  ? threadMap.get(`right:${rightLineNumber}`)
                  : undefined;
                const leftType = getLineType(line, "left");
                const rightType = getLineType(line, "right");
                return (
                  <div key={line.id} className="contents">
                    <div className={`border-r border-otter-border px-3 py-1 text-right ${LINE_TEXT[leftType]}`}>
                      {leftLineNumber ?? ""}
                    </div>
                    <div className={`border-r border-otter-border px-3 py-1 ${LINE_BG[leftType]}`}>
                      <span className={`${LINE_TEXT[leftType]} ${line.isDimmed ? "opacity-60" : ""}`}>
                        {getLineContent(line, "left")}
                      </span>
                    </div>
                    <div className={`border-r border-otter-border px-3 py-1 text-right ${LINE_TEXT[rightType]}`}>
                      {rightLineNumber ?? ""}
                    </div>
                    <div className={`px-3 py-1 ${LINE_BG[rightType]}`}>
                      <span className={`${LINE_TEXT[rightType]} ${line.isDimmed ? "opacity-60" : ""}`}>
                        {getLineContent(line, "right")}
                      </span>
                    </div>
                    {leftThread ? (
                      <div className="col-span-4 border-t border-otter-border px-6 py-3">
                        <ReviewCommentThread thread={leftThread} />
                      </div>
                    ) : null}
                    {rightThread ? (
                      <div className="col-span-4 border-t border-otter-border px-6 py-3">
                        <ReviewCommentThread thread={rightThread} />
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
