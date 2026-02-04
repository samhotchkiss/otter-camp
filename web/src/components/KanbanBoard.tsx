import { useState, useCallback, useMemo, memo, useEffect } from "react";
import {
  DndContext,
  DragOverlay,
  closestCorners,
  KeyboardSensor,
  PointerSensor,
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
}

const TaskCard = memo(function TaskCard({ task, isDragging }: TaskCardProps) {
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

  const className = useMemo(() => `
    group cursor-grab rounded-lg border border-slate-200 bg-white p-4 shadow-sm
    transition-all duration-200
    hover:border-sky-300 hover:shadow-md
    active:cursor-grabbing
    dark:border-slate-700 dark:bg-slate-800
    ${isBeingDragged ? "opacity-50 shadow-lg ring-2 ring-sky-400" : ""}
  `.trim(), [isBeingDragged]);

  return (
    <div
      ref={setNodeRef}
      style={style}
      {...attributes}
      {...listeners}
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
        >
          {task.priority}
        </span>
      )}
    </div>
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
}

const KanbanColumn = memo(function KanbanColumn({ column, tasks, isOver }: KanbanColumnProps) {
  const taskIds = useMemo(() => tasks.map((t) => t.id), [tasks]);

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
    <div className={containerClassName}>
      <div className="mb-4 flex items-center gap-2">
        <span className="text-xl">{column.emoji}</span>
        <h3 className="font-semibold text-slate-800 dark:text-slate-100">
          {column.title}
        </h3>
        <span className="ml-auto rounded-full bg-slate-200 px-2 py-0.5 text-xs font-medium text-slate-600 dark:bg-slate-700 dark:text-slate-300">
          {tasks.length}
        </span>
      </div>

      <SortableContext items={taskIds} strategy={verticalListSortingStrategy}>
        <div className="flex flex-1 flex-col gap-3">
          {tasks.map((task) => (
            <TaskCard key={task.id} task={task} />
          ))}
          {tasks.length === 0 && (
            <div className={emptyClassName}>
              {isOver ? "Drop here!" : "No tasks yet"}
            </div>
          )}
        </div>
      </SortableContext>
    </div>
  );
});

// Main Kanban Board Component - Memoized
function KanbanBoardComponent() {
  const [tasks, setTasks] = useState<Task[]>(INITIAL_TASKS);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [overColumn, setOverColumn] = useState<TaskStatus | null>(null);

  const {
    selectedTaskIndex,
    setTaskCount,
    openTaskDetail,
  } = useKeyboardShortcutsContext();

  // Screen reader announcements for dynamic content
  const { announce } = useLiveRegion();

  // Create a flat list of all task IDs for keyboard navigation
  const allTaskIds = useMemo(() => {
    return tasks.map((task) => task.id);
  }, [tasks]);

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
        setTasks((prev) =>
          prev.map((task) =>
            task.id === taskId ? { ...task, priority } : task
          )
        );
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

  // Memoized task lookups
  const taskMap = useMemo(() => {
    const map = new Map<string, Task>();
    tasks.forEach((task) => map.set(task.id, task));
    return map;
  }, [tasks]);

  // Get tasks by status - memoized
  const tasksByStatus = useMemo(() => ({
    todo: tasks.filter((task) => task.status === "todo"),
    "in-progress": tasks.filter((task) => task.status === "in-progress"),
    done: tasks.filter((task) => task.status === "done"),
  }), [tasks]);

  // Find which column a task belongs to - memoized callback
  const findColumnByTaskId = useCallback((taskId: string): TaskStatus | null => {
    const task = taskMap.get(taskId);
    return task?.status ?? null;
  }, [taskMap]);

  // Handle drag start - memoized callback
  const handleDragStart = useCallback((event: DragStartEvent) => {
    const { active } = event;
    const task = taskMap.get(active.id as string);
    if (task) {
      setActiveTask(task);
    }
  }, [taskMap]);

  // Handle drag over (for visual feedback) - memoized callback
  const handleDragOver = useCallback((event: DragOverEvent) => {
    const { over } = event;
    
    if (!over) {
      setOverColumn(null);
      return;
    }

    const overId = over.id as string;
    
    // If hovering over a task, get its column
    const taskColumn = findColumnByTaskId(overId);
    if (taskColumn) {
      setOverColumn(taskColumn);
      return;
    }

    // If hovering directly over a column
    if (COLUMNS.some((col) => col.id === overId)) {
      setOverColumn(overId as TaskStatus);
    }
  }, [findColumnByTaskId]);

  // Handle drag end - memoized callback
  const handleDragEnd = useCallback(async (event: DragEndEvent) => {
    const { active, over } = event;

    setActiveTask(null);
    setOverColumn(null);

    if (!over) return;

    const activeId = active.id as string;
    const overId = over.id as string;

    // Find the target column
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

    // Update local state optimistically
    setTasks((prev) =>
      prev.map((task) =>
        task.id === activeId ? { ...task, status: targetStatus! } : task
      )
    );

    // Announce to screen readers
    announce(`Task "${draggedTask.title}" moved to ${targetColumn?.title || targetStatus}`);

    // Persist to API
    await updateTaskStatus(activeId, targetStatus);
  }, [findColumnByTaskId, taskMap, announce]);

  return (
    <div className="w-full overflow-x-auto p-4">
      <div className="mb-6">
        <h2 className="text-2xl font-bold text-slate-900 dark:text-white">
          🦦 Camp Tasks
        </h2>
        <p className="text-sm text-slate-500 dark:text-slate-400">
          Drag and drop tasks between columns to update their status
        </p>
      </div>

      <DndContext
        sensors={sensors}
        collisionDetection={closestCorners}
        onDragStart={handleDragStart}
        onDragOver={handleDragOver}
        onDragEnd={handleDragEnd}
      >
        <div className="flex gap-6">
          {COLUMNS.map((column) => (
            <KanbanColumn
              key={column.id}
              column={column}
              tasks={tasksByStatus[column.id]}
              isOver={overColumn === column.id}
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
