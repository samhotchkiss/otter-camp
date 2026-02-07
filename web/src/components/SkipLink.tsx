/**
 * SkipLink - Accessible skip navigation link
 *
 * Provides a hidden link that becomes visible on focus, allowing keyboard users
 * to skip repetitive navigation and jump directly to main content.
 *
 * @see https://www.w3.org/WAI/WCAG21/Understanding/bypass-blocks.html
 */

type SkipLinkProps = {
  /** ID of the target element to skip to (without #) */
  targetId?: string;
  /** Custom label for the skip link */
  label?: string;
};

export default function SkipLink({
  targetId = "main-content",
  label = "Skip to main content",
}: SkipLinkProps) {
  const handleClick = (event: React.MouseEvent<HTMLAnchorElement>) => {
    event.preventDefault();
    const target = document.getElementById(targetId);

    if (target) {
      // Set tabindex temporarily if not focusable
      if (!target.hasAttribute("tabindex")) {
        target.setAttribute("tabindex", "-1");
        target.addEventListener(
          "blur",
          () => target.removeAttribute("tabindex"),
          { once: true }
        );
      }

      target.focus();
      target.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  };

  return (
    <a
      href={`#${targetId}`}
      onClick={handleClick}
      className="
        fixed left-4 top-4 z-[100] -translate-y-16 rounded-lg
        bg-sky-600 px-4 py-2 text-sm font-medium text-white
        shadow-lg transition-transform duration-200
        focus:translate-y-0 focus:outline-none focus:ring-2
        focus:ring-sky-500 focus:ring-offset-2
        dark:bg-sky-500 dark:focus:ring-offset-slate-900
      "
    >
      {label}
    </a>
  );
}
