import { useTheme } from "../hooks/useTheme";

export default function ThemeToggle() {
  const { theme, toggle } = useTheme();

  const isDark = theme === "dark";

  return (
    <button
      type="button"
      onClick={toggle}
      className="rounded-lg bg-transparent p-2 text-lg transition-opacity hover:opacity-70"
      aria-label={isDark ? "Switch to light mode" : "Switch to dark mode"}
    >
      <span aria-hidden="true">{isDark ? "☀️" : "🌙"}</span>
    </button>
  );
}
