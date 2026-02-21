export function formatProjectTaskSummary(completed: number, total: number): string {
  if (total <= 0) {
    return "No tasks yet";
  }
  return `${completed}/${total} tasks`;
}
