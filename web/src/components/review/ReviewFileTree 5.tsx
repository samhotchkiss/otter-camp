import type { DiffFile } from "./types";

type ReviewFileTreeProps = {
  files: DiffFile[];
  selectedFileId?: string;
  onSelectFile?: (fileId: string) => void;
};

const STATUS_STYLES: Record<string, string> = {
  modified: "bg-amber-50 text-amber-700 border-amber-200",
  added: "bg-emerald-50 text-emerald-700 border-emerald-200",
  deleted: "bg-rose-50 text-rose-700 border-rose-200",
  renamed: "bg-sky-50 text-sky-700 border-sky-200",
};

export default function ReviewFileTree({ files, selectedFileId, onSelectFile }: ReviewFileTreeProps) {
  return (
    <aside className="rounded-lg border border-otter-border bg-otter-surface shadow-sm">
      <div className="border-b border-otter-border px-4 py-3">
        <h3 className="text-sm font-semibold text-otter-text">Files changed</h3>
        <p className="text-xs text-otter-muted">{files.length} files</p>
      </div>
      <ul className="max-h-[480px] overflow-y-auto py-2">
        {files.map((file) => {
          const isSelected = file.id === selectedFileId;
          return (
            <li key={file.id}>
              <button
                type="button"
                onClick={() => onSelectFile?.(file.id)}
                className={`flex w-full items-center justify-between gap-2 px-4 py-2 text-left text-sm transition hover:bg-otter-surface-alt ${
                  isSelected ? "bg-otter-surface-alt" : ""
                }`}
              >
                <div className="min-w-0">
                  <p className="truncate font-medium text-otter-text">{file.path}</p>
                  {file.previousPath ? (
                    <p className="truncate text-xs text-otter-muted">was {file.previousPath}</p>
                  ) : null}
                </div>
                <div className="flex items-center gap-2">
                  <span
                    className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase ${
                      STATUS_STYLES[file.status]
                    }`}
                  >
                    {file.status}
                  </span>
                  <span className="text-xs text-emerald-700">+{file.additions}</span>
                  <span className="text-xs text-rose-700">-{file.deletions}</span>
                </div>
              </button>
            </li>
          );
        })}
      </ul>
    </aside>
  );
}
