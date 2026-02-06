import { describe, expect, it } from "vitest";
import {
  parseCriticMarkupComments,
  serializeCriticMarkupComment,
  tokenizeCriticMarkup,
  restoreCriticMarkupTokens,
} from "./criticMarkup";

describe("criticMarkup utilities", () => {
  it("parses author-attributed comments", () => {
    const input = "Hello {>>AB: tighten this intro<<} world";
    const comments = parseCriticMarkupComments(input);

    expect(comments).toHaveLength(1);
    expect(comments[0].author).toBe("AB");
    expect(comments[0].message).toBe("tighten this intro");
  });

  it("parses legacy comments without author", () => {
    const input = "Hello {>> tighten this intro <<} world";
    const comments = parseCriticMarkupComments(input);

    expect(comments).toHaveLength(1);
    expect(comments[0].author).toBeUndefined();
    expect(comments[0].message).toBe("tighten this intro");
  });

  it("preserves parsed raw comment formatting through serialize", () => {
    const input = "{>>AB: keep exact spacing<<}";
    const [comment] = parseCriticMarkupComments(input);
    expect(serializeCriticMarkupComment(comment)).toBe(input);
  });

  it("tokenizes and restores comments deterministically", () => {
    const input = "One {>>AB: alpha<<} and {>>beta<<}.";
    const tokenized = tokenizeCriticMarkup(input);

    expect(tokenized.markdown).toContain("@@CRITIC_COMMENT_0@@");
    expect(tokenized.markdown).toContain("@@CRITIC_COMMENT_1@@");

    const restored = restoreCriticMarkupTokens(tokenized.markdown, tokenized.commentsByToken);
    expect(restored).toBe(input);
  });
});
