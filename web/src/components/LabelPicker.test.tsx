import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import LabelPicker, { type LabelOption } from "./LabelPicker";

const labels: LabelOption[] = [
  { id: "label-bug", name: "bug", color: "#ef4444" },
  { id: "label-feature", name: "feature", color: "#22c55e" },
  { id: "label-design", name: "design", color: "#ec4899" },
];

describe("LabelPicker", () => {
  it("filters labels from search input and shows selected checkmark", () => {
    render(
      <LabelPicker
        labels={labels}
        selectedLabelIDs={["label-feature"]}
        onAdd={vi.fn()}
        onRemove={vi.fn()}
        onCreate={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Manage labels" }));
    const popover = screen.getByTestId("label-picker-popover");
    expect(within(popover).getByText("feature")).toBeInTheDocument();
    expect(within(popover).getByTestId("label-picker-selected-label-feature")).toHaveTextContent("Selected");

    fireEvent.change(within(popover).getByLabelText("Search labels"), {
      target: { value: "des" },
    });

    expect(within(popover).getByText("design")).toBeInTheDocument();
    expect(within(popover).queryByText("bug")).not.toBeInTheDocument();
  });

  it("toggles add/remove callbacks based on current selection", () => {
    const onAdd = vi.fn();
    const onRemove = vi.fn();
    render(
      <LabelPicker
        labels={labels}
        selectedLabelIDs={["label-feature"]}
        onAdd={onAdd}
        onRemove={onRemove}
        onCreate={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Manage labels" }));

    fireEvent.click(screen.getByRole("button", { name: "Add label bug" }));
    expect(onAdd).toHaveBeenCalledWith("label-bug");

    fireEvent.click(screen.getByRole("button", { name: "Remove label feature" }));
    expect(onRemove).toHaveBeenCalledWith("label-feature");
  });

  it("supports create-new flow with color picker", () => {
    const onCreate = vi.fn();
    render(
      <LabelPicker
        labels={labels}
        selectedLabelIDs={[]}
        onAdd={vi.fn()}
        onRemove={vi.fn()}
        onCreate={onCreate}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Manage labels" }));

    fireEvent.change(screen.getByLabelText("Search labels"), {
      target: { value: "blocked-on-sam" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Color #f97316" }));
    fireEvent.click(screen.getByRole("button", { name: 'Create label "blocked-on-sam"' }));

    expect(onCreate).toHaveBeenCalledWith("blocked-on-sam", "#f97316");
  });
});
