export function getChatContextCue(
  conversationType: "dm" | "project" | "issue" | null | undefined,
): "Main context" | "Project context" | "Issue context" | "DM context" {
  if (conversationType === "project") {
    return "Project context";
  }
  if (conversationType === "issue") {
    return "Issue context";
  }
  if (conversationType === "dm") {
    return "DM context";
  }
  return "Main context";
}
