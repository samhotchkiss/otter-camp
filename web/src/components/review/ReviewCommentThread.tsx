import type { ReviewCommentThread } from "./types";

type ReviewCommentThreadProps = {
  thread: ReviewCommentThread;
};

function formatTimestamp(isoString: string): string {
  const date = new Date(isoString);
  return date.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

export default function ReviewCommentThread({ thread }: ReviewCommentThreadProps) {
  return (
    <div className="rounded-md border border-otter-border bg-otter-surface px-4 py-3 shadow-sm">
      <div className="mb-2 flex items-center justify-between text-xs text-otter-muted">
        <span>Line {thread.lineNumber}</span>
        <span>{thread.isResolved ? "Resolved" : "Open"}</span>
      </div>
      <div className="space-y-3">
        {thread.comments.map((comment) => (
          <div key={comment.id} className="flex gap-3">
            <div className="h-8 w-8 overflow-hidden rounded-full bg-otter-surface-alt">
              {comment.author.avatarUrl ? (
                <img
                  src={comment.author.avatarUrl}
                  alt={comment.author.name}
                  className="h-full w-full object-cover"
                />
              ) : (
                <div className="flex h-full w-full items-center justify-center text-xs font-semibold text-otter-muted">
                  {comment.author.name
                    .split(" ")
                    .map((part) => part[0])
                    .join("")
                    .slice(0, 2)}
                </div>
              )}
            </div>
            <div className="flex-1">
              <div className="flex items-center gap-2 text-sm font-semibold text-otter-text">
                <span>{comment.author.name}</span>
                {comment.author.handle ? (
                  <span className="text-xs font-normal text-otter-muted">@{comment.author.handle}</span>
                ) : null}
                <span className="text-xs font-normal text-otter-muted">{formatTimestamp(comment.createdAt)}</span>
              </div>
              <p className="mt-1 text-sm text-otter-text/90">{comment.body}</p>
              {comment.reactions && comment.reactions.length > 0 ? (
                <div className="mt-2 flex flex-wrap gap-2">
                  {comment.reactions.map((reaction) => (
                    <span
                      key={reaction.id}
                      className="rounded-full border border-otter-border bg-otter-surface-alt px-2 py-1 text-xs text-otter-muted"
                    >
                      {reaction.emoji} {reaction.count}
                    </span>
                  ))}
                </div>
              ) : null}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
