import { useState, useEffect, useRef, useCallback } from "react";
import DashboardLayout from "../layouts/DashboardLayout";

/**
 * Team member type for avatars.
 */
interface TeamMember {
  id: string;
  name: string;
  avatarUrl?: string;
}

/**
 * Project data type.
 */
export interface Project {
  id: string;
  name: string;
  description: string;
  taskCount: number;
  completedTasks: number;
  teamMembers: TeamMember[];
  color: string;
  archived?: boolean;
  createdAt: string;
}

/**
 * Props for ProjectsPage.
 */
export interface ProjectsPageProps {
  apiEndpoint?: string;
  onFilterKanban?: (projectId: string | null) => void;
}

// Sample data for demo
const SAMPLE_PROJECTS: Project[] = [
  {
    id: "proj-1",
    name: "Camp Setup",
    description: "Initial camp infrastructure and perimeter setup for the expedition team",
    taskCount: 12,
    completedTasks: 8,
    teamMembers: [
      { id: "m1", name: "Scout Otter" },
      { id: "m2", name: "Builder Otter" },
      { id: "m3", name: "Lead Otter" },
    ],
    color: "sky",
    createdAt: new Date().toISOString(),
  },
  {
    id: "proj-2",
    name: "Expedition Planning",
    description: "Route mapping and resource allocation for the upcoming river expedition",
    taskCount: 8,
    completedTasks: 3,
    teamMembers: [
      { id: "m1", name: "Scout Otter" },
      { id: "m4", name: "Navigator Otter" },
    ],
    color: "emerald",
    createdAt: new Date().toISOString(),
  },
  {
    id: "proj-3",
    name: "Supply Management",
    description: "Inventory tracking and restocking for all camp supplies and equipment",
    taskCount: 15,
    completedTasks: 15,
    teamMembers: [
      { id: "m2", name: "Builder Otter" },
      { id: "m5", name: "Quartermaster Otter" },
    ],
    color: "amber",
    createdAt: new Date().toISOString(),
  },
  {
    id: "proj-4",
    name: "Training Program",
    description: "Onboarding and skill development for new camp members",
    taskCount: 6,
    completedTasks: 2,
    teamMembers: [
      { id: "m3", name: "Lead Otter" },
      { id: "m6", name: "Trainer Otter" },
      { id: "m7", name: "Rookie Otter" },
      { id: "m8", name: "Cadet Otter" },
    ],
    color: "violet",
    createdAt: new Date().toISOString(),
  },
];

const COLOR_VARIANTS: Record<string, { bg: string; border: string; text: string; progress: string }> = {
  sky: {
    bg: "bg-sky-500/10 dark:bg-sky-500/20",
    border: "border-sky-200 dark:border-sky-800",
    text: "text-sky-700 dark:text-sky-300",
    progress: "bg-sky-500",
  },
  emerald: {
    bg: "bg-emerald-500/10 dark:bg-emerald-500/20",
    border: "border-emerald-200 dark:border-emerald-800",
    text: "text-emerald-700 dark:text-emerald-300",
    progress: "bg-emerald-500",
  },
  amber: {
    bg: "bg-amber-500/10 dark:bg-amber-500/20",
    border: "border-amber-200 dark:border-amber-800",
    text: "text-amber-700 dark:text-amber-300",
    progress: "bg-amber-500",
  },
  violet: {
    bg: "bg-violet-500/10 dark:bg-violet-500/20",
    border: "border-violet-200 dark:border-violet-800",
    text: "text-violet-700 dark:text-violet-300",
    progress: "bg-violet-500",
  },
  rose: {
    bg: "bg-rose-500/10 dark:bg-rose-500/20",
    border: "border-rose-200 dark:border-rose-800",
    text: "text-rose-700 dark:text-rose-300",
    progress: "bg-rose-500",
  },
  slate: {
    bg: "bg-slate-500/10 dark:bg-slate-500/20",
    border: "border-slate-200 dark:border-slate-700",
    text: "text-slate-700 dark:text-slate-300",
    progress: "bg-slate-500",
  },
};

/**
 * Avatar stack component showing team members.
 */
function TeamAvatars({ members, maxVisible = 3 }: { members: TeamMember[]; maxVisible?: number }) {
  const visibleMembers = members.slice(0, maxVisible);
  const overflow = members.length - maxVisible;

  const getInitials = (name: string) => {
    return name
      .split(" ")
      .map((n) => n[0])
      .join("")
      .toUpperCase()
      .slice(0, 2);
  };

  const colors = [
    "from-sky-400 to-sky-600",
    "from-emerald-400 to-emerald-600",
    "from-amber-400 to-amber-600",
    "from-violet-400 to-violet-600",
    "from-rose-400 to-rose-600",
  ];

  return (
    <div className="flex -space-x-2">
      {visibleMembers.map((member, index) => (
        <div
          key={member.id}
          className={`relative flex h-8 w-8 items-center justify-center rounded-full border-2 border-white bg-gradient-to-br text-xs font-semibold text-white dark:border-slate-800 ${colors[index % colors.length]}`}
          title={member.name}
        >
          {member.avatarUrl ? (
            <img
              src={member.avatarUrl}
              alt={member.name}
              className="h-full w-full rounded-full object-cover"
            />
          ) : (
            getInitials(member.name)
          )}
        </div>
      ))}
      {overflow > 0 && (
        <div className="relative flex h-8 w-8 items-center justify-center rounded-full border-2 border-white bg-slate-200 text-xs font-semibold text-slate-600 dark:border-slate-800 dark:bg-slate-700 dark:text-slate-300">
          +{overflow}
        </div>
      )}
    </div>
  );
}

/**
 * Progress bar component.
 */
function ProgressBar({
  completed,
  total,
  colorClass,
}: {
  completed: number;
  total: number;
  colorClass: string;
}) {
  const percentage = total > 0 ? Math.round((completed / total) * 100) : 0;

  return (
    <div className="flex items-center gap-3">
      <div className="h-2 flex-1 overflow-hidden rounded-full bg-slate-200 dark:bg-slate-700">
        <div
          className={`h-full rounded-full transition-all duration-500 ${colorClass}`}
          style={{ width: `${percentage}%` }}
        />
      </div>
      <span className="min-w-[3rem] text-right text-xs font-medium text-slate-500 dark:text-slate-400">
        {percentage}%
      </span>
    </div>
  );
}

/**
 * Project settings dropdown menu.
 */
function ProjectSettingsDropdown({
  project,
  onRename,
  onArchive,
  onDelete,
}: {
  project: Project;
  onRename: (project: Project) => void;
  onArchive: (project: Project) => void;
  onDelete: (project: Project) => void;
}) {
  const [isOpen, setIsOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  // Close dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    };

    if (isOpen) {
      document.addEventListener("mousedown", handleClickOutside);
      return () => document.removeEventListener("mousedown", handleClickOutside);
    }
  }, [isOpen]);

  const handleAction = (action: () => void) => (e: React.MouseEvent) => {
    e.stopPropagation();
    setIsOpen(false);
    action();
  };

  return (
    <div className="relative" ref={dropdownRef}>
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          setIsOpen(!isOpen);
        }}
        className="rounded-lg p-1.5 text-slate-400 opacity-0 transition group-hover:opacity-100 hover:bg-slate-200 hover:text-slate-600 dark:hover:bg-slate-700 dark:hover:text-slate-300"
        aria-label="Project settings"
      >
        <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={2}
            d="M12 5v.01M12 12v.01M12 19v.01M12 6a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2z"
          />
        </svg>
      </button>

      {isOpen && (
        <div className="absolute right-0 top-full z-20 mt-1 w-48 overflow-hidden rounded-xl border border-slate-200 bg-white shadow-lg dark:border-slate-700 dark:bg-slate-800">
          <button
            type="button"
            onClick={handleAction(() => onRename(project))}
            className="flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm text-slate-700 transition hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"
              />
            </svg>
            Rename
          </button>
          <button
            type="button"
            onClick={handleAction(() => onArchive(project))}
            className="flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm text-slate-700 transition hover:bg-slate-100 dark:text-slate-200 dark:hover:bg-slate-700"
          >
            <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M5 8h14M5 8a2 2 0 110-4h14a2 2 0 110 4M5 8v10a2 2 0 002 2h10a2 2 0 002-2V8m-9 4h4"
              />
            </svg>
            {project.archived ? "Unarchive" : "Archive"}
          </button>
          <div className="border-t border-slate-200 dark:border-slate-700" />
          <button
            type="button"
            onClick={handleAction(() => onDelete(project))}
            className="flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm text-red-600 transition hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20"
          >
            <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
              />
            </svg>
            Delete
          </button>
        </div>
      )}
    </div>
  );
}

/**
 * Project card component.
 */
function ProjectCard({
  project,
  onClick,
  onRename,
  onArchive,
  onDelete,
}: {
  project: Project;
  onClick: (project: Project) => void;
  onRename: (project: Project) => void;
  onArchive: (project: Project) => void;
  onDelete: (project: Project) => void;
}) {
  const colorVariant = COLOR_VARIANTS[project.color] || COLOR_VARIANTS.slate;

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={() => onClick(project)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onClick(project);
        }
      }}
      className={`group relative cursor-pointer rounded-2xl border bg-white p-5 shadow-sm transition-all duration-200 hover:shadow-md hover:ring-2 hover:ring-sky-400/50 dark:bg-slate-800/80 ${colorVariant.border} ${project.archived ? "opacity-60" : ""}`}
    >
      {/* Header */}
      <div className="mb-3 flex items-start justify-between">
        <div className="flex items-center gap-3">
          <div
            className={`flex h-10 w-10 items-center justify-center rounded-xl ${colorVariant.bg}`}
          >
            <span className="text-lg">📁</span>
          </div>
          <div>
            <h3 className="font-semibold text-slate-900 dark:text-white">{project.name}</h3>
            {project.archived && (
              <span className="text-xs text-slate-500 dark:text-slate-400">Archived</span>
            )}
          </div>
        </div>
        <ProjectSettingsDropdown
          project={project}
          onRename={onRename}
          onArchive={onArchive}
          onDelete={onDelete}
        />
      </div>

      {/* Description */}
      <p className="mb-4 line-clamp-2 text-sm text-slate-600 dark:text-slate-400">
        {project.description}
      </p>

      {/* Progress */}
      <div className="mb-4">
        <div className="mb-1.5 flex items-center justify-between text-sm">
          <span className="text-slate-500 dark:text-slate-400">Progress</span>
          <span className={`font-medium ${colorVariant.text}`}>
            {project.completedTasks}/{project.taskCount} tasks
          </span>
        </div>
        <ProgressBar
          completed={project.completedTasks}
          total={project.taskCount}
          colorClass={colorVariant.progress}
        />
      </div>

      {/* Footer */}
      <div className="flex items-center justify-between">
        <TeamAvatars members={project.teamMembers} />
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onClick(project);
          }}
          className={`rounded-lg px-3 py-1.5 text-xs font-medium transition ${colorVariant.bg} ${colorVariant.text} hover:opacity-80`}
        >
          View Board →
        </button>
      </div>
    </div>
  );
}

/**
 * Create project modal form.
 */
function CreateProjectModal({
  isOpen,
  onClose,
  onCreate,
}: {
  isOpen: boolean;
  onClose: () => void;
  onCreate: (project: Omit<Project, "id" | "createdAt" | "taskCount" | "completedTasks">) => void;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [color, setColor] = useState("sky");
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus input when modal opens
  useEffect(() => {
    if (isOpen) {
      setTimeout(() => inputRef.current?.focus(), 100);
    }
  }, [isOpen]);

  // Handle escape key
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && isOpen) {
        onClose();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, onClose]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;

    onCreate({
      name: name.trim(),
      description: description.trim(),
      color,
      teamMembers: [],
      archived: false,
    });

    setName("");
    setDescription("");
    setColor("sky");
    onClose();
  };

  if (!isOpen) return null;

  const colors = ["sky", "emerald", "amber", "violet", "rose", "slate"];

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      role="dialog"
      aria-modal="true"
      aria-labelledby="create-project-title"
    >
      <div className="w-full max-w-md overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-800">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-slate-200 px-6 py-4 dark:border-slate-700">
          <h2
            id="create-project-title"
            className="text-lg font-semibold text-slate-900 dark:text-white"
          >
            Create New Project
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg p-1.5 text-slate-400 transition hover:bg-slate-100 hover:text-slate-600 dark:hover:bg-slate-700 dark:hover:text-slate-300"
            aria-label="Close"
          >
            <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M6 18L18 6M6 6l12 12"
              />
            </svg>
          </button>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="p-6">
          <div className="space-y-4">
            {/* Name */}
            <div>
              <label
                htmlFor="project-name"
                className="mb-1.5 block text-sm font-medium text-slate-700 dark:text-slate-300"
              >
                Project Name
              </label>
              <input
                ref={inputRef}
                id="project-name"
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g., River Expedition"
                className="w-full rounded-xl border border-slate-300 bg-white px-4 py-2.5 text-slate-900 placeholder-slate-400 transition focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-600 dark:bg-slate-900 dark:text-white dark:placeholder-slate-500"
                required
              />
            </div>

            {/* Description */}
            <div>
              <label
                htmlFor="project-description"
                className="mb-1.5 block text-sm font-medium text-slate-700 dark:text-slate-300"
              >
                Description
              </label>
              <textarea
                id="project-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="What's this project about?"
                rows={3}
                className="w-full resize-none rounded-xl border border-slate-300 bg-white px-4 py-2.5 text-slate-900 placeholder-slate-400 transition focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-600 dark:bg-slate-900 dark:text-white dark:placeholder-slate-500"
              />
            </div>

            {/* Color */}
            <div>
              <label className="mb-2 block text-sm font-medium text-slate-700 dark:text-slate-300">
                Color
              </label>
              <div className="flex flex-wrap gap-2">
                {colors.map((c) => {
                  const variant = COLOR_VARIANTS[c];
                  return (
                    <button
                      key={c}
                      type="button"
                      onClick={() => setColor(c)}
                      className={`h-8 w-8 rounded-full transition ${variant.progress} ${
                        color === c
                          ? "ring-2 ring-offset-2 ring-offset-white dark:ring-offset-slate-800"
                          : "opacity-60 hover:opacity-100"
                      }`}
                      aria-label={`Select ${c} color`}
                      style={color === c ? { ringColor: variant.progress } : undefined}
                    />
                  );
                })}
              </div>
            </div>
          </div>

          {/* Actions */}
          <div className="mt-6 flex gap-3">
            <button
              type="button"
              onClick={onClose}
              className="flex-1 rounded-xl border border-slate-300 px-4 py-2.5 text-sm font-medium text-slate-700 transition hover:bg-slate-100 dark:border-slate-600 dark:text-slate-300 dark:hover:bg-slate-700"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!name.trim()}
              className="flex-1 rounded-xl bg-sky-500 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-sky-600 disabled:cursor-not-allowed disabled:opacity-50"
            >
              Create Project
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

/**
 * Rename project modal.
 */
function RenameProjectModal({
  project,
  isOpen,
  onClose,
  onRename,
}: {
  project: Project | null;
  isOpen: boolean;
  onClose: () => void;
  onRename: (projectId: string, newName: string) => void;
}) {
  const [name, setName] = useState(project?.name || "");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (project) {
      setName(project.name);
    }
  }, [project]);

  useEffect(() => {
    if (isOpen) {
      setTimeout(() => {
        inputRef.current?.focus();
        inputRef.current?.select();
      }, 100);
    }
  }, [isOpen]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || !project) return;
    onRename(project.id, name.trim());
    onClose();
  };

  if (!isOpen || !project) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      role="dialog"
      aria-modal="true"
    >
      <div className="w-full max-w-sm overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-800">
        <form onSubmit={handleSubmit} className="p-6">
          <h2 className="mb-4 text-lg font-semibold text-slate-900 dark:text-white">
            Rename Project
          </h2>
          <input
            ref={inputRef}
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="mb-4 w-full rounded-xl border border-slate-300 bg-white px-4 py-2.5 text-slate-900 transition focus:border-sky-500 focus:outline-none focus:ring-2 focus:ring-sky-500/20 dark:border-slate-600 dark:bg-slate-900 dark:text-white"
            required
          />
          <div className="flex gap-3">
            <button
              type="button"
              onClick={onClose}
              className="flex-1 rounded-xl border border-slate-300 px-4 py-2.5 text-sm font-medium text-slate-700 transition hover:bg-slate-100 dark:border-slate-600 dark:text-slate-300 dark:hover:bg-slate-700"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!name.trim()}
              className="flex-1 rounded-xl bg-sky-500 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-sky-600 disabled:opacity-50"
            >
              Save
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

/**
 * Delete confirmation modal.
 */
function DeleteProjectModal({
  project,
  isOpen,
  onClose,
  onDelete,
}: {
  project: Project | null;
  isOpen: boolean;
  onClose: () => void;
  onDelete: (projectId: string) => void;
}) {
  if (!isOpen || !project) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      role="dialog"
      aria-modal="true"
    >
      <div className="w-full max-w-sm overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-xl dark:border-slate-700 dark:bg-slate-800">
        <div className="p-6">
          <div className="mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-red-100 dark:bg-red-900/30">
            <svg
              className="h-6 w-6 text-red-600 dark:text-red-400"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"
              />
            </svg>
          </div>
          <h2 className="mb-2 text-lg font-semibold text-slate-900 dark:text-white">
            Delete "{project.name}"?
          </h2>
          <p className="mb-6 text-sm text-slate-600 dark:text-slate-400">
            This action cannot be undone. All tasks in this project will be deleted.
          </p>
          <div className="flex gap-3">
            <button
              type="button"
              onClick={onClose}
              className="flex-1 rounded-xl border border-slate-300 px-4 py-2.5 text-sm font-medium text-slate-700 transition hover:bg-slate-100 dark:border-slate-600 dark:text-slate-300 dark:hover:bg-slate-700"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => {
                onDelete(project.id);
                onClose();
              }}
              className="flex-1 rounded-xl bg-red-500 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-red-600"
            >
              Delete
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

/**
 * ProjectsPage - Grid view of all projects with CRUD operations.
 */
export default function ProjectsPage({ onFilterKanban }: ProjectsPageProps) {
  const [projects, setProjects] = useState<Project[]>(SAMPLE_PROJECTS);
  const [isCreateModalOpen, setIsCreateModalOpen] = useState(false);
  const [projectToRename, setProjectToRename] = useState<Project | null>(null);
  const [projectToDelete, setProjectToDelete] = useState<Project | null>(null);
  const [showArchived, setShowArchived] = useState(false);

  // Filter projects
  const filteredProjects = showArchived
    ? projects
    : projects.filter((p) => !p.archived);

  // Active and archived counts
  const activeCount = projects.filter((p) => !p.archived).length;
  const archivedCount = projects.filter((p) => p.archived).length;

  // Handle project card click - filter Kanban
  const handleProjectClick = useCallback(
    (project: Project) => {
      onFilterKanban?.(project.id);
      // In a real app, this would navigate to /tasks?project={project.id}
      console.log(`Filtering Kanban by project: ${project.name}`);
    },
    [onFilterKanban]
  );

  // Create project
  const handleCreateProject = useCallback(
    (projectData: Omit<Project, "id" | "createdAt" | "taskCount" | "completedTasks">) => {
      const newProject: Project = {
        ...projectData,
        id: `proj-${Date.now()}`,
        createdAt: new Date().toISOString(),
        taskCount: 0,
        completedTasks: 0,
      };
      setProjects((prev) => [newProject, ...prev]);
    },
    []
  );

  // Rename project
  const handleRenameProject = useCallback((projectId: string, newName: string) => {
    setProjects((prev) =>
      prev.map((p) => (p.id === projectId ? { ...p, name: newName } : p))
    );
  }, []);

  // Archive/unarchive project
  const handleArchiveProject = useCallback((project: Project) => {
    setProjects((prev) =>
      prev.map((p) => (p.id === project.id ? { ...p, archived: !p.archived } : p))
    );
  }, []);

  // Delete project
  const handleDeleteProject = useCallback((projectId: string) => {
    setProjects((prev) => prev.filter((p) => p.id !== projectId));
  }, []);

  return (
    <DashboardLayout activeNavId="projects">
      <div className="w-full">
        {/* Header */}
        <div className="mb-8 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h1 className="text-2xl font-bold text-slate-900 dark:text-white sm:text-3xl">
              Projects
            </h1>
            <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
              {activeCount} active project{activeCount !== 1 ? "s" : ""}
              {archivedCount > 0 && ` • ${archivedCount} archived`}
            </p>
          </div>

          <div className="flex items-center gap-3">
            {/* Show archived toggle */}
            {archivedCount > 0 && (
              <button
                type="button"
                onClick={() => setShowArchived(!showArchived)}
                className={`rounded-xl border px-4 py-2.5 text-sm font-medium transition ${
                  showArchived
                    ? "border-sky-500 bg-sky-500/10 text-sky-600 dark:text-sky-400"
                    : "border-slate-300 text-slate-600 hover:bg-slate-100 dark:border-slate-600 dark:text-slate-400 dark:hover:bg-slate-700"
                }`}
              >
                {showArchived ? "Hide Archived" : "Show Archived"}
              </button>
            )}

            {/* Create button */}
            <button
              type="button"
              onClick={() => setIsCreateModalOpen(true)}
              className="inline-flex items-center gap-2 rounded-xl bg-sky-500 px-5 py-2.5 text-sm font-medium text-white shadow-sm transition hover:bg-sky-600"
            >
              <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M12 4v16m8-8H4"
                />
              </svg>
              New Project
            </button>
          </div>
        </div>

        {/* Projects grid */}
        {filteredProjects.length === 0 ? (
          <div className="flex min-h-[300px] flex-col items-center justify-center rounded-2xl border-2 border-dashed border-slate-300 bg-slate-50/50 dark:border-slate-700 dark:bg-slate-900/50">
            <div className="mb-4 text-5xl">📁</div>
            <h3 className="mb-2 text-lg font-semibold text-slate-700 dark:text-slate-300">
              No projects yet
            </h3>
            <p className="mb-4 text-sm text-slate-500 dark:text-slate-400">
              Create your first project to get started
            </p>
            <button
              type="button"
              onClick={() => setIsCreateModalOpen(true)}
              className="inline-flex items-center gap-2 rounded-xl bg-sky-500 px-5 py-2.5 text-sm font-medium text-white shadow-sm transition hover:bg-sky-600"
            >
              <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M12 4v16m8-8H4"
                />
              </svg>
              Create Project
            </button>
          </div>
        ) : (
          <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
            {filteredProjects.map((project) => (
              <ProjectCard
                key={project.id}
                project={project}
                onClick={handleProjectClick}
                onRename={setProjectToRename}
                onArchive={handleArchiveProject}
                onDelete={setProjectToDelete}
              />
            ))}
          </div>
        )}
      </div>

      {/* Modals */}
      <CreateProjectModal
        isOpen={isCreateModalOpen}
        onClose={() => setIsCreateModalOpen(false)}
        onCreate={handleCreateProject}
      />
      <RenameProjectModal
        project={projectToRename}
        isOpen={!!projectToRename}
        onClose={() => setProjectToRename(null)}
        onRename={handleRenameProject}
      />
      <DeleteProjectModal
        project={projectToDelete}
        isOpen={!!projectToDelete}
        onClose={() => setProjectToDelete(null)}
        onDelete={handleDeleteProject}
      />
    </DashboardLayout>
  );
}
