import { fireEvent, render, screen } from "@testing-library/react";
import * as React from "react";
import { describe, expect, it } from "vitest";

import LabelFilter, { type LabelOption } from "./LabelFilter";

const labels: LabelOption[] = [
  { id: "label-bug", name: "bug", color: "#ef4444" },
  { id: "label-feature", name: "feature", color: "#22c55e" },
  { id: "label-blocked", name: "blocked", color: "#f97316" },
];

function renderHarness() {
  function Harness() {
    const [selectedLabelIDs, setSelectedLabelIDs] = React.useState<string[]>([]);
    return (
      <div>
        <LabelFilter
          labels={labels}
          selectedLabelIDs={selectedLabelIDs}
          onChange={setSelectedLabelIDs}
        />
        <output data-testid="selected-labels">{selectedLabelIDs.join(",")}</output>
      </div>
    );
  }

  return render(<Harness />);
}

describe("LabelFilter", () => {
  it("toggles labels on and off for multi-select filtering", () => {
    renderHarness();

    fireEvent.click(screen.getByRole("button", { name: "Toggle label bug" }));
    expect(screen.getByTestId("selected-labels")).toHaveTextContent("label-bug");

    fireEvent.click(screen.getByRole("button", { name: "Toggle label feature" }));
    expect(screen.getByTestId("selected-labels")).toHaveTextContent("label-bug,label-feature");

    fireEvent.click(screen.getByRole("button", { name: "Toggle label bug" }));
    expect(screen.getByTestId("selected-labels")).toHaveTextContent("label-feature");
  });

  it("clears all selected labels", () => {
    renderHarness();

    fireEvent.click(screen.getByRole("button", { name: "Toggle label bug" }));
    fireEvent.click(screen.getByRole("button", { name: "Toggle label feature" }));
    fireEvent.click(screen.getByRole("button", { name: "Clear label filters" }));

    expect(screen.getByTestId("selected-labels")).toHaveTextContent("");
  });
});
