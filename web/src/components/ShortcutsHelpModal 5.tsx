import { SHORTCUT_DEFINITIONS, formatShortcut, type ShortcutDefinition } from "../hooks/useKeyboardShortcuts";
import { useFocusTrap } from "../hooks/useFocusTrap";

type ShortcutsHelpModalProps = {
  isOpen: boolean;
  onClose: () => void;
};

const CATEGORY_ORDER = ["General", "Tasks", "Navigation"] as const;

function groupByCategory(shortcuts: ShortcutDefinition[]) {
  const groups = new Map<string, ShortcutDefinition[]>();
  
  for (const category of CATEGORY_ORDER) {
    groups.set(category, []);
  }
  
  for (const shortcut of shortcuts) {
    const list = groups.get(shortcut.category);
    if (list) {
      list.push(shortcut);
    }
  }
  
  return groups;
}

export default function ShortcutsHelpModal({ isOpen, onClose }: ShortcutsHelpModalProps) {
  // Focus trap for modal accessibility
  const { containerRef } = useFocusTrap({
    isActive: isOpen,
    onEscape: onClose,
    returnFocusOnClose: true,
  });

  if (!isOpen) return null;

  const grouped = groupByCategory(SHORTCUT_DEFINITIONS);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 px-4 py-6 text-slate-100 backdrop-blur-sm"
      onClick={onClose}
      aria-hidden="true"
    >
      <div
        ref={containerRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="shortcuts-modal-title"
        aria-describedby="shortcuts-modal-description"
        className="w-full max-w-2xl overflow-hidden rounded-2xl border border-slate-800 bg-slate-900/95 shadow-2xl shadow-slate-950/40 outline-none"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-slate-800 px-6 py-4">
          <div className="flex items-center gap-3">
            <div className="inline-flex h-10 w-10 items-center justify-center rounded-full bg-slate-800 text-lg">
              ⌨️
            </div>
            <div>
              <h2 id="shortcuts-modal-title" className="text-lg font-semibold text-white">Keyboard Shortcuts</h2>
              <p id="shortcuts-modal-description" className="text-sm text-slate-400">Navigate faster with these shortcuts</p>
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-2 text-slate-400 transition hover:bg-slate-800 hover:text-white"
            aria-label="Close"
          >
            <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Content */}
        <div className="max-h-[60vh] overflow-y-auto px-6 py-4">
          <div className="grid gap-6 sm:grid-cols-2">
            {CATEGORY_ORDER.map((category) => {
              const shortcuts = grouped.get(category) ?? [];
              if (shortcuts.length === 0) return null;

              return (
                <div key={category}>
                  <h3 id={`shortcuts-category-${category.toLowerCase()}`} className="mb-3 text-xs font-semibold uppercase tracking-[0.25em] text-slate-500">
                    {category}
                  </h3>
                  <div className="space-y-2">
                    {shortcuts.map((shortcut, index) => (
                      <div
                        key={`${category}-${index}`}
                        className="flex items-center justify-between rounded-lg px-3 py-2 transition hover:bg-slate-800/50"
                      >
                        <span className="text-sm text-slate-300">{shortcut.description}</span>
                        <kbd className="ml-4 inline-flex min-w-[2.5rem] items-center justify-center rounded-md border border-slate-700 bg-slate-800 px-2 py-1 text-xs font-medium text-slate-300">
                          {formatShortcut(shortcut)}
                        </kbd>
                      </div>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        {/* Footer */}
        <div className="border-t border-slate-800 px-6 py-4">
          <p className="text-center text-xs text-slate-500">
            Press <kbd className="rounded bg-slate-800 px-1.5 py-0.5 text-xs">Esc</kbd> to close
          </p>
        </div>
      </div>
    </div>
  );
}
