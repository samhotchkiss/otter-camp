import { useState, useCallback, useMemo, memo, useEffect } from "react";
import {
  DndContext,
  DragOverlay,
  closestCorners,
  KeyboardSensor,
  PointerSensor,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragOverEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useKeyboardShortcutsContext } from "../../contexts/KeyboardShortcutsContext";
import { useLiveRegion } from "../LiveRegion";

export type TaskStatus = "backlog" | "in-progress" | "review" | "done";
export type TaskPriority = "low" | "medium" | "high" | "critical";

export type TaskAssignee = {
  id: string;
  name: string;
  avatarUrl?: string;
};

export interface Task {
  id: string;
  title: string;
  status: TaskStatus;
  assignee?: TaskAssignee | null;
  priority?: TaskPriority;
  createdAt: string;
}

interface Column {
  id: TaskStatus;
  title: string;
  emoji: string;
}

const COLUMNS: Column[] = [
  { id: "backlog", title: "Backlog", emoji: "🗂️" },
  { id: "in-progress", title: "In Progress", emoji: "🚀" },
  { id: "review", title: "Review", emoji: "👀" },
  { id: "done", title: "Done", emoji: "✅" },
];

const PRIORITY_COLORS: Record<TaskPriority, string> = {
  low: "bg-slate-100 text-slate-600 dark:bg-slate-700 dark:text-slate-300",
  medium: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300",
  high: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300",
  critical:
    "bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-300",
};

const STATUS_TO_API: Record<TaskStatus, string> = {
  backlog: "queued",
  "in-progress": "in_progress",
  review: "review",
  done: "done",
};

const INITIAL_TASKS: Task[] = [
  {
    id: "task-1",
    title: "Collect requirements for tasks board",
    status: "backlog",
    priority: "high",
    assignee: { id: "otter-1", name: "🦦 Scout Otter" },
    createdAt: new Date().toISOString(),
  },
  {
    id: "task-2",
    title: "Implement Kanban columns + cards",
    status: "in-progress",
    priority: "medium",
    assignee: { id: "otter-2", name: "🦦 Builder Otter" },
    createdAt: new Date().toISOString(),
  },
  {
    id: "task-3",
    title: "Review responsive layout on mobile",
    status: "review",
    priority: "low",
    assignee: { id: "otter-3", name: "🦦 Lead Otter" },
    createdAt: new Date().toISOString(),
  },
  {
    id: "task-4",
    title: "Ship Kanban board component",
    status: "done",
    priority: "critical",
    assignee: { id: "otter-2", name: "🦦 Builder Otter" },
    createdAt: new Date().toISOString(),
  },
];

async function updateTaskStatus(taskId: string, newStatus: TaskStatus): Promise<void> {
  const apiUrl = `/api/tasks/${taskId}/status`;
  const apiStatus = STATUS_TO_API[newStatus];

  try {
    const response = await fetch(apiUrl, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status: apiStatus }),
    });

    if (!response.ok) {
      throw new Error(`Failed to update task: ${response.statusText}`);
    }
  } catch (error) {
    console.warn("API call failed (expected in dev):", error);
  }
}

function getInitials(name: string) {
  const parts = name
    .replace(/[^\p{L}\p{N}\s]/gu, "")
    .split(/\s+/)
    .filter(Boolean);
  const first = parts[0]?.[0] ?? "?";
  const last = parts.length > 1 ? parts[parts.length - 1][0] : "";
  return (first + last).toUpperCase();
}

function AssigneePill({ assignee }: { assignee?: TaskAssignee | null }) {
  if (!assignee) {
    return (
      <span className="text-xs text-slate-400 dark:text-slate-500">
        Unassigned
      </span>
    );
  }

  if (assignee.avatarUrl) {
    return (
      <span className="flex min-w-0 items-center gap-2">
        <img
          src={assignee.avatarUrl}
          alt={assignee.name}
          loading="lazy"
          decoding="async"
          className="h-6 w-6 rounded-full object-cover ring-2 ring-white dark:ring-slate-800"
        />
        <span className="min-w-0 truncate text-xs text-slate-600 dark:text-slate-300">
          {assignee.name}
        </span>
      </span>
    );
  }

  return (
    <span className="flex min-w-0 items-center gap-2">
      <span className="flex h-6 w-6 items-center justify-center rounded-full bg-gradient-to-br from-sky-400 to-emerald-400 text-[10px] font-semibold text-white ring-2 ring-white dark:ring-slate-800">
        {getInitials(assignee.name)}
      </span>
      <span className="min-w-0 truncate text-xs text-slate-600 dark:text-slate-300">
        {assignee.name}
      </span>
    </span>
  );
}

interface TaskCardProps {
  task: Task;
  isDragging?: boolean;
  isSelected?: boolean;
  onOpen?: (taskId: string) => void;
}

const TaskCard = memo(function TaskCard({
  task,
  isDragging,
  isSelected,
  onOpen,
}: TaskCardProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging: isSortableDragging,
  } = useSortable({ id: task.id });

  const style = useMemo(
    () => ({
      transform: CSS.Transform.toString(transform),
      transition,
    }),
    [transform, transition]
  );

  const isBeingDragged = isDragging || isSortableDragging;

  const className = useMemo(
    () =>
      `
        group cursor-grab rounded-lg border bg-white p-4 shadow-sm
        transition-all duration-200
        hover:border-sky-300 hover:shadow-md
        active:cursor-grabbing
        dark:bg-slate-800
        ${
          isBeingDragged
            ? "opacity-50 shadow-lg ring-2 ring-sky-400 border-slate-200 dark:border-slate-700"
            : ""
        }
        ${
          isSelected && !isBeingDragged
            ? "border-sky-500 ring-2 ring-sky-500/30 dark:border-sky-400"
            : "border-slate-200 dark:border-slate-700"
        }
      `.trim(),
    [isBeingDragged, isSelected]
  );

  const handleOpen = () => onOpen?.(task.id);

  return (
    <article
      ref={setNodeRef}
      style={style}
      {...attributes}
      {...listeners}
      role="listitem"
      aria-label={[
        `Task: ${task.title}`,
        task.assignee?.name ? `Assignee: ${task.assignee.name}` : "Unassigned",
        task.priority ? `Priority: ${task.priority}` : null,
      ]
        .filter(Boolean)
        .join(", ")}
      aria-grabbed={isBeingDragged}
      className={className}
      onDoubleClick={handleOpen}
      onKeyDown={(e) => {
        if (e.key === "Enter") handleOpen();
      }}
    >
      <div className="flex items-start gap-3">
        <h4 className="min-w-0 flex-1 font-medium text-slate-900 dark:text-white">
          <span className="block truncate">{task.title}</span>
        </h4>
        {task.priority ? (
          <span
            className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${PRIORITY_COLORS[task.priority]}`}
            aria-label={`Priority: ${task.priority}`}
          >
            {task.priority}
          </span>
        ) : null}
      </div>

      <div className="mt-3 flex items-center justify-between">
        <AssigneePill assignee={task.assignee} />
      </div>
    </article>
  );
});

const DragOverlayCard = memo(function DragOverlayCard({ task }: { task: Task }) {
  return (
    <div className="cursor-grabbing rounded-lg border border-sky-400 bg-white p-4 shadow-xl ring-2 ring-sky-400/50 dark:border-sky-500 dark:bg-slate-800">
      <div className="flex items-start gap-3">
        <h4 className="min-w-0 flex-1 font-medium text-slate-900 dark:text-white">
          <span className="block truncate">{task.title}</span>
        </h4>
        {task.priority ? (
          <span
            className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${PRIORITY_COLORS[task.priority]}`}
          >
            {task.priority}
          </span>
        ) : null}
      </div>
      <div className="mt-3">
        <AssigneePill assignee={task.assignee} />
      </div>
    </div>
  );
});

interface KanbanColumnProps {
  column: Column;
  tasks: Task[];
  isOver?: boolean;
  selectedTaskId?: string | null;
  onOpenTask?: (taskId: string) => void;
}

const KanbanColumn = memo(function KanbanColumn({
  column,
  tasks,
  isOver,
  selectedTaskId,
  onOpenTask,
}: KanbanColumnProps) {
  const taskIds = useMemo(() => tasks.map((t) => t.id), [tasks]);
  const { setNodeRef } = useDroppable({ id: column.id });

  const containerClassName = useMemo(
    () =>
      `
        flex min-h-[420px] min-w-0 flex-col rounded-xl border bg-slate-50 p-4
        transition-colors duration-200
        dark:border-slate-700 dark:bg-slate-900/50
        ${isOver ? "border-sky-400 bg-sky-50 dark:bg-sky-900/20" : "border-slate-200"}
      `.trim(),
    [isOver]
  );

  const emptyClassName = useMemo(
    () =>
      `
        flex flex-1 items-center justify-center rounded-lg border-2 border-dashed
        text-sm text-slate-400
        transition-colors duration-200
        dark:border-slate-700 dark:text-slate-500
        ${
          isOver
            ? "border-sky-400 bg-sky-100/50 text-sky-600 dark:bg-sky-900/30 dark:text-sky-400"
            : "border-slate-300"
        }
      `.trim(),
    [isOver]
  );

  return (
    <section
      ref={setNodeRef}
      aria-labelledby={`column-${column.id}-heading`}
      aria-describedby={`column-${column.id}-count`}
      className={containerClassName}
    >
      <div className="mb-4 flex items-center gap-2">
        <span className="text-xl" aria-hidden="true">
          {column.emoji}
        </span>
        <h3
          id={`column-${column.id}-heading`}
          className="font-semibold text-slate-800 dark:text-slate-100"
        >
          {column.title}
        </h3>
        <span
          id={`column-${column.id}-count`}
          className="ml-auto rounded-full bg-slate-200 px-2 py-0.5 text-xs font-medium text-slate-600 dark:bg-slate-700 dark:text-slate-300"
          aria-label={`${tasks.length} ${tasks.length === 1 ? "task" : "tasks"}`}
        >
          {tasks.length}
        </span>
      </div>

      <SortableContext items={taskIds} strategy={verticalListSortingStrategy}>
        <div
          role="list"
          aria-label={`${column.title} tasks`}
          className="flex flex-1 flex-col gap-3 overflow-y-auto pr-1"
        >
          {tasks.map((task) => (
            <TaskCard
              key={task.id}
              task={task}
              isSelected={task.id === selectedTaskId}
              onOpen={onOpenTask}
            />
          ))}
          {tasks.length === 0 ? (
            <div className={emptyClassName} role="status">
              {isOver ? "Drop here!" : "No tasks yet"}
            </div>
          ) : null}
        </div>
      </SortableContext>
    </section>
  );
});

function KanbanBoardComponent() {
  const [tasks, setTasks] = useState<Task[]>(INITIAL_TASKS);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [overColumn, setOverColumn] = useState<TaskStatus | null>(null);

  const { selectedTaskIndex, setTaskCount, openTaskDetail } =
    useKeyboardShortcutsContext();

  const { announce } = useLiveRegion();

  const allTaskIds = useMemo(() => tasks.map((task) => task.id), [tasks]);

  useEffect(() => {
    setTaskCount(allTaskIds.length);
  }, [allTaskIds.length, setTaskCount]);

  const selectedTaskId = useMemo(() => {
    if (selectedTaskIndex < 0 || selectedTaskIndex >= allTaskIds.length) {
      return null;
    }
    return allTaskIds[selectedTaskIndex];
  }, [allTaskIds, selectedTaskIndex]);

  useEffect(() => {
    const handleOpenTask = () => {
      if (selectedTaskId) openTaskDetail(selectedTaskId);
    };

    const handleSetPriority = (event: CustomEvent<string>) => {
      if (!selectedTaskId) return;
      const priority = event.detail as TaskPriority;
      setTasks((prev) =>
        prev.map((task) =>
          task.id === selectedTaskId ? { ...task, priority } : task
        )
      );
    };

    window.addEventListener("keyboard:open-task", handleOpenTask);
    window.addEventListener(
      "keyboard:set-priority",
      handleSetPriority as EventListener
    );

    return () => {
      window.removeEventListener("keyboard:open-task", handleOpenTask);
      window.removeEventListener(
        "keyboard:set-priority",
        handleSetPriority as EventListener
      );
    };
  }, [openTaskDetail, selectedTaskId]);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  );

  const taskMap = useMemo(() => {
    const map = new Map<string, Task>();
    tasks.forEach((task) => map.set(task.id, task));
    return map;
  }, [tasks]);

  const tasksByStatus = useMemo(
    () => ({
      backlog: tasks.filter((task) => task.status === "backlog"),
      "in-progress": tasks.filter((task) => task.status === "in-progress"),
      review: tasks.filter((task) => task.status === "review"),
      done: tasks.filter((task) => task.status === "done"),
    }),
    [tasks]
  );

  const findColumnByTaskId = useCallback(
    (taskId: string): TaskStatus | null => {
      const task = taskMap.get(taskId);
      return task?.status ?? null;
    },
    [taskMap]
  );

  const handleDragStart = useCallback(
    (event: DragStartEvent) => {
      const task = taskMap.get(event.active.id as string);
      if (task) setActiveTask(task);
    },
    [taskMap]
  );

  const handleDragOver = useCallback(
    (event: DragOverEvent) => {
      const { over } = event;
      if (!over) {
        setOverColumn(null);
        return;
      }

      const overId = over.id as string;
      const taskColumn = findColumnByTaskId(overId);
      if (taskColumn) {
        setOverColumn(taskColumn);
        return;
      }

      if (COLUMNS.some((col) => col.id === overId)) {
        setOverColumn(overId as TaskStatus);
      }
    },
    [findColumnByTaskId]
  );

  const handleDragEnd = useCallback(
    async (event: DragEndEvent) => {
      const { active, over } = event;

      setActiveTask(null);
      setOverColumn(null);

      if (!over) return;

      const activeId = active.id as string;
      const overId = over.id as string;

      let targetStatus: TaskStatus | null = null;

      if (COLUMNS.some((col) => col.id === overId)) {
        targetStatus = overId as TaskStatus;
      } else {
        targetStatus = findColumnByTaskId(overId);
      }

      if (!targetStatus) return;

      const draggedTask = taskMap.get(activeId);
      if (!draggedTask || draggedTask.status === targetStatus) return;

      const targetColumn = COLUMNS.find((col) => col.id === targetStatus);

      setTasks((prev) =>
        prev.map((task) =>
          task.id === activeId ? { ...task, status: targetStatus! } : task
        )
      );

      announce(
        `Task "${draggedTask.title}" moved to ${targetColumn?.title || targetStatus}`
      );

      await updateTaskStatus(activeId, targetStatus);
    },
    [announce, findColumnByTaskId, taskMap]
  );

  const handleOpenTask = useCallback(
    (taskId: string) => openTaskDetail(taskId),
    [openTaskDetail]
  );

  return (
    <div
      className="w-full p-4"
      role="region"
      aria-label="Kanban task board"
    >
      <DndContext
        sensors={sensors}
        collisionDetection={closestCorners}
        onDragStart={handleDragStart}
        onDragOver={handleDragOver}
        onDragEnd={handleDragEnd}
      >
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {COLUMNS.map((column) => (
            <KanbanColumn
              key={column.id}
              column={column}
              tasks={tasksByStatus[column.id]}
              isOver={overColumn === column.id}
              selectedTaskId={selectedTaskId}
              onOpenTask={handleOpenTask}
            />
          ))}
        </div>

        <DragOverlay>
          {activeTask ? <DragOverlayCard task={activeTask} /> : null}
        </DragOverlay>
      </DndContext>
    </div>
  );
}

const KanbanBoard = memo(KanbanBoardComponent);

export default KanbanBoard;

