import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ContentReview from "./ContentReview";

describe("ContentReview state workflow", () => {
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
