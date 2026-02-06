import { describe, expect, it } from "vitest";
import { buildMarkdownImageLink, insertMarkdownImageLinkAtSelection } from "./markdownAsset";

describe("markdown asset helpers", () => {
  it("builds normalized markdown image links", () => {
    expect(buildMarkdownImageLink("/assets/cover.png")).toBe("![cover](/assets/cover.png)");
    expect(buildMarkdownImageLink("assets/diagram.gif", "Diagram")).toBe(
      "![Diagram](/assets/diagram.gif)"
    );
    expect(buildMarkdownImageLink("figure.jpg")).toBe("![figure](/assets/figure.jpg)");
  });

  it("inserts markdown image links at selection offsets", () => {
    const result = insertMarkdownImageLinkAtSelection({
      markdown: "Intro\n\nBody",
      start: 7,
      end: 7,
      assetPath: "/assets/cover.png",
      altText: "Cover",
    });

    expect(result.imageMarkdown).toBe("![Cover](/assets/cover.png)");
    expect(result.markdown).toContain("![Cover](/assets/cover.png)");
    expect(result.cursor).toBeGreaterThan(7);
  });
});
