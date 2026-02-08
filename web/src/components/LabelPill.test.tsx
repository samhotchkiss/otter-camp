import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import LabelPill, { type LabelOption } from "./LabelPill";

const sampleLabel: LabelOption = {
  id: "label-bug",
  name: "bug",
  color: "#ef4444",
};

describe("LabelPill", () => {
  it("renders label text and color styles", () => {
    render(<LabelPill label={sampleLabel} />);

    const pill = screen.getByTestId("label-pill-label-bug");
    expect(pill).toHaveTextContent("bug");
    expect(pill).toHaveStyle({ color: "rgb(239, 68, 68)" });
    expect(pill).toHaveStyle({ backgroundColor: "rgba(239, 68, 68, 0.15)" });
  });

  it("renders remove action in editable mode and triggers callback", () => {
    const onRemove = vi.fn();
    render(<LabelPill label={sampleLabel} editable onRemove={onRemove} />);

    fireEvent.click(screen.getByRole("button", { name: "Remove label bug" }));
    expect(onRemove).toHaveBeenCalledWith(sampleLabel);
  });

  it("supports click handler for management actions", () => {
    const onClick = vi.fn();
    render(<LabelPill label={sampleLabel} onClick={onClick} />);

    fireEvent.click(screen.getByTestId("label-pill-label-bug"));
    expect(onClick).toHaveBeenCalledWith(sampleLabel);
  });
});
