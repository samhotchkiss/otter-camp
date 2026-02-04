export type ReviewSection = {
  id: string;
  title: string;
  level: number;
  startLine: number;
};

export function slugifyHeading(input: string): string {
  const normalized = input
    .toLowerCase()
    .replace(/[^a-z0-9\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-");
  return normalized || "section";
}

export function extractTextFromNode(node: unknown): string {
  if (typeof node === "string") return node;
  if (typeof node === "number") return String(node);
  if (!node) return "";
  if (Array.isArray(node)) return node.map(extractTextFromNode).join("");
  if (typeof node === "object" && "props" in (node as { props?: unknown })) {
    const props = (node as { props?: { children?: unknown } }).props;
    return extractTextFromNode(props?.children);
  }
  return "";
}

export function parseMarkdownSections(markdown: string): ReviewSection[] {
  const lines = markdown.split("\n");
  const sections: ReviewSection[] = [];
  const seen = new Map<string, number>();

  lines.forEach((line, index) => {
    const match = /^(#{1,6})\s+(.*)/.exec(line.trim());
    if (!match) return;

    const level = match[1].length;
    const title = match[2].trim();
    const baseId = slugifyHeading(title);
    const id = getUniqueSectionId(baseId, seen);

    sections.push({
      id,
      title,
      level,
      startLine: index + 1,
    });
  });

  if (sections.length === 0) {
    sections.push({ id: "document", title: "Document", level: 1, startLine: 1 });
  }

  return sections;
}

export function getUniqueSectionId(baseId: string, counts: Map<string, number>): string {
  const count = counts.get(baseId) ?? 0;
  counts.set(baseId, count + 1);
  return count === 0 ? baseId : `${baseId}-${count + 1}`;
}
