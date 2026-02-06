import {
  Fragment,
  cloneElement,
  isValidElement,
  useMemo,
  type CSSProperties,
  type ReactNode,
} from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { oneDark, oneLight } from "react-syntax-highlighter/dist/esm/styles/prism";
import { extractTextFromNode, getUniqueSectionId, slugifyHeading } from "./markdownUtils";
import {
  isCriticToken,
  restoreCriticMarkupTokens,
  tokenizeCriticMarkup,
  type CriticMarkupComment,
} from "./criticMarkup";

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
  slugCounts?: Map<string, number>,
  commentsByToken?: Record<string, CriticMarkupComment>
) {
  return function HeadingRenderer({ children }: { children?: React.ReactNode }) {
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
            {renderChildrenWithCriticBubbles(children, commentsByToken ?? {})}
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

function CriticCommentBubble({ comment }: { comment: CriticMarkupComment }) {
  return (
    <span
      className="mx-1 inline-flex items-start gap-1 rounded-md border border-amber-300 bg-amber-100 px-2 py-0.5 align-middle text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-900/40 dark:text-amber-100"
      data-testid="critic-comment-bubble"
    >
      {comment.author ? (
        <span
          className="rounded bg-amber-200 px-1 font-semibold uppercase tracking-wide dark:bg-amber-800"
          data-testid="critic-comment-author"
        >
          {comment.author}
        </span>
      ) : null}
      <span>{comment.message}</span>
    </span>
  );
}

function renderChildrenWithCriticBubbles(
  children: ReactNode,
  commentsByToken: Record<string, CriticMarkupComment>
): ReactNode {
  if (typeof children === "string") {
    const parts = children.split(/(@@CRITIC_COMMENT_\d+@@)/g);
    return parts.map((part, index) => {
      if (isCriticToken(part)) {
        const comment = commentsByToken[part];
        if (comment) {
          return <CriticCommentBubble key={`${comment.id}-${index}`} comment={comment} />;
        }
      }
      return part;
    });
  }

  if (Array.isArray(children)) {
    return children.map((child, index) => (
      <Fragment key={`critic-child-${index}`}>
        {renderChildrenWithCriticBubbles(child, commentsByToken)}
      </Fragment>
    ));
  }

  if (isValidElement(children)) {
    const originalChildren = (children.props as { children?: ReactNode }).children;
    const renderedChildren = renderChildrenWithCriticBubbles(originalChildren, commentsByToken);
    return cloneElement(children, undefined, renderedChildren);
  }

  return children;
}

export default function MarkdownPreview({
  markdown,
  activeSectionId,
  commentCounts,
  onSectionSelect,
}: MarkdownPreviewProps) {
  const prefersDark = useMemo(() => {
    if (typeof window === "undefined") return false;
    return window.matchMedia?.("(prefers-color-scheme: dark)")?.matches ?? false;
  }, []);

  const headingProps = {
    activeSectionId,
    commentCounts,
    onSectionSelect,
  };
  const criticMarkup = useMemo(() => tokenizeCriticMarkup(markdown), [markdown]);
  const commentsByToken = criticMarkup.commentsByToken;
  const slugCounts = new Map<string, number>();

  return (
    <div className="rounded-2xl border border-slate-200 bg-white/70 p-4 shadow-sm backdrop-blur dark:border-slate-800 dark:bg-slate-900/40">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: useHeadingRenderer(1, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts, commentsByToken),
          h2: useHeadingRenderer(2, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts, commentsByToken),
          h3: useHeadingRenderer(3, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts, commentsByToken),
          h4: useHeadingRenderer(4, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts, commentsByToken),
          h5: useHeadingRenderer(5, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts, commentsByToken),
          h6: useHeadingRenderer(6, headingProps.activeSectionId, headingProps.commentCounts, headingProps.onSectionSelect, slugCounts, commentsByToken),
          code({ className, children, ...props }) {
            const inline = Boolean((props as { inline?: boolean }).inline);
            const match = /language-(\w+)/.exec(className || "");
            const renderedCode = restoreCriticMarkupTokens(
              String(children).replace(/\n$/, ""),
              commentsByToken
            );
            if (!inline && match) {
              return (
                <div className="my-4 overflow-hidden rounded-xl border border-slate-200 bg-slate-950/90 dark:border-slate-800">
                  <SyntaxHighlighter
                    language={match[1]}
                    style={(prefersDark ? oneDark : oneLight) as Record<string, CSSProperties>}
                    customStyle={{
                      background: "transparent",
                      margin: 0,
                      padding: "1rem",
                    }}
                  >
                    {renderedCode}
                  </SyntaxHighlighter>
                </div>
              );
            }

            return (
              <code
                className="rounded bg-slate-100 px-1.5 py-0.5 text-sm text-slate-800 dark:bg-slate-800 dark:text-slate-100"
                {...props}
              >
                {renderedCode}
              </code>
            );
          },
          blockquote({ children }) {
            return (
              <blockquote className="border-l-4 border-indigo-400 bg-indigo-50 px-4 py-2 text-slate-700 dark:border-indigo-500 dark:bg-indigo-900/30 dark:text-slate-200">
                {renderChildrenWithCriticBubbles(children, commentsByToken)}
              </blockquote>
            );
          },
          ul({ children }) {
            return <ul className="list-disc space-y-2 pl-6 text-slate-700 dark:text-slate-200">{children}</ul>;
          },
          ol({ children }) {
            return <ol className="list-decimal space-y-2 pl-6 text-slate-700 dark:text-slate-200">{children}</ol>;
          },
          li({ children }) {
            return <li>{renderChildrenWithCriticBubbles(children, commentsByToken)}</li>;
          },
          p({ children }) {
            return (
              <p className="text-slate-700 dark:text-slate-200">
                {renderChildrenWithCriticBubbles(children, commentsByToken)}
              </p>
            );
          },
          a({ children, href }) {
            return (
              <a
                href={href}
                className="text-indigo-600 underline-offset-4 hover:underline dark:text-indigo-300"
              >
                {renderChildrenWithCriticBubbles(children, commentsByToken)}
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
                {renderChildrenWithCriticBubbles(children, commentsByToken)}
              </th>
            );
          },
          td({ children }) {
            return (
              <td className="border-b border-slate-200 px-3 py-2 dark:border-slate-800">
                {renderChildrenWithCriticBubbles(children, commentsByToken)}
              </td>
            );
          },
        }}
      >
        {criticMarkup.markdown}
      </ReactMarkdown>
    </div>
  );
}
