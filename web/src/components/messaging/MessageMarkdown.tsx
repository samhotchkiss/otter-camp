import { isValidElement, type ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

export type MessageMarkdownProps = {
  markdown: string;
  className?: string;
};

type OtterInternalLinkMeta = {
  href: string;
  kind: string;
  detail: string;
};

const LOCALHOST_NAMES = new Set(["localhost", "127.0.0.1", "::1"]);

function normalizePathname(pathname: string): string {
  const trimmed = pathname.trim();
  if (!trimmed) {
    return "/";
  }
  const normalized = trimmed.startsWith("/") ? trimmed : `/${trimmed}`;
  if (normalized.length > 1 && normalized.endsWith("/")) {
    return normalized.slice(0, -1);
  }
  return normalized;
}

function decodeSegment(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

function extractText(children: ReactNode): string {
  if (children == null || typeof children === "boolean") {
    return "";
  }
  if (typeof children === "string" || typeof children === "number") {
    return String(children);
  }
  if (Array.isArray(children)) {
    return children.map((child) => extractText(child)).join("");
  }
  if (isValidElement(children)) {
    return extractText(children.props.children);
  }
  return "";
}

function buildDetail(pathname: string): { kind: string; detail: string } {
  const parts = pathname.split("/").filter(Boolean).map((part) => decodeSegment(part));
  if (parts.length === 0) {
    return { kind: "Home", detail: pathname };
  }
  if (parts[0] === "projects" && parts.length >= 3 && (parts[2] === "issues" || parts[2] === "tasks")) {
    return {
      kind: "Task",
      detail: `Project ${parts[1]} · Task ${parts[3] ?? "details"}`,
    };
  }
  if (parts[0] === "projects" && parts.length === 2) {
    return {
      kind: "Project",
      detail: parts[1],
    };
  }
  if (parts[0] === "projects") {
    return {
      kind: "Project",
      detail: parts.slice(1).join(" · "),
    };
  }
  if (parts[0] === "agents") {
    return {
      kind: "Agent",
      detail: parts[1] ?? "Directory",
    };
  }
  if (parts[0] === "knowledge") {
    return {
      kind: "Knowledge",
      detail: parts[1] ? parts.slice(1).join(" · ") : "Knowledge Base",
    };
  }
  if (parts[0] === "workflows") {
    return {
      kind: "Workflow",
      detail: parts[1] ? parts.slice(1).join(" · ") : "Workflows",
    };
  }
  if (parts[0] === "inbox") {
    return { kind: "Inbox", detail: "Needs your attention" };
  }
  if (parts[0] === "chats") {
    return { kind: "Chat", detail: parts[1] ? parts[1] : "Conversation" };
  }
  if (parts[0] === "settings") {
    return { kind: "Settings", detail: parts[1] ? parts[1] : "Workspace settings" };
  }
  return { kind: "OtterCamp", detail: pathname };
}

function isLikelyOtterHost(url: URL): boolean {
  if (typeof window === "undefined") {
    return LOCALHOST_NAMES.has(url.hostname);
  }
  const current = new URL(window.location.href);
  if (url.hostname === current.hostname) {
    return true;
  }
  return LOCALHOST_NAMES.has(url.hostname) && LOCALHOST_NAMES.has(current.hostname);
}

export function resolveOtterInternalLink(href: string | undefined): OtterInternalLinkMeta | null {
  if (!href) {
    return null;
  }
  const raw = href.trim();
  if (!raw) {
    return null;
  }

  let parsed: URL;
  try {
    if (raw.startsWith("/")) {
      const base = typeof window === "undefined" ? "http://localhost" : window.location.origin;
      parsed = new URL(raw, base);
    } else {
      parsed = new URL(raw);
    }
  } catch {
    return null;
  }

  if (!isLikelyOtterHost(parsed)) {
    return null;
  }

  const pathname = normalizePathname(parsed.pathname);
  if (pathname.startsWith("/api/") || pathname === "/api") {
    return null;
  }
  const { kind, detail } = buildDetail(pathname);
  return {
    href: `${pathname}${parsed.search}${parsed.hash}`,
    kind,
    detail,
  };
}

export default function MessageMarkdown({
  markdown,
  className,
}: MessageMarkdownProps) {
  return (
    <div className={className}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          pre({ children }) {
            return (
              <pre className="my-2 overflow-x-auto rounded-xl border border-slate-200 bg-slate-950/90 p-3 text-xs text-slate-100 dark:border-slate-700">
                {children}
              </pre>
            );
          },
          p({ children }) {
            return <p className="whitespace-pre-wrap break-words">{children}</p>;
          },
          a({ children, href }) {
            const internal = resolveOtterInternalLink(href);
            if (internal) {
              const text = extractText(children).trim();
              const title = text && text !== href ? text : `Open ${internal.kind}`;
              return (
                <a
                  href={internal.href}
                  className="my-1 inline-flex max-w-[30rem] items-start gap-3 rounded-xl border border-[var(--accent)]/35 bg-[var(--accent)]/10 px-3 py-2 no-underline transition hover:border-[var(--accent)]/60 hover:bg-[var(--accent)]/15"
                  data-testid="otter-internal-link-card"
                >
                  <span
                    aria-hidden="true"
                    className="mt-0.5 inline-flex h-6 w-6 items-center justify-center rounded-md border border-[var(--accent)]/35 bg-[var(--surface)] text-[13px]"
                  >
                    OC
                  </span>
                  <span className="min-w-0">
                    <span className="block text-[10px] font-semibold uppercase tracking-wide text-[var(--text-muted)]">
                      {internal.kind}
                    </span>
                    <span className="block truncate text-sm font-semibold text-[var(--text)]">
                      {title}
                    </span>
                    <span className="block truncate text-xs text-[var(--text-muted)]">
                      {internal.detail}
                    </span>
                  </span>
                </a>
              );
            }
            return (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer"
                className="underline underline-offset-4 decoration-current/40 hover:decoration-current"
              >
                {children}
              </a>
            );
          },
          ul({ children }) {
            return <ul className="list-disc space-y-1 pl-5">{children}</ul>;
          },
          ol({ children }) {
            return <ol className="list-decimal space-y-1 pl-5">{children}</ol>;
          },
          blockquote({ children }) {
            return (
              <blockquote className="border-l-2 border-current/20 pl-3 opacity-90">
                {children}
              </blockquote>
            );
          },
          code({ inline, className: codeClassName, children, ...props }: any) {
            if (inline) {
              return (
                <code
                  className="rounded bg-black/10 px-1 py-0.5 font-mono text-[0.85em] dark:bg-white/10"
                  {...props}
                >
                  {children}
                </code>
              );
            }

            return (
              <code className={codeClassName} {...props}>
                {String(children).replace(/\n$/, "")}
              </code>
            );
          },
        }}
      >
        {markdown}
      </ReactMarkdown>
    </div>
  );
}
