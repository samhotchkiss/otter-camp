import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

export type MessageMarkdownProps = {
  markdown: string;
  className?: string;
};

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
