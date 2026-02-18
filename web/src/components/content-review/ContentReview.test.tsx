import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ContentReview from "./ContentReview";

describe("ContentReview state workflow", () => {
  it("renders redesigned review shell scaffolding for stats, line lane, and sidebar", () => {
    render(<ContentReview initialMarkdown="# Title\n\nBody" reviewerName="Sam" />);

    expect(screen.getByTestId("content-review-shell")).toBeInTheDocument();
    expect(screen.getByTestId("review-stats-grid")).toBeInTheDocument();
    expect(screen.getByTestId("review-line-lane")).toBeInTheDocument();
    expect(screen.getByTestId("review-comment-sidebar")).toBeInTheDocument();
  });

  it("starts in Draft and requires Ready-for-Review before approval actions", () => {
    render(<ContentReview initialMarkdown="# Title\n\nBody" reviewerName="Sam" />);

    expect(screen.getByTestId("review-state-label")).toHaveTextContent("State: Draft");
    expect(screen.getByRole("button", { name: "Mark Ready for Review" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve Content" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Request Changes" })).not.toBeInTheDocument();
  });

  it("supports Draft -> Ready for Review -> Needs Changes -> Ready for Review -> Approved", async () => {
    const user = userEvent.setup();
    render(<ContentReview initialMarkdown="# Title\n\nBody" reviewerName="Sam" />);

    await user.click(screen.getByRole("button", { name: "Mark Ready for Review" }));
    expect(screen.getByTestId("review-state-label")).toHaveTextContent("State: Ready for Review");
    expect(screen.getByRole("button", { name: "Approve Content" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Request Changes" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Request Changes" }));
    expect(screen.getByTestId("review-state-label")).toHaveTextContent("State: Needs Changes");
    expect(screen.getByRole("button", { name: "Mark Ready for Review" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Mark Ready for Review" }));
    expect(screen.getByTestId("review-state-label")).toHaveTextContent("State: Ready for Review");

    await user.click(screen.getByRole("button", { name: "Approve Content" }));
    expect(screen.getByTestId("review-state-label")).toHaveTextContent("State: Approved");
    expect(screen.getByText("Approved")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Approve Content" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Request Changes" })).not.toBeInTheDocument();
  });
});

describe("ContentReview markdown source/render toggle", () => {
  it("switches views without mutating underlying markdown text", async () => {
    const user = userEvent.setup();
    render(<ContentReview initialMarkdown="# Title\n\nBody" reviewerName="Sam" />);

    const textarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    await user.type(textarea, "\n\nMore detail");
    const expected = textarea.value;

    await user.click(screen.getByRole("button", { name: "Rendered" }));
    await user.click(screen.getByRole("button", { name: "Source" }));

    expect((screen.getByTestId("source-textarea") as HTMLTextAreaElement).value).toBe(expected);
  });

  it("restores source cursor/selection after toggling back from rendered view", async () => {
    const user = userEvent.setup();
    render(<ContentReview initialMarkdown="# Title\n\nBody" reviewerName="Sam" />);

    const textarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    textarea.focus();
    textarea.setSelectionRange(2, 7);
    fireEvent.select(textarea);

    await user.click(screen.getByRole("button", { name: "Rendered" }));
    await user.click(screen.getByRole("button", { name: "Source" }));

    await waitFor(() => {
      const source = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
      expect(source.selectionStart).toBe(2);
      expect(source.selectionEnd).toBe(7);
    });
  });

  it("preserves unsaved edits across multiple toggles", async () => {
    const user = userEvent.setup();
    render(<ContentReview initialMarkdown="# Title\n\nBody" reviewerName="Sam" />);

    const textarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    await user.type(textarea, "\nUnsaved change");
    const expected = textarea.value;

    await user.click(screen.getByRole("button", { name: "Rendered" }));
    await user.click(screen.getByRole("button", { name: "Source" }));
    await user.click(screen.getByRole("button", { name: "Rendered" }));
    await user.click(screen.getByRole("button", { name: "Source" }));

    expect((screen.getByTestId("source-textarea") as HTMLTextAreaElement).value).toBe(expected);
  });

  it("renders headings, lists, and code blocks stably in rendered view", async () => {
    const user = userEvent.setup();
    const markdown = "# Heading\n\n- one\n- two\n\n```ts\nconst value = 1;\n```";
    render(<ContentReview initialMarkdown={markdown} reviewerName="Sam" />);

    await user.click(screen.getByRole("button", { name: "Rendered" }));

    expect(screen.getByText("Heading")).toBeInTheDocument();
    expect(screen.getByText("one")).toBeInTheDocument();
    expect(screen.getByText((content) => content.includes("value"))).toBeInTheDocument();
  });
});

describe("ContentReview inline comment insertion", () => {
  it("supports click flow to insert CriticMarkup at current selection", async () => {
    const user = userEvent.setup();
    render(<ContentReview initialMarkdown="Hello world" reviewerName="Sam" />);

    const textarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    textarea.focus();
    textarea.setSelectionRange(5, 5);
    fireEvent.select(textarea);

    await user.click(screen.getByRole("button", { name: "Add Inline Comment" }));
    await user.type(screen.getByTestId("inline-comment-input"), "tighten");
    await user.click(screen.getByRole("button", { name: "Insert Inline Comment" }));

    expect((screen.getByTestId("source-textarea") as HTMLTextAreaElement).value).toContain(
      "{>>Sam: tighten<<}"
    );
  });

  it("supports keyboard flow (Cmd/Ctrl+Shift+M) to open insertion composer", async () => {
    const user = userEvent.setup();
    render(<ContentReview initialMarkdown="Hello world" reviewerName="AB" />);

    const textarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    textarea.focus();
    textarea.setSelectionRange(11, 11);
    fireEvent.keyDown(textarea, { key: "m", ctrlKey: true, shiftKey: true });

    await user.type(screen.getByTestId("inline-comment-input"), "add ending");
    await user.click(screen.getByRole("button", { name: "Insert Inline Comment" }));

    expect((screen.getByTestId("source-textarea") as HTMLTextAreaElement).value).toContain(
      "{>>AB: add ending<<}"
    );
  });

  it("persists inserted comments through reload and re-render", async () => {
    const user = userEvent.setup();
    const first = render(<ContentReview initialMarkdown="Draft content" reviewerName="Sam" />);

    const textarea = first.getByTestId("source-textarea") as HTMLTextAreaElement;
    textarea.focus();
    textarea.setSelectionRange(5, 5);
    fireEvent.select(textarea);

    await user.click(first.getByRole("button", { name: "Add Inline Comment" }));
    await user.type(first.getByTestId("inline-comment-input"), "check this");
    await user.click(first.getByRole("button", { name: "Insert Inline Comment" }));

    const persistedMarkdown = (first.getByTestId("source-textarea") as HTMLTextAreaElement).value;
    first.unmount();

    render(<ContentReview initialMarkdown={persistedMarkdown} reviewerName="Sam" />);
    await user.click(screen.getByRole("button", { name: "Rendered" }));

    expect(screen.getByTestId("critic-comment-bubble")).toBeInTheDocument();
    expect(screen.getByText("check this")).toBeInTheDocument();
  });

  it("inserts markdown image links from the image insert flow", async () => {
    const user = userEvent.setup();
    render(<ContentReview initialMarkdown="Intro" reviewerName="Sam" />);

    const textarea = screen.getByTestId("source-textarea") as HTMLTextAreaElement;
    textarea.focus();
    textarea.setSelectionRange(textarea.value.length, textarea.value.length);
    fireEvent.select(textarea);

    await user.click(screen.getByRole("button", { name: "Add Image Link" }));
    await user.type(screen.getByTestId("image-path-input"), "/assets/cover.png");
    await user.type(screen.getByTestId("image-alt-input"), "Cover");
    await user.click(screen.getByRole("button", { name: "Insert Image Link" }));

    expect((screen.getByTestId("source-textarea") as HTMLTextAreaElement).value).toContain(
      "![Cover](/assets/cover.png)"
    );
  });
});

describe("ContentReview read-only snapshots", () => {
  it("hides editing controls and still renders CriticMarkup comments", async () => {
    const user = userEvent.setup();
    render(
      <ContentReview
        initialMarkdown={"# Snapshot\n\nBody {>>AB: old feedback<<}"}
        reviewerName="Sam"
        readOnly
      />
    );

    expect(screen.getByTestId("content-review-read-only")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mark Ready for Review" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add Inline Comment" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add Image Link" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Rendered" }));
    expect(await screen.findByText("old feedback")).toBeInTheDocument();
  });
});
