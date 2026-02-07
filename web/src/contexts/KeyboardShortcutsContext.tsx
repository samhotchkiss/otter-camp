import { createContext, useContext, useState, useCallback, type ReactNode } from "react";

type KeyboardShortcutsContextValue = {
  // Command palette
  isCommandPaletteOpen: boolean;
  openCommandPalette: () => void;
  closeCommandPalette: () => void;
  
  // Shortcuts help modal
  isShortcutsHelpOpen: boolean;
  openShortcutsHelp: () => void;
  closeShortcutsHelp: () => void;
  
  // Task navigation
  selectedTaskIndex: number;
  setSelectedTaskIndex: (index: number) => void;
  taskCount: number;
  setTaskCount: (count: number) => void;
  
  // Task detail
  selectedTaskId: string | null;
  openTaskDetail: (taskId: string) => void;
  closeTaskDetail: () => void;
  
  // New task
  isNewTaskOpen: boolean;
  openNewTask: () => void;
  closeNewTask: () => void;
};

const KeyboardShortcutsContext = createContext<KeyboardShortcutsContextValue | null>(null);

export function KeyboardShortcutsProvider({ children }: { children: ReactNode }) {
  // Command palette
  const [isCommandPaletteOpen, setIsCommandPaletteOpen] = useState(false);
  
  // Shortcuts help
  const [isShortcutsHelpOpen, setIsShortcutsHelpOpen] = useState(false);
  
  // Task navigation
  const [selectedTaskIndex, setSelectedTaskIndex] = useState(-1);
  const [taskCount, setTaskCount] = useState(0);
  
  // Task detail
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);
  
  // New task
  const [isNewTaskOpen, setIsNewTaskOpen] = useState(false);

  const openCommandPalette = useCallback(() => {
    setIsCommandPaletteOpen(true);
  }, []);

  const closeCommandPalette = useCallback(() => {
    setIsCommandPaletteOpen(false);
  }, []);

  const openShortcutsHelp = useCallback(() => {
    setIsShortcutsHelpOpen(true);
  }, []);

  const closeShortcutsHelp = useCallback(() => {
    setIsShortcutsHelpOpen(false);
  }, []);

  const openTaskDetail = useCallback((taskId: string) => {
    setSelectedTaskId(taskId);
  }, []);

  const closeTaskDetail = useCallback(() => {
    setSelectedTaskId(null);
  }, []);

  const openNewTask = useCallback(() => {
    setIsNewTaskOpen(true);
  }, []);

  const closeNewTask = useCallback(() => {
    setIsNewTaskOpen(false);
  }, []);

  const value: KeyboardShortcutsContextValue = {
    isCommandPaletteOpen,
    openCommandPalette,
    closeCommandPalette,
    isShortcutsHelpOpen,
    openShortcutsHelp,
    closeShortcutsHelp,
    selectedTaskIndex,
    setSelectedTaskIndex,
    taskCount,
    setTaskCount,
    selectedTaskId,
    openTaskDetail,
    closeTaskDetail,
    isNewTaskOpen,
    openNewTask,
    closeNewTask,
  };

  return (
    <KeyboardShortcutsContext.Provider value={value}>
      {children}
    </KeyboardShortcutsContext.Provider>
  );
}

export function useKeyboardShortcutsContext() {
  const context = useContext(KeyboardShortcutsContext);
  if (!context) {
    throw new Error("useKeyboardShortcutsContext must be used within KeyboardShortcutsProvider");
  }
  return context;
}
