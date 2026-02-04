import { useState, useCallback } from "react";
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

// API helper for persistence
async function updateTaskStatus(taskId: string, newStatus: TaskStatus): Promise<void> {
  // TODO: Replace with actual API endpoint
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

// Sortable Task Card Component
interface TaskCardProps {
  task: Task;
  isDragging?: boolean;
}

function TaskCard({ task, isDragging }: TaskCardProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging: isSortableDragging,
  } = useSortable({ id: task.id });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  const priorityColors = {
    low: "bg-slate-100 text-slate-600 dark:bg-slate-700 dark:text-slate-300",
    medium: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300",
    high: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300",
  };

  const isBeingDragged = isDragging || isSortableDragging;

  return (
    <div
      ref={setNodeRef}
      style={style}
      {...attributes}
      {...listeners}
      className={`
        group cursor-grab rounded-lg border border-slate-200 bg-white p-4 shadow-sm
        transition-all duration-200
        hover:border-sky-300 hover:shadow-md
        active:cursor-grabbing
        dark:border-slate-700 dark:bg-slate-800
        ${isBeingDragged ? "opacity-50 shadow-lg ring-2 ring-sky-400" : ""}
      `}
    >
      <h4 className="font-medium text-slate-900 dark:text-white">{task.title}</h4>
      {task.description && (
        <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">
          {task.description}
        </p>
      )}
      {task.priority && (
        <span
          className={`mt-2 inline-block rounded-full px-2 py-0.5 text-xs font-medium ${priorityColors[task.priority]}`}
        >
          {task.priority}
        </span>
      )}
    </div>
  );
}

// Overlay card shown while dragging
function DragOverlayCard({ task }: { task: Task }) {
  const priorityColors = {
    low: "bg-slate-100 text-slate-600 dark:bg-slate-700 dark:text-slate-300",
    medium: "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300",
    high: "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300",
  };

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
          className={`mt-2 inline-block rounded-full px-2 py-0.5 text-xs font-medium ${priorityColors[task.priority]}`}
        >
          {task.priority}
        </span>
      )}
    </div>
  );
}

// Kanban Column Component
interface KanbanColumnProps {
  column: Column;
  tasks: Task[];
  isOver?: boolean;
}

function KanbanColumn({ column, tasks, isOver }: KanbanColumnProps) {
  const taskIds = tasks.map((t) => t.id);

  return (
    <div
      className={`
        flex min-h-[400px] w-80 flex-col rounded-xl border bg-slate-50 p-4
        transition-colors duration-200
        dark:border-slate-700 dark:bg-slate-900/50
        ${isOver ? "border-sky-400 bg-sky-50 dark:bg-sky-900/20" : "border-slate-200"}
      `}
    >
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
            <div
              className={`
                flex flex-1 items-center justify-center rounded-lg border-2 border-dashed
                text-sm text-slate-400
                transition-colors duration-200
                dark:border-slate-700 dark:text-slate-500
                ${isOver ? "border-sky-400 bg-sky-100/50 text-sky-600 dark:bg-sky-900/30 dark:text-sky-400" : "border-slate-300"}
              `}
            >
              {isOver ? "Drop here!" : "No tasks yet"}
            </div>
          )}
        </div>
      </SortableContext>
    </div>
  );
}

// Main Kanban Board Component
export default function KanbanBoard() {
  const [tasks, setTasks] = useState<Task[]>(INITIAL_TASKS);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [overColumn, setOverColumn] = useState<TaskStatus | null>(null);

  // Configure sensors for drag detection
  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: {
        distance: 8, // Require 8px movement before drag starts
      },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    })
  );

  // Get tasks by status
  const getTasksByStatus = useCallback(
    (status: TaskStatus) => tasks.filter((task) => task.status === status),
    [tasks]
  );

  // Find which column a task belongs to
  const findColumnByTaskId = (taskId: string): TaskStatus | null => {
    const task = tasks.find((t) => t.id === taskId);
    return task?.status ?? null;
  };

  // Handle drag start
  const handleDragStart = (event: DragStartEvent) => {
    const { active } = event;
    const task = tasks.find((t) => t.id === active.id);
    if (task) {
      setActiveTask(task);
    }
  };

  // Handle drag over (for visual feedback)
  const handleDragOver = (event: DragOverEvent) => {
    const { over } = event;
    
    if (!over) {
      setOverColumn(null);
      return;
    }

    // Check if we're over a column or a task
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
  };

  // Handle drag end
  const handleDragEnd = async (event: DragEndEvent) => {
    const { active, over } = event;

    setActiveTask(null);
    setOverColumn(null);

    if (!over) return;

    const activeId = active.id as string;
    const overId = over.id as string;

    // Find the target column
    let targetStatus: TaskStatus | null = null;

    // Check if dropped on a column
    if (COLUMNS.some((col) => col.id === overId)) {
      targetStatus = overId as TaskStatus;
    } else {
      // Dropped on a task - get that task's column
      targetStatus = findColumnByTaskId(overId);
    }

    if (!targetStatus) return;

    const activeTask = tasks.find((t) => t.id === activeId);
    if (!activeTask || activeTask.status === targetStatus) return;

    // Update local state optimistically
    setTasks((prev) =>
      prev.map((task) =>
        task.id === activeId ? { ...task, status: targetStatus! } : task
      )
    );

    // Persist to API
    await updateTaskStatus(activeId, targetStatus);
  };

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
              tasks={getTasksByStatus(column.id)}
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
