import { useCallback, useState, useRef } from "react";

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

type ValidationResult = {
  valid: boolean;
  version: string;
  org_id: string;
  exported_at: string;
  task_count: number;
  project_count: number;
  agent_count: number;
  activity_count: number;
  total_items: number;
  errors: string[];
  warnings: string[];
};

type ImportResult = {
  success: boolean;
  dry_run: boolean;
  tasks_imported: number;
  tasks_skipped: number;
  projects_imported: number;
  projects_skipped: number;
  agents_imported: number;
  agents_skipped: number;
  errors: string[];
  warnings: string[];
};

type ImportMode = "merge" | "replace";

// ─────────────────────────────────────────────────────────────────────────────
// Button Component
// ─────────────────────────────────────────────────────────────────────────────

type ButtonProps = {
  children: React.ReactNode;
  onClick?: () => void;
  variant?: "primary" | "secondary" | "danger";
  disabled?: boolean;
  loading?: boolean;
  type?: "button" | "submit";
};

function Button({
  children,
  onClick,
  variant = "primary",
  disabled,
  loading,
  type = "button",
}: ButtonProps) {
  const baseClasses =
    "inline-flex items-center justify-center gap-2 rounded-lg px-4 py-2.5 text-sm font-medium shadow-sm transition focus:outline-none focus:ring-2 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-slate-900";

  const variantClasses = {
    primary:
      "bg-emerald-500 text-white hover:bg-emerald-600 focus:ring-emerald-500",
    secondary:
      "border border-otter-border bg-white text-otter-text hover:bg-otter-surface-alt focus:ring-slate-500 dark:border-otter-dark-border dark:bg-otter-dark-surface dark:text-otter-dark-text dark:hover:bg-otter-dark-surface-alt",
    danger: "bg-red-500 text-white hover:bg-red-600 focus:ring-red-500",
  };

  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled || loading}
      className={`${baseClasses} ${variantClasses[variant]}`}
    >
      {loading && (
        <svg className="h-4 w-4 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle
            className="opacity-25"
            cx="12"
            cy="12"
            r="10"
            stroke="currentColor"
            strokeWidth="4"
          />
          <path
            className="opacity-75"
            fill="currentColor"
            d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
          />
        </svg>
      )}
      {children}
    </button>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Progress Bar Component
// ─────────────────────────────────────────────────────────────────────────────

type ProgressBarProps = {
  progress: number;
  label?: string;
};

function ProgressBar({ progress, label }: ProgressBarProps) {
  return (
    <div className="w-full">
      {label && (
        <div className="mb-1 flex justify-between text-sm">
          <span className="text-otter-muted dark:text-otter-dark-muted">{label}</span>
          <span className="text-otter-muted dark:text-otter-dark-muted">
            {Math.round(progress)}%
          </span>
        </div>
      )}
      <div className="h-2 w-full overflow-hidden rounded-full bg-otter-surface-alt dark:bg-otter-dark-surface-alt">
        <div
          className="h-full rounded-full bg-emerald-500 transition-all duration-300"
          style={{ width: `${progress}%` }}
        />
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Import Preview Component
// ─────────────────────────────────────────────────────────────────────────────

type ImportPreviewProps = {
  validation: ValidationResult;
  onConfirm: (mode: ImportMode, dryRun: boolean) => void;
  onCancel: () => void;
  importing: boolean;
};

function ImportPreview({
  validation,
  onConfirm,
  onCancel,
  importing,
}: ImportPreviewProps) {
  const [mode, setMode] = useState<ImportMode>("merge");
  const [dryRun, setDryRun] = useState(true);

  return (
    <div className="space-y-4">
      {/* Summary */}
      <div className="rounded-lg border border-otter-border bg-otter-surface-alt p-4 dark:border-otter-dark-border dark:bg-otter-dark-surface/50">
        <h4 className="mb-3 font-medium text-otter-text dark:text-white">
          Import Preview
        </h4>
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <div className="text-center">
            <div className="text-2xl font-bold text-emerald-600 dark:text-emerald-400">
              {validation.task_count}
            </div>
            <div className="text-sm text-otter-muted dark:text-otter-dark-muted">
              Tasks
            </div>
          </div>
          <div className="text-center">
            <div className="text-2xl font-bold text-blue-600 dark:text-blue-400">
              {validation.project_count}
            </div>
            <div className="text-sm text-otter-muted dark:text-otter-dark-muted">
              Projects
            </div>
          </div>
          <div className="text-center">
            <div className="text-2xl font-bold text-purple-600 dark:text-purple-400">
              {validation.agent_count}
            </div>
            <div className="text-sm text-otter-muted dark:text-otter-dark-muted">
              Agents
            </div>
          </div>
          <div className="text-center">
            <div className="text-2xl font-bold text-orange-600 dark:text-orange-400">
              {validation.activity_count}
            </div>
            <div className="text-sm text-otter-muted dark:text-otter-dark-muted">
              Activities
            </div>
          </div>
        </div>
        {validation.exported_at && (
          <p className="mt-3 text-center text-xs text-otter-muted dark:text-otter-dark-muted">
            Exported on{" "}
            {new Date(validation.exported_at).toLocaleDateString("en-US", {
              dateStyle: "medium",
            })}{" "}
            at{" "}
            {new Date(validation.exported_at).toLocaleTimeString("en-US", {
              timeStyle: "short",
            })}
          </p>
        )}
      </div>

      {/* Warnings */}
      {validation.warnings.length > 0 && (
        <div className="rounded-lg border border-yellow-200 bg-yellow-50 p-4 dark:border-yellow-800 dark:bg-yellow-900/20">
          <h4 className="mb-2 font-medium text-yellow-800 dark:text-yellow-200">
            ⚠️ Warnings
          </h4>
          <ul className="list-inside list-disc space-y-1 text-sm text-yellow-700 dark:text-yellow-300">
            {validation.warnings.map((warning, i) => (
              <li key={i}>{warning}</li>
            ))}
          </ul>
        </div>
      )}

      {/* Errors */}
      {validation.errors.length > 0 && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-800 dark:bg-red-900/20">
          <h4 className="mb-2 font-medium text-red-800 dark:text-red-200">
            ❌ Errors
          </h4>
          <ul className="list-inside list-disc space-y-1 text-sm text-red-700 dark:text-red-300">
            {validation.errors.map((error, i) => (
              <li key={i}>{error}</li>
            ))}
          </ul>
        </div>
      )}

      {/* Import Options */}
      {validation.valid && (
        <div className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-otter-text dark:text-otter-dark-muted">
              Import Mode
            </label>
            <div className="mt-2 space-y-2">
              <label className="flex items-center gap-3">
                <input
                  type="radio"
                  name="mode"
                  value="merge"
                  checked={mode === "merge"}
                  onChange={() => setMode("merge")}
                  className="h-4 w-4 border-otter-border text-emerald-500 focus:ring-emerald-500 dark:border-otter-dark-border"
                />
                <div>
                  <span className="font-medium text-otter-text dark:text-white">
                    Merge
                  </span>
                  <span className="ml-2 text-sm text-otter-muted dark:text-otter-dark-muted">
                    Add new items, skip existing
                  </span>
                </div>
              </label>
              <label className="flex items-center gap-3">
                <input
                  type="radio"
                  name="mode"
                  value="replace"
                  checked={mode === "replace"}
                  onChange={() => setMode("replace")}
                  className="h-4 w-4 border-otter-border text-emerald-500 focus:ring-emerald-500 dark:border-otter-dark-border"
                />
                <div>
                  <span className="font-medium text-otter-text dark:text-white">
                    Replace
                  </span>
                  <span className="ml-2 text-sm text-otter-muted dark:text-otter-dark-muted">
                    Delete existing data and import fresh
                  </span>
                </div>
              </label>
            </div>
          </div>

          <label className="flex items-center gap-3">
            <input
              type="checkbox"
              checked={dryRun}
              onChange={(e) => setDryRun(e.target.checked)}
              className="h-4 w-4 rounded border-otter-border text-emerald-500 focus:ring-emerald-500 dark:border-otter-dark-border"
            />
            <div>
              <span className="font-medium text-otter-text dark:text-white">
                Dry Run
              </span>
              <span className="ml-2 text-sm text-otter-muted dark:text-otter-dark-muted">
                Preview changes without actually importing
              </span>
            </div>
          </label>
        </div>
      )}

      {/* Actions */}
      <div className="flex justify-end gap-3">
        <Button variant="secondary" onClick={onCancel} disabled={importing}>
          Cancel
        </Button>
        {validation.valid && (
          <Button
            onClick={() => onConfirm(mode, dryRun)}
            loading={importing}
            disabled={importing}
          >
            {dryRun ? "Preview Import" : "Import Data"}
          </Button>
        )}
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Import Results Component
// ─────────────────────────────────────────────────────────────────────────────

type ImportResultsProps = {
  result: ImportResult;
  onClose: () => void;
  onConfirmImport?: () => void;
};

function ImportResults({ result, onClose, onConfirmImport }: ImportResultsProps) {
  return (
    <div className="space-y-4">
      {/* Status Banner */}
      <div
        className={`rounded-lg p-4 ${
          result.success
            ? "border border-emerald-200 bg-emerald-50 dark:border-emerald-800 dark:bg-emerald-900/20"
            : "border border-red-200 bg-red-50 dark:border-red-800 dark:bg-red-900/20"
        }`}
      >
        <div className="flex items-center gap-2">
          <span className="text-2xl">{result.success ? "✅" : "❌"}</span>
          <div>
            <h4
              className={`font-medium ${
                result.success
                  ? "text-emerald-800 dark:text-emerald-200"
                  : "text-red-800 dark:text-red-200"
              }`}
            >
              {result.dry_run
                ? "Dry Run Complete"
                : result.success
                  ? "Import Complete"
                  : "Import Failed"}
            </h4>
            {result.dry_run && (
              <p className="text-sm text-emerald-700 dark:text-emerald-300">
                No data was actually imported. Review the results below.
              </p>
            )}
          </div>
        </div>
      </div>

      {/* Results Summary */}
      <div className="grid grid-cols-3 gap-4">
        <div className="rounded-lg border border-otter-border p-3 text-center dark:border-otter-dark-border">
          <div className="text-xl font-bold text-emerald-600 dark:text-emerald-400">
            {result.tasks_imported}
          </div>
          <div className="text-xs text-otter-muted dark:text-otter-dark-muted">
            Tasks Imported
          </div>
          {result.tasks_skipped > 0 && (
            <div className="mt-1 text-xs text-otter-muted dark:text-otter-dark-muted">
              ({result.tasks_skipped} skipped)
            </div>
          )}
        </div>
        <div className="rounded-lg border border-otter-border p-3 text-center dark:border-otter-dark-border">
          <div className="text-xl font-bold text-blue-600 dark:text-blue-400">
            {result.projects_imported}
          </div>
          <div className="text-xs text-otter-muted dark:text-otter-dark-muted">
            Projects Imported
          </div>
          {result.projects_skipped > 0 && (
            <div className="mt-1 text-xs text-otter-muted dark:text-otter-dark-muted">
              ({result.projects_skipped} skipped)
            </div>
          )}
        </div>
        <div className="rounded-lg border border-otter-border p-3 text-center dark:border-otter-dark-border">
          <div className="text-xl font-bold text-purple-600 dark:text-purple-400">
            {result.agents_imported}
          </div>
          <div className="text-xs text-otter-muted dark:text-otter-dark-muted">
            Agents Imported
          </div>
          {result.agents_skipped > 0 && (
            <div className="mt-1 text-xs text-otter-muted dark:text-otter-dark-muted">
              ({result.agents_skipped} skipped)
            </div>
          )}
        </div>
      </div>

      {/* Errors */}
      {result.errors && result.errors.length > 0 && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 dark:border-red-800 dark:bg-red-900/20">
          <h4 className="mb-2 font-medium text-red-800 dark:text-red-200">
            Errors
          </h4>
          <ul className="max-h-32 list-inside list-disc space-y-1 overflow-y-auto text-sm text-red-700 dark:text-red-300">
            {result.errors.map((error, i) => (
              <li key={i}>{error}</li>
            ))}
          </ul>
        </div>
      )}

      {/* Actions */}
      <div className="flex justify-end gap-3">
        {result.dry_run && result.success && onConfirmImport && (
          <Button onClick={onConfirmImport}>
            Confirm Import
          </Button>
        )}
        <Button variant="secondary" onClick={onClose}>
          {result.dry_run ? "Cancel" : "Close"}
        </Button>
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Main Data Management Component
// ─────────────────────────────────────────────────────────────────────────────

type DataManagementProps = {
  orgId: string;
};

export default function DataManagement({ orgId }: DataManagementProps) {
  const [exporting, setExporting] = useState(false);
  const [exportProgress, setExportProgress] = useState(0);
  const [importing, setImporting] = useState(false);
  const [importProgress, setImportProgress] = useState(0);
  const [validation, setValidation] = useState<ValidationResult | null>(null);
  const [importResult, setImportResult] = useState<ImportResult | null>(null);
  const [pendingImportData, setPendingImportData] = useState<unknown>(null);
  const [pendingImportMode, setPendingImportMode] = useState<ImportMode>("merge");
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleExport = useCallback(async () => {
    setExporting(true);
    setExportProgress(10);

    try {
      setExportProgress(30);
      const response = await fetch(`/api/export?org_id=${orgId}`);
      setExportProgress(70);

      if (!response.ok) {
        throw new Error("Export failed");
      }

      const data = await response.json();
      setExportProgress(90);

      // Create and download file
      const blob = new Blob([JSON.stringify(data, null, 2)], {
        type: "application/json",
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `otter-camp-export-${new Date().toISOString().split("T")[0]}.json`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);

      setExportProgress(100);
    } catch (error) {
      console.error("Export failed:", error);
      alert("Failed to export data. Please try again.");
    } finally {
      setTimeout(() => {
        setExporting(false);
        setExportProgress(0);
      }, 500);
    }
  }, [orgId]);

  const handleFileSelect = useCallback(
    async (event: React.ChangeEvent<HTMLInputElement>) => {
      const file = event.target.files?.[0];
      if (!file) return;

      // Reset state
      setValidation(null);
      setImportResult(null);
      setPendingImportData(null);
      setImporting(true);
      setImportProgress(10);

      try {
        // Read file
        const text = await file.text();
        setImportProgress(30);

        let data;
        try {
          data = JSON.parse(text);
        } catch {
          alert("Invalid JSON file");
          return;
        }
        setImportProgress(50);

        // Validate with server
        const response = await fetch("/api/import/validate", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(data),
        });
        setImportProgress(80);

        if (!response.ok) {
          throw new Error("Validation request failed");
        }

        const validationResult = await response.json();
        setValidation(validationResult);
        setPendingImportData(data);
        setImportProgress(100);
      } catch (error) {
        console.error("Import validation failed:", error);
        alert("Failed to validate import file. Please try again.");
      } finally {
        setImporting(false);
        setImportProgress(0);
        // Reset file input
        if (fileInputRef.current) {
          fileInputRef.current.value = "";
        }
      }
    },
    []
  );

  const handleConfirmImport = useCallback(
    async (mode: ImportMode, dryRun: boolean) => {
      if (!pendingImportData) return;

      setImporting(true);
      setImportProgress(10);
      setPendingImportMode(mode);

      try {
        setImportProgress(30);
        const response = await fetch(`/api/import?org_id=${orgId}`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            data: pendingImportData,
            mode,
            dry_run: dryRun,
          }),
        });
        setImportProgress(80);

        if (!response.ok) {
          throw new Error("Import failed");
        }

        const result = await response.json();
        setImportResult(result);
        setImportProgress(100);

        // Clear validation if this was a real import
        if (!dryRun) {
          setValidation(null);
          setPendingImportData(null);
        }
      } catch (error) {
        console.error("Import failed:", error);
        alert("Failed to import data. Please try again.");
      } finally {
        setImporting(false);
        setImportProgress(0);
      }
    },
    [pendingImportData, orgId]
  );

  const handleActualImport = useCallback(() => {
    // Import with the same mode but dryRun=false
    handleConfirmImport(pendingImportMode, false);
    setImportResult(null);
  }, [handleConfirmImport, pendingImportMode]);

  const handleCancelImport = useCallback(() => {
    setValidation(null);
    setPendingImportData(null);
    setImportResult(null);
  }, []);

  return (
    <section className="overflow-hidden rounded-2xl border border-otter-border bg-white/90 shadow-sm backdrop-blur dark:border-otter-dark-border dark:bg-otter-dark-bg/90">
      <div className="border-b border-otter-border px-6 py-4 dark:border-otter-dark-border">
        <div className="flex items-center gap-3">
          <span className="text-2xl" aria-hidden="true">
            💾
          </span>
          <div>
            <h2 className="text-lg font-semibold text-otter-text dark:text-white">
              Data Management
            </h2>
            <p className="mt-0.5 text-sm text-otter-muted dark:text-otter-dark-muted">
              Export and import your workspace data
            </p>
          </div>
        </div>
      </div>

      <div className="p-6">
        {/* Show import results if available */}
        {importResult ? (
          <ImportResults 
            result={importResult} 
            onClose={handleCancelImport}
            onConfirmImport={importResult.dry_run ? handleActualImport : undefined}
          />
        ) : validation ? (
          /* Show import preview if validation is done */
          <ImportPreview
            validation={validation}
            onConfirm={handleConfirmImport}
            onCancel={handleCancelImport}
            importing={importing}
          />
        ) : (
          /* Default view with export/import buttons */
          <div className="space-y-6">
            {/* Export Section */}
            <div className="space-y-3">
              <h3 className="font-medium text-otter-text dark:text-white">
                Export Data
              </h3>
              <p className="text-sm text-otter-muted dark:text-otter-dark-muted">
                Download all your workspace data as a JSON file. Includes tasks,
                projects, agents, and recent activity.
              </p>
              {exporting && (
                <ProgressBar progress={exportProgress} label="Exporting..." />
              )}
              <Button
                onClick={handleExport}
                loading={exporting}
                disabled={exporting}
              >
                <svg
                  className="h-4 w-4"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"
                  />
                </svg>
                Export Workspace
              </Button>
            </div>

            <hr className="border-otter-border dark:border-otter-dark-border" />

            {/* Import Section */}
            <div className="space-y-3">
              <h3 className="font-medium text-otter-text dark:text-white">
                Import Data
              </h3>
              <p className="text-sm text-otter-muted dark:text-otter-dark-muted">
                Restore data from a previous export. You can choose to merge
                with existing data or replace it entirely.
              </p>
              {importing && (
                <ProgressBar progress={importProgress} label="Processing..." />
              )}
              <div className="flex items-center gap-4">
                <input
                  ref={fileInputRef}
                  type="file"
                  accept=".json,application/json"
                  onChange={handleFileSelect}
                  className="hidden"
                  id="import-file"
                />
                <label htmlFor="import-file">
                  <Button
                    variant="secondary"
                    disabled={importing}
                    onClick={() => fileInputRef.current?.click()}
                  >
                    <svg
                      className="h-4 w-4"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke="currentColor"
                    >
                      <path
                        strokeLinecap="round"
                        strokeLinejoin="round"
                        strokeWidth={2}
                        d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12"
                      />
                    </svg>
                    Select File to Import
                  </Button>
                </label>
              </div>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}
