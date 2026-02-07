/**
 * Format a timestamp for display.
 */
export function formatTimestamp(isoString: string): string {
  const date = new Date(isoString);
  const now = new Date();
  const isToday = date.toDateString() === now.toDateString();

  const timeStr = date.toLocaleTimeString(undefined, {
    hour: "numeric",
    minute: "2-digit",
  });

  if (isToday) {
    return timeStr;
  }

  const dateStr = date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });

  return `${dateStr} ${timeStr}`;
}

/**
 * Get initials from a name for avatar fallback.
 */
export function getInitials(name: string | null | undefined): string {
  const safeName = (name ?? "").trim();
  if (!safeName) {
    return "?";
  }

  return safeName
    .split(" ")
    .map((part) => part[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

// Alias for backwards compatibility
export const formatMessageTimestamp = formatTimestamp;
