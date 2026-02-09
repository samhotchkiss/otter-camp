export function formatProjectTaskSummary(completed: number, total: number): string {
  if (total <= 0) {
    return "No issues yet";
  }
  return `${completed}/${total} issues`;
}
