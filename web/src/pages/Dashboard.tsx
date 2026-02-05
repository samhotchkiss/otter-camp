import { useState, useMemo, Suspense } from "react";
import CommandPalette from "../components/CommandPalette";
import KanbanBoard from "../components/kanban";
import ActivityPanel from "../components/ActivityPanel";
import ProjectFilters from "../components/ProjectFilters";
import OnboardingTour from "../components/OnboardingTour";
import TaskDetail from "../components/TaskDetail";
import NewTaskModal from "../components/NewTaskModal";
import ErrorBoundary from "../components/ErrorBoundary";
import LoadingSpinner from "../components/LoadingSpinner";
import { SkeletonKanbanColumn } from "../components/Skeleton";
import type { Command } from "../hooks/useCommandPalette";
import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";

// Sample assignees for filters demo
const ASSIGNEES = [
  { value: "otter-1", label: "🦦 Scout Otter" },
  { value: "otter-2", label: "🦦 Builder Otter" },
  { value: "otter-3", label: "🦦 Lead Otter" },
];

// Sample projects for filters demo
const PROJECTS = [
  { value: "camp-setup", label: "Camp Setup" },
  { value: "expedition", label: "Expedition Planning" },
  { value: "supplies", label: "Supply Management" },
];

export default function Dashboard() {
  const [activeView, setActiveView] = useState<"kanban" | "activity">("kanban");
  
  const {
    isCommandPaletteOpen,
    closeCommandPalette,
    selectedTaskId,
    closeTaskDetail,
    isNewTaskOpen,
    closeNewTask,
  } = useKeyboardShortcutsContext();

  const commands = useMemo<Command[]>(
    () => [
      {
        id: "nav-projects",
        label: "Go to Projects",
        category: "Navigation",
        keywords: ["projects", "boards"],
        action: () => window.alert("Projects view coming soon"),
      },
      {
        id: "nav-tasks",
        label: "Go to Tasks",
        category: "Navigation",
        keywords: ["tasks", "todo"],
        action: () => setActiveView("kanban"),
      },
      {
        id: "nav-agents",
        label: "Go to Agents",
        category: "Navigation",
        keywords: ["agents", "ai"],
        action: () => window.alert("Agents view coming soon"),
      },
      {
        id: "nav-feed",
        label: "Go to Feed",
        category: "Navigation",
        keywords: ["feed", "activity"],
        action: () => setActiveView("activity"),
      },
      {
        id: "task-create",
        label: "Create New Task",
        category: "Tasks",
        keywords: ["new", "task", "create"],
        action: () => window.alert("Task creation coming soon"),
      },
      {
        id: "task-search",
        label: "Search Tasks",
        category: "Tasks",
        keywords: ["search", "find"],
        action: () => {
          const searchInput = document.querySelector<HTMLInputElement>(
            'input[placeholder="Search tasks..."]'
          );
          searchInput?.focus();
        },
      },
      {
        id: "agent-scout",
        label: "Summon Scout Agent",
        category: "Agents",
        keywords: ["scout", "research"],
        action: () => window.alert("Scout agent incoming!"),
      },
      {
        id: "agent-builder",
        label: "Summon Builder Agent",
        category: "Agents",
        keywords: ["builder", "ship"],
        action: () => window.alert("Builder agent incoming!"),
      },
      {
        id: "settings-theme",
        label: "Toggle Dark Mode",
        category: "Settings",
        keywords: ["dark", "light", "theme"],
        action: () => document.documentElement.classList.toggle("dark"),
      },
      {
        id: "settings-profile",
        label: "Open Profile Settings",
        category: "Settings",
        keywords: ["profile", "account"],
        action: () => window.alert("Profile settings coming soon"),
      },
    ],
    []
  );

  return (
    <OnboardingTour>
      <div className="space-y-6">
        {/* Page Header */}
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h1 className="text-2xl font-bold text-slate-900 dark:text-white sm:text-3xl">
              Dashboard
            </h1>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              Manage your camp, track tasks, and coordinate with agents.
            </p>
          </div>

          {/* View toggle */}
          <div className="flex rounded-xl border border-slate-200 bg-white p-1 dark:border-slate-700 dark:bg-slate-800">
            <button
              type="button"
              onClick={() => setActiveView("kanban")}
              className={`rounded-lg px-4 py-2 text-sm font-medium transition ${
                activeView === "kanban"
                  ? "bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300"
                  : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-700"
              }`}
            >
              📋 Kanban
            </button>
            <button
              type="button"
              onClick={() => setActiveView("activity")}
              className={`rounded-lg px-4 py-2 text-sm font-medium transition ${
                activeView === "activity"
                  ? "bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300"
                  : "text-slate-600 hover:bg-slate-100 dark:text-slate-300 dark:hover:bg-slate-700"
              }`}
            >
              📡 Activity
            </button>
          </div>
        </div>

        {/* Filters - includes create task button */}
        <div
          className="rounded-2xl border border-slate-200 bg-white/80 p-4 backdrop-blur-sm dark:border-slate-800 dark:bg-slate-900/80 sm:p-6"
          data-tour="create-task"
        >
          <ProjectFilters assignees={ASSIGNEES} projects={PROJECTS} />
        </div>

        {/* Main Content Grid */}
        <div className="grid gap-6 lg:grid-cols-3">
          {/* Primary Content */}
          <div className={activeView === "kanban" ? "lg:col-span-2" : "lg:col-span-3"}>
            <ErrorBoundary errorMessage="Failed to load content">
              {activeView === "kanban" ? (
                <div
                  className="rounded-2xl border border-slate-200 bg-white/80 backdrop-blur-sm dark:border-slate-800 dark:bg-slate-900/80"
                  data-tour="kanban-board"
                >
                  <Suspense
                    fallback={
                      <div className="flex gap-4 overflow-x-auto p-4">
                        <SkeletonKanbanColumn />
                        <SkeletonKanbanColumn />
                        <SkeletonKanbanColumn />
                        <SkeletonKanbanColumn />
                      </div>
                    }
                  >
                    <KanbanBoard />
                  </Suspense>
                </div>
              ) : (
                <Suspense
                  fallback={
                    <div className="flex min-h-[300px] items-center justify-center">
                      <LoadingSpinner size="lg" message="Loading activity..." />
                    </div>
                  }
                >
                  <ActivityPanel className="w-full" />
                </Suspense>
              )}
            </ErrorBoundary>
          </div>

          {/* Sidebar Activity (only in kanban view) */}
          {activeView === "kanban" && (
            <div className="lg:col-span-1" data-tour="agent-chat">
              <ErrorBoundary errorMessage="Failed to load activity">
                <Suspense
                  fallback={
                    <div className="flex min-h-[200px] items-center justify-center rounded-2xl border border-slate-200 bg-white/80 dark:border-slate-800 dark:bg-slate-900/80">
                      <LoadingSpinner size="md" message="Loading..." />
                    </div>
                  }
                >
                  <ActivityPanel className="h-fit" />
                </Suspense>
              </ErrorBoundary>
            </div>
          )}
        </div>
      </div>

      {/* Command Palette */}
      <CommandPalette
        commands={commands}
        isOpen={isCommandPaletteOpen}
        onOpenChange={(open) => !open && closeCommandPalette()}
      />

      {/* Task Detail Slide-over */}
      {selectedTaskId && (
        <TaskDetail
          taskId={selectedTaskId}
          isOpen={!!selectedTaskId}
          onClose={closeTaskDetail}
        />
      )}

      {/* New Task Modal */}
      <NewTaskModal isOpen={isNewTaskOpen} onClose={closeNewTask} />
    </OnboardingTour>
  );
}
