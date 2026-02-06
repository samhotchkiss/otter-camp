export type MarkdownImageInsertionInput = {
  markdown: string;
  start: number;
  end: number;
  assetPath: string;
  altText?: string;
};

export type MarkdownImageInsertionResult = {
  markdown: string;
  imageMarkdown: string;
  cursor: number;
};

function normalizeOffset(value: number, length: number): number {
  if (!Number.isFinite(value) || Number.isNaN(value)) return 0;
  if (value < 0) return 0;
  if (value > length) return length;
  return Math.floor(value);
}

function normalizeAssetPath(input: string): string {
  const trimmed = input.trim();
  if (trimmed === "") {
    return "/assets/image.png";
  }
  if (trimmed.startsWith("/")) {
    return trimmed;
  }
  if (trimmed.startsWith("assets/")) {
    return `/${trimmed}`;
  }
  return `/assets/${trimmed}`;
}

function deriveAltText(assetPath: string, altText?: string): string {
  const provided = (altText ?? "").trim();
  if (provided !== "") {
    return provided;
  }
  const fileName = assetPath.split("/").pop() ?? "image";
  const withoutExt = fileName.replace(/\.[A-Za-z0-9]+$/, "");
  return withoutExt || "image";
}

export function buildMarkdownImageLink(assetPath: string, altText?: string): string {
  const normalizedPath = normalizeAssetPath(assetPath);
  const normalizedAlt = deriveAltText(normalizedPath, altText);
  return `![${normalizedAlt}](${normalizedPath})`;
}

export function insertMarkdownImageLinkAtSelection(
  input: MarkdownImageInsertionInput
): MarkdownImageInsertionResult {
  const markdown = input.markdown ?? "";
  const length = markdown.length;
  let start = normalizeOffset(input.start, length);
  let end = normalizeOffset(input.end, length);
  if (start > end) {
    [start, end] = [end, start];
  }

  const imageMarkdown = buildMarkdownImageLink(input.assetPath, input.altText);
  const insertion = imageMarkdown + "\n";
  const next = markdown.slice(0, end) + insertion + markdown.slice(end);

  return {
    markdown: next,
    imageMarkdown,
    cursor: end + insertion.length,
  };
}
