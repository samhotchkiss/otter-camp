import { describe, expect, it } from "vitest";
import {
  insertCriticMarkupCommentAtSelection,
  parseCriticMarkupComments,
  restoreCriticMarkupTokens,
  serializeCriticMarkupComment,
  tokenizeCriticMarkup,
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

  it("inserts comments at caret and after selected ranges", () => {
    const input = "Hello world";
    const caretInsertion = insertCriticMarkupCommentAtSelection({
      markdown: input,
      start: 5,
      end: 5,
      author: "Sam",
      message: "tighten this",
    });
    expect(caretInsertion.markdown).toBe("Hello{>>Sam: tighten this<<} world");

    const rangeInsertion = insertCriticMarkupCommentAtSelection({
      markdown: input,
      start: 0,
      end: 5,
      author: "Sam",
      message: "good intro",
    });
    expect(rangeInsertion.markdown).toBe("Hello{>>Sam: good intro<<} world");
  });

  it("keeps repeated insertions outside existing marker boundaries", () => {
    const first = insertCriticMarkupCommentAtSelection({
      markdown: "Alpha beta",
      start: 5,
      end: 5,
      author: "AB",
      message: "first",
    });
    const second = insertCriticMarkupCommentAtSelection({
      markdown: first.markdown,
      start: 8,
      end: 8,
      author: "AB",
      message: "second",
    });

    expect(second.markdown).toContain("{>>AB: first<<}{>>AB: second<<}");
    expect(() => parseCriticMarkupComments(second.markdown)).not.toThrow();
  });
});
