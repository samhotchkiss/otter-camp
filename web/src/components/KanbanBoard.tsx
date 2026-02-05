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
  type DragStartEvent,
  type DragEndEvent,
  type DragOverEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useKeyboardShortcutsContext } from "../contexts/KeyboardShortcutsContext";
import { useLiveRegion } from "./LiveRegion";

// Types
export interface Task {
  id: string;
  title: string;
  description?: string;
  status: TaskStatus;
  priority?: "low" | "medium" | "high";
  createdAt: string;
}

export type TaskStatus = "todo" | "in-progress" | "done";

interface Column {
  id: TaskStatus;
  title: string;
  emoji: string;
}

const COLUMNS: Column[] = [
  { id: "todo", title: "To Do", emoji: "📋" },
  { id: "in-progress", title: "In Progress", emoji: "🚀" },
  { id: "done", title: "Done", emoji: "✅" },
];

// Sample tasks for demo
const INITIAL_TASKS: Task[] = [
  {
    id: "task-1",
    title: "Set up camp perimeter",
    description: "Mark boundaries and secure the area",
    status: "todo",
    priority: "high",
    createdAt: new Date().toISOString(),
  },
  {
    id: "task-2",
    title: "Gather firewood",
    description: "Collect dry wood for the campfire",
    status: "todo",
    priority: "medium",
    createdAt: new Date().toISOString(),
  },
  {
    id: "task-3",
    title: "Check supplies",
    description: "Inventory all camping gear",
    status: "in-progress",
    priority: "medium",
    createdAt: new Date().toISOString(),
  },
  {
    id: "task-4",
    title: "Set up tents",
    description: "Pitch tents and arrange sleeping quarters",
    status: "done",
    priority: "high",
    createdAt: new Date().toISOString(),
  },
];

type TaskId = Task["id"];

type KanbanState = {
  tasksById: Record<TaskId, Task>;
  taskIdsByStatus: Record<TaskStatus, TaskId[]>;
};

type DropIndicator =
  | { type: "task"; taskId: TaskId; position: "above" | "below" }
  | { type: "column"; columnId: TaskStatus }
  | null;

function createInitialKanbanState(tasks: Task[]): KanbanState {
  const tasksById: Record<TaskId, Task> = {};
  const taskIdsByStatus: Record<TaskStatus, TaskId[]> = {
    todo: [],
    "in-progress": [],
    done: [],
  };

  for (const task of tasks) {
    tasksById[task.id] = task;
    taskIdsByStatus[task.status].push(task.id);
  }

  return { tasksById, taskIdsByStatus };
}

function isTaskStatus(value: string): value is TaskStatus {
  return COLUMNS.some((col) => col.id === value);
}

// Priority color mapping - memoized outside component
const PRIORITY_COLORS = {
  low: "bg-slate-100 text-slate-600 dark:bg-slate-700 dark:text-slate-300",
  medium: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300",
  high: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300",
} as const;

// API helper for persistence
async function updateTaskStatus(taskId: string, newStatus: TaskStatus): Promise<void> {
  const apiUrl = `/api/tasks/${taskId}`;
  
  try {
    const response = await fetch(apiUrl, {
      method: "PATCH",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ status: newStatus }),
    });

    if (!response.ok) {
      throw new Error(`Failed to update task: ${response.statusText}`);
    }
  } catch (error) {
    // In development, log but don't fail (API might not exist yet)
    console.warn("API call failed (expected in dev):", error);
  }
}

// Sortable Task Card Component - Memoized for performance
interface TaskCardProps {
  task: Task;
  isDragging?: boolean;
  isSelected?: boolean;
  dropIndicator?: "above" | "below" | null;
}

const TaskCard = memo(function TaskCard({ task, isDragging, isSelected, dropIndicator }: TaskCardProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging: isSortableDragging,
  } = useSortable({ id: task.id });

  const style = useMemo(() => ({
    transform: CSS.Transform.toString(transform),
    transition,
  }), [transform, transition]);

  const isBeingDragged = isDragging || isSortableDragging;

  const dropIndicatorClassName = useMemo(() => {
    if (dropIndicator === "above") {
      return "before:content-[''] before:absolute before:left-0 before:right-0 before:-top-2 before:z-10 before:h-0.5 before:rounded-full before:bg-sky-500";
    }
    if (dropIndicator === "below") {
      return "after:content-[''] after:absolute after:left-0 after:right-0 after:-bottom-2 after:z-10 after:h-0.5 after:rounded-full after:bg-sky-500";
    }
    return "";
  }, [dropIndicator]);

  const className = useMemo(() => `
    group relative cursor-grab rounded-lg border bg-white p-4 shadow-sm
    transition-all duration-200
    hover:border-sky-300 hover:shadow-md
    active:cursor-grabbing
    dark:bg-slate-800
    ${isBeingDragged ? "opacity-50 shadow-lg ring-2 ring-sky-400 border-slate-200 dark:border-slate-700" : ""}
    ${isSelected && !isBeingDragged ? "border-sky-500 ring-2 ring-sky-500/30 dark:border-sky-400" : "border-slate-200 dark:border-slate-700"}
    ${dropIndicatorClassName}
  `.trim(), [isBeingDragged, isSelected, dropIndicatorClassName]);

  return (
    <article
      ref={setNodeRef}
      style={style}
      {...attributes}
      {...listeners}
      role="listitem"
      aria-label={`Task: ${task.title}${task.priority ? `, Priority: ${task.priority}` : ""}`}
      aria-grabbed={isBeingDragged}
      className={className}
    >
      <h4 className="font-medium text-slate-900 dark:text-white">{task.title}</h4>
      {task.description && (
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
          {task.description}
        </p>
      )}
      {task.priority && (
        <span
          className={`mt-2 inline-block rounded-full px-2 py-0.5 text-xs font-medium ${PRIORITY_COLORS[task.priority]}`}
          aria-label={`Priority: ${task.priority}`}
        >
          {task.priority}
        </span>
      )}
    </article>
  );
});

// Overlay card shown while dragging - Memoized
const DragOverlayCard = memo(function DragOverlayCard({ task }: { task: Task }) {
  return (
    <div className="cursor-grabbing rounded-lg border border-sky-400 bg-white p-4 shadow-xl ring-2 ring-sky-400/50 dark:border-sky-500 dark:bg-slate-800">
      <h4 className="font-medium text-slate-900 dark:text-white">{task.title}</h4>
      {task.description && (
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
          {task.description}
        </p>
      )}
      {task.priority && (
        <span
          className={`mt-2 inline-block rounded-full px-2 py-0.5 text-xs font-medium ${PRIORITY_COLORS[task.priority]}`}
        >
          {task.priority}
        </span>
      )}
    </div>
  );
});

// Kanban Column Component - Memoized
interface KanbanColumnProps {
  column: Column;
  tasks: Task[];
  isOver?: boolean;
  selectedTaskId?: string | null;
  dropIndicator?: DropIndicator;
}

const KanbanColumn = memo(function KanbanColumn({ column, tasks, isOver, selectedTaskId, dropIndicator }: KanbanColumnProps) {
  const taskIds = useMemo(() => tasks.map((t) => t.id), [tasks]);
  const { setNodeRef } = useDroppable({ id: column.id });

  const taskDropIndicatorId = dropIndicator?.type === "task" ? dropIndicator.taskId : null;
  const taskDropIndicatorPosition = dropIndicator?.type === "task" ? dropIndicator.position : null;
  const showEmptyColumnIndicator = dropIndicator?.type === "column" && dropIndicator.columnId === column.id;

  const containerClassName = useMemo(() => `
    flex min-h-[400px] w-80 flex-col rounded-xl border bg-slate-50 p-4
    transition-colors duration-200
    dark:border-slate-700 dark:bg-slate-900/50
    ${isOver ? "border-sky-400 bg-sky-50 dark:bg-sky-900/20" : "border-slate-200"}
  `.trim(), [isOver]);

  const emptyClassName = useMemo(() => `
    flex flex-1 items-center justify-center rounded-lg border-2 border-dashed
    text-sm text-slate-400
    transition-colors duration-200
    dark:border-slate-700 dark:text-slate-500
    ${isOver ? "border-sky-400 bg-sky-100/50 text-sky-600 dark:bg-sky-900/30 dark:text-sky-400" : "border-slate-300"}
  `.trim(), [isOver]);

  return (
    <section
      ref={setNodeRef}
      aria-labelledby={`column-${column.id}-heading`}
      aria-describedby={`column-${column.id}-count`}
      className={containerClassName}
    >
      <div className="mb-4 flex items-center gap-2">
        <span className="text-xl" aria-hidden="true">{column.emoji}</span>
        <h3 id={`column-${column.id}-heading`} className="font-semibold text-slate-800 dark:text-slate-100">
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
        <div role="list" aria-label={`${column.title} tasks`} className="flex flex-1 flex-col gap-3">
          {tasks.map((task) => (
            <TaskCard
              key={task.id}
              task={task}
              isSelected={task.id === selectedTaskId}
              dropIndicator={
                task.id === taskDropIndicatorId ? taskDropIndicatorPosition : null
              }
            />
          ))}
          {tasks.length === 0 && (
            <div className={emptyClassName} role="status">
              {isOver ? "Drop here!" : "No tasks yet"}
              {showEmptyColumnIndicator && (
                <div
                  aria-hidden="true"
                  className="mt-3 h-0.5 w-full rounded-full bg-sky-500"
                />
              )}
            </div>
          )}
        </div>
      </SortableContext>
    </section>
  );
});

// Main Kanban Board Component - Memoized
function KanbanBoardComponent() {
  const [kanban, setKanban] = useState<KanbanState>(() => createInitialKanbanState(INITIAL_TASKS));
  const [activeTaskId, setActiveTaskId] = useState<TaskId | null>(null);
  const [overColumn, setOverColumn] = useState<TaskStatus | null>(null);
  const [dropIndicator, setDropIndicator] = useState<DropIndicator>(null);

  const {
    selectedTaskIndex,
    setTaskCount,
    openTaskDetail,
  } = useKeyboardShortcutsContext();

  // Screen reader announcements for dynamic content
  const { announce } = useLiveRegion();

  // Create a flat list of all task IDs for keyboard navigation
  const allTaskIds = useMemo(() => {
    return [
      ...kanban.taskIdsByStatus.todo,
      ...kanban.taskIdsByStatus["in-progress"],
      ...kanban.taskIdsByStatus.done,
    ];
  }, [kanban.taskIdsByStatus]);

  // Update task count when tasks change
  useEffect(() => {
    setTaskCount(allTaskIds.length);
  }, [allTaskIds.length, setTaskCount]);

  // Handle keyboard events for task actions
  useEffect(() => {
    const handleOpenTask = () => {
      if (selectedTaskIndex >= 0 && selectedTaskIndex < allTaskIds.length) {
        const taskId = allTaskIds[selectedTaskIndex];
        openTaskDetail(taskId);
      }
    };

    const handleSetPriority = (event: CustomEvent<string>) => {
      if (selectedTaskIndex >= 0 && selectedTaskIndex < allTaskIds.length) {
        const taskId = allTaskIds[selectedTaskIndex];
        const priority = event.detail as Task["priority"];
        setKanban((prev) => ({
          ...prev,
          tasksById: {
            ...prev.tasksById,
            [taskId]: {
              ...prev.tasksById[taskId],
              priority,
            },
          },
        }));
      }
    };

    window.addEventListener("keyboard:open-task", handleOpenTask);
    window.addEventListener("keyboard:set-priority", handleSetPriority as EventListener);

    return () => {
      window.removeEventListener("keyboard:open-task", handleOpenTask);
      window.removeEventListener("keyboard:set-priority", handleSetPriority as EventListener);
    };
  }, [selectedTaskIndex, allTaskIds, openTaskDetail]);

  // Configure sensors for drag detection - memoized
  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: {
        distance: 8,
      },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    })
  );

  // Get tasks by status - memoized
  const tasksByStatus = useMemo(() => ({
    todo: kanban.taskIdsByStatus.todo.map((id) => kanban.tasksById[id]),
    "in-progress": kanban.taskIdsByStatus["in-progress"].map((id) => kanban.tasksById[id]),
    done: kanban.taskIdsByStatus.done.map((id) => kanban.tasksById[id]),
  }), [kanban.taskIdsByStatus, kanban.tasksById]);

  // Find which column a task belongs to - memoized callback
  const findColumnByTaskId = useCallback((taskId: string): TaskStatus | null => {
    const task = kanban.tasksById[taskId];
    return task?.status ?? null;
  }, [kanban.tasksById]);

  // Handle drag start - memoized callback
  const handleDragStart = useCallback((event: DragStartEvent) => {
    const { active } = event;
    const taskId = active.id as TaskId;
    const task = kanban.tasksById[taskId];
    if (task) {
      setActiveTaskId(taskId);
    }
  }, [kanban.tasksById]);

  // Handle drag over (for visual feedback) - memoized callback
  const handleDragOver = useCallback((event: DragOverEvent) => {
    const { active, over } = event;
    
    if (!over) {
      setOverColumn(null);
      setDropIndicator(null);
      return;
    }

    const activeId = active.id as string;
    const overId = over.id as string;
    
    // If hovering over a task, get its column
    const taskColumn = findColumnByTaskId(overId);
    if (taskColumn) {
      setOverColumn(taskColumn);

      if (activeId === overId) {
        setDropIndicator(null);
        return;
      }

      const activeRect = active.rect.current.translated ?? active.rect.current.initial;
      const overRect = over.rect;

      if (!activeRect || !overRect) {
        setDropIndicator({ type: "task", taskId: overId, position: "above" });
        return;
      }

      const activeCenterY = activeRect.top + activeRect.height / 2;
      const overCenterY = overRect.top + overRect.height / 2;

      setDropIndicator({
        type: "task",
        taskId: overId,
        position: activeCenterY < overCenterY ? "above" : "below",
      });
      return;
    }

    // If hovering directly over a column
    if (COLUMNS.some((col) => col.id === overId)) {
      setOverColumn(overId as TaskStatus);
      const columnId = overId as TaskStatus;
      const columnTaskIds = kanban.taskIdsByStatus[columnId];

      if (columnTaskIds.length === 0) {
        setDropIndicator({ type: "column", columnId });
        return;
      }

      const lastTaskId = columnTaskIds[columnTaskIds.length - 1];
      setDropIndicator({ type: "task", taskId: lastTaskId, position: "below" });
    }
  }, [findColumnByTaskId, kanban.taskIdsByStatus]);

  const handleDragCancel = useCallback(() => {
    setActiveTaskId(null);
    setOverColumn(null);
    setDropIndicator(null);
  }, []);

  // Handle drag end - memoized callback
  const handleDragEnd = useCallback(async (event: DragEndEvent) => {
    const { active, over } = event;

    setActiveTaskId(null);
    setOverColumn(null);
    setDropIndicator(null);

    if (!over) return;

    const activeId = active.id as string;
    const overId = over.id as string;

    if (activeId === overId) return;

    const indicator = dropIndicator;
    const draggedTask = kanban.tasksById[activeId];
    if (!draggedTask) return;

    const sourceStatus = draggedTask.status;

    // Find the target column
    let targetStatus: TaskStatus | null = null;

    if (isTaskStatus(overId)) {
      targetStatus = overId as TaskStatus;
    } else {
      targetStatus = findColumnByTaskId(overId);
    }

    if (!targetStatus) return;

    setKanban((prev) => {
      const activeTask = prev.tasksById[activeId];
      if (!activeTask) return prev;

      const fromStatus = activeTask.status;
      const toStatus = targetStatus;

      // Reorder within the same column
      if (fromStatus === toStatus) {
        const ids = prev.taskIdsByStatus[fromStatus];
        const activeIndex = ids.indexOf(activeId);

        if (activeIndex === -1) return prev;

        // Dropped on the column itself - move to end
        if (isTaskStatus(overId)) {
          const nextIds = [...ids];
          nextIds.splice(activeIndex, 1);
          nextIds.push(activeId);
          return {
            ...prev,
            taskIdsByStatus: {
              ...prev.taskIdsByStatus,
              [fromStatus]: nextIds,
            },
          };
        }

        const overTaskId = overId as TaskId;
        if (!ids.includes(overTaskId)) return prev;

        const remainingIds = ids.filter((id) => id !== activeId);
        const overIndex = remainingIds.indexOf(overTaskId);
        if (overIndex === -1) return prev;

        const shouldInsertAfter =
          indicator?.type === "task" &&
          indicator.taskId === overTaskId &&
          indicator.position === "below";

        const insertIndex = shouldInsertAfter ? overIndex + 1 : overIndex;
        const nextIds = [...remainingIds];
        nextIds.splice(insertIndex, 0, activeId);

        return {
          ...prev,
          taskIdsByStatus: {
            ...prev.taskIdsByStatus,
            [fromStatus]: nextIds,
          },
        };
      }

      // Move across columns
      const fromIds = prev.taskIdsByStatus[fromStatus];
      const toIds = prev.taskIdsByStatus[toStatus];

      if (!fromIds.includes(activeId)) return prev;

      const nextFromIds = fromIds.filter((id) => id !== activeId);

      let insertIndex = toIds.length;
      if (!isTaskStatus(overId)) {
        const overIndex = toIds.indexOf(overId as TaskId);
        if (overIndex !== -1) {
          const shouldInsertAfter =
            indicator?.type === "task" &&
            indicator.taskId === (overId as TaskId) &&
            indicator.position === "below";
          insertIndex = shouldInsertAfter ? overIndex + 1 : overIndex;
        }
      }

      const nextToIds = [...toIds];
      nextToIds.splice(insertIndex, 0, activeId);

      return {
        ...prev,
        tasksById: {
          ...prev.tasksById,
          [activeId]: { ...activeTask, status: toStatus },
        },
        taskIdsByStatus: {
          ...prev.taskIdsByStatus,
          [fromStatus]: nextFromIds,
          [toStatus]: nextToIds,
        },
      };
    });

    const targetColumn = COLUMNS.find((col) => col.id === targetStatus);

    if (sourceStatus !== targetStatus) {
      // Announce to screen readers
      announce(`Task "${draggedTask.title}" moved to ${targetColumn?.title || targetStatus}`);

      // Persist to API
      await updateTaskStatus(activeId, targetStatus);
      return;
    }

    announce(`Task "${draggedTask.title}" reordered in ${targetColumn?.title || targetStatus}`);
  }, [dropIndicator, kanban.tasksById, findColumnByTaskId, announce]);

  const activeTask = activeTaskId ? kanban.tasksById[activeTaskId] : null;

  return (
    <div className="w-full overflow-x-auto p-4" role="region" aria-labelledby="kanban-heading">
      <div className="mb-6">
        <h1 id="kanban-heading" className="text-2xl font-bold text-slate-900 dark:text-white">
          <span aria-hidden="true">🦦 </span>Camp Tasks
        </h1>
        <p className="text-sm text-slate-500 dark:text-slate-400">
          Drag and drop tasks between columns to update their status. Use keyboard to navigate.
        </p>
      </div>

      <DndContext
        sensors={sensors}
        collisionDetection={closestCorners}
        onDragStart={handleDragStart}
        onDragOver={handleDragOver}
        onDragEnd={handleDragEnd}
        onDragCancel={handleDragCancel}
      >
        <div className="flex gap-6">
          {COLUMNS.map((column) => (
            <KanbanColumn
              key={column.id}
              column={column}
              tasks={tasksByStatus[column.id]}
              isOver={overColumn === column.id}
              dropIndicator={overColumn === column.id ? dropIndicator : null}
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
