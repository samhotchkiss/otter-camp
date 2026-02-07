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
export function getInitials(name: unknown): string {
  let normalized = "";
  if (typeof name === "string") {
    normalized = name;
  } else if (typeof name === "number" && Number.isFinite(name)) {
    normalized = String(name);
  }

  const safeName = normalized.trim();
  if (!safeName) {
    return "?";
  }

  return safeName
    .split(/\s+/)
    .filter(Boolean)
    .map((part) => part.charAt(0))
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

// Alias for backwards compatibility
export const formatMessageTimestamp = formatTimestamp;
