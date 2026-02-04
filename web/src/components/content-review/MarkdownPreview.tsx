import { useMemo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { oneDark, oneLight } from "react-syntax-highlighter/dist/esm/styles/prism";
import { extractTextFromNode, getUniqueSectionId, slugifyHeading } from "./markdownUtils";

export type MarkdownPreviewProps = {
  markdown: string;
  activeSectionId?: string;
  commentCounts?: Record<string, number>;
  onSectionSelect?: (sectionId: string) => void;
};

function useHeadingRenderer(
  level: number,
  activeSectionId?: string,
  commentCounts?: Record<string, number>,
  onSectionSelect?: (sectionId: string) => void,
  slugCounts?: Map<string, number>
) {
  return function HeadingRenderer({ children }: { children: React.ReactNode }) {
    const text = extractTextFromNode(children);
    const baseId = slugifyHeading(text);
    const sectionId = slugCounts ? getUniqueSectionId(baseId, slugCounts) : baseId;
    const count = commentCounts?.[sectionId] ?? 0;
    const isActive = sectionId === activeSectionId;
    const Tag = `h${level}` as keyof JSX.IntrinsicElements;

    return (
      <div className="group flex items-start gap-2 scroll-mt-24">
        <Tag
          id={sectionId}
          className={`flex-1 text-slate-900 dark:text-slate-100 ${
            isActive ? "text-indigo-600 dark:text-indigo-300" : ""
          }`}
        >
          <button
            type="button"
            onClick={() => onSectionSelect?.(sectionId)}
            className="text-left transition-colors hover:text-indigo-600 dark:hover:text-indigo-300"
          >
            {children}
          </button>
        </Tag>
        {count > 0 ? (
          <button
            type="button"
            onClick={() => onSectionSelect?.(sectionId)}
            className="mt-1 rounded-full bg-amber-100 px-2 py-0.5 text-xs font-semibold text-amber-800 dark:bg-amber-900/40 dark:text-amber-200"
            aria-label={`${count} comment${count === 1 ? "" : "s"} on ${text}`}
          >
            {count}
          </button>
        ) : null}
      </div>
    );
  };
}

export default function MarkdownPreview({
  markdown,
  activeSectionId,
  commentCounts,
  onSectionSelect,
}: MarkdownPreviewProps) {
  const prefersDark = useMemo(() => {
    if (typeof window === "undefined") return false;
    return window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false;
  }, []);

  const headingProps = {
    activeSectionId,
    commentCounts,
    onSectionSelect,
  };
  const slugCounts = new Map<string, number>();

  return (
    <div className="rounded-2xl border border-slate-200 bg-white/70 p-4 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/40">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: useHeadingRenderer(1, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts),
          h2: useHeadingRenderer(2, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts),
          h3: useHeadingRenderer(3, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts),
          h4: useHeadingRenderer(4, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts),
          h5: useHeadingRenderer(5, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts),
          h6: useHeadingRenderer(6, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts),
          code({ inline, className, children, ...props }) {
            const match = /language-(\w+)/.exec(className || "");
            if (!inline && match) {
              return (
                <div className="my-4 overflow-hidden rounded-xl border border-slate-200 bg-slate-950/90 dark:border-slate-800">
                  <SyntaxHighlighter
                    language={match[1]}
                    style={prefersDark ? oneDark : oneLight}
                    customStyle={{
                      background: "transparent",
                      margin: 0,
                      padding: "1rem",
                    }}
                    {...props}
                  >
                    {String(children).replace(/\n$/, "")}
                  </SyntaxHighlighter>
                </div>
              );
            }

            return (
              <code
                className="rounded bg-slate-100 px-1.5 py-0.5 text-sm text-slate-800 dark:bg-slate-800 dark:text-slate-100"
                {...props}
              >
                {children}
              </code>
            );
          },
          blockquote({ children }) {
            return (
              <blockquote className="border-l-4 border-indigo-400 bg-indigo-50 px-4 py-2 text-slate-700 dark:border-indigo-500 dark:bg-indigo-900/30 dark:text-slate-200">
                {children}
              </blockquote>
            );
          },
          ul({ children }) {
            return <ul className="list-disc space-y-2 pl-6 text-slate-700 dark:text-slate-200">{children}</ul>;
          },
          ol({ children }) {
            return <ol className="list-decimal space-y-2 pl-6 text-slate-700 dark:text-slate-200">{children}</ol>;
          },
          p({ children }) {
            return <p className="text-slate-700 dark:text-slate-200">{children}</p>;
          },
          a({ children, href }) {
            return (
              <a
                href={href}
                className="text-indigo-600 underline-offset-4 hover:underline dark:text-indigo-300"
              >
                {children}
              </a>
            );
          },
          table({ children }) {
            return (
              <div className="overflow-x-auto">
                <table className="min-w-full border-separate border-spacing-0 text-left text-sm text-slate-700 dark:text-slate-200">
                  {children}
                </table>
              </div>
            );
          },
          th({ children }) {
            return (
              <th className="border-b border-slate-200 bg-slate-100 px-3 py-2 text-xs font-semibold uppercase tracking-wide text-slate-600 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200">
                {children}
              </th>
            );
          },
          td({ children }) {
            return (
              <td className="border-b border-slate-200 px-3 py-2 dark:border-slate-800">
                {children}
              </td>
            );
          },
        }}
      >
        {markdown}
      </ReactMarkdown>
    </div>
  );
}
