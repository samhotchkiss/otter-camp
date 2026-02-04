import { useEffect, useRef } from "react";

export type ShortcutModifiers = {
  cmd?: boolean; // Cmd on Mac, Ctrl on Windows
  shift?: boolean;
  alt?: boolean;
};

export type Shortcut = {
  key: string;
  modifiers?: ShortcutModifiers;
  description: string;
  category: "Navigation" | "Tasks" | "General";
  action: () => void;
  /** Skip when user is typing in an input/textarea */
  skipInInput?: boolean;
};

export type ShortcutDefinition = Omit<Shortcut, "action">;

// All available shortcuts for the help modal
export const SHORTCUT_DEFINITIONS: ShortcutDefinition[] = [
  // General
  { key: "k", modifiers: { cmd: true }, description: "Open command palette", category: "General" },
  { key: "/", modifiers: { cmd: true }, description: "Show keyboard shortcuts", category: "General" },
  { key: "Escape", description: "Close modals/panels", category: "General" },
  
  // Tasks
  { key: "n", modifiers: { cmd: true }, description: "Create new task", category: "Tasks" },
  { key: "j", description: "Move down in task list", category: "Tasks", skipInInput: true },
  { key: "k", description: "Move up in task list", category: "Tasks", skipInInput: true },
  { key: "Enter", description: "Open selected task", category: "Tasks", skipInInput: true },
  { key: "1", description: "Set priority: Low", category: "Tasks", skipInInput: true },
  { key: "2", description: "Set priority: Medium", category: "Tasks", skipInInput: true },
  { key: "3", description: "Set priority: High", category: "Tasks", skipInInput: true },
  { key: "4", description: "Set priority: Critical", category: "Tasks", skipInInput: true },
];

function isInputElement(element: Element | null): boolean {
  if (!element) return false;
  const tagName = element.tagName.toLowerCase();
  return (
    tagName === "input" ||
    tagName === "textarea" ||
    tagName === "select" ||
    (element as HTMLElement).isContentEditable
  );
}

function matchesShortcut(event: KeyboardEvent, shortcut: Shortcut): boolean {
  const { key, modifiers = {} } = shortcut;
  
  // Check key (case-insensitive for letters)
  const eventKey = event.key.toLowerCase();
  const shortcutKey = key.toLowerCase();
  
  if (eventKey !== shortcutKey) return false;
  
  // Check modifiers
  const isCmdOrCtrl = event.metaKey || event.ctrlKey;
  const wantsCmd = modifiers.cmd ?? false;
  
  if (wantsCmd !== isCmdOrCtrl) return false;
  if ((modifiers.shift ?? false) !== event.shiftKey) return false;
  if ((modifiers.alt ?? false) !== event.altKey) return false;
  
  return true;
}

export function useKeyboardShortcuts(shortcuts: Shortcut[]) {
  const shortcutsRef = useRef(shortcuts);
  shortcutsRef.current = shortcuts;

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const activeElement = document.activeElement;
      const inInput = isInputElement(activeElement);

      for (const shortcut of shortcutsRef.current) {
        // Skip input-sensitive shortcuts when in input
        if (inInput && shortcut.skipInInput !== false && !shortcut.modifiers?.cmd) {
          continue;
        }

        if (matchesShortcut(event, shortcut)) {
          event.preventDefault();
          shortcut.action();
          return;
        }
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);
}

// Format shortcut for display
export function formatShortcut(shortcut: ShortcutDefinition): string {
  const parts: string[] = [];
  
  if (shortcut.modifiers?.cmd) {
    parts.push("⌘");
  }
  if (shortcut.modifiers?.shift) {
    parts.push("⇧");
  }
  if (shortcut.modifiers?.alt) {
    parts.push("⌥");
  }
  
  // Format special keys
  const keyDisplay = {
    escape: "Esc",
    enter: "↵",
    arrowup: "↑",
    arrowdown: "↓",
    arrowleft: "←",
    arrowright: "→",
  }[shortcut.key.toLowerCase()] ?? shortcut.key.toUpperCase();
  
  parts.push(keyDisplay);
  
  return parts.join(" ");
}
