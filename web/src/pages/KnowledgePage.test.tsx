import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import KnowledgePage from "./KnowledgePage";

describe("KnowledgePage", () => {
  it("renders figma memory baseline sections", () => {
    render(<KnowledgePage />);

    expect(screen.getByRole("heading", { name: "Memory System" })).toBeInTheDocument();
    expect(screen.getByText("Vector retrieval • Entity synthesis • File persistence")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Search memory graph...")).toBeInTheDocument();

    expect(screen.getByRole("heading", { name: "Stream: Conversation Extraction" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Taxonomy" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Authored Documentation" })).toBeInTheDocument();

    expect(screen.getByText("Customer Portal - Authentication Flow")).toBeInTheDocument();
    expect(screen.getByText("Vector Embeddings")).toBeInTheDocument();
  });
});
