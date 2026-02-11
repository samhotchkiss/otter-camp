import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AgentsNewPage from "./AgentsNewPage";

describe("AgentsNewPage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("supports browse -> customize -> create -> welcome flow", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentsNewPage />);

    expect(screen.getByRole("heading", { name: "Hire an Agent" })).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Search Profiles"), { target: { value: "witty" } });
    expect(screen.getByRole("button", { name: /Kit/i })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Kit/i }));
    expect(await screen.findByText("Customize Kit")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    const body = JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body || "{}"));
    expect(body).toMatchObject({
      displayName: "Kit",
      profileId: "kit",
      model: "gpt-5.2-codex",
    });

    expect(await screen.findByText("Kit is ready to go")).toBeInTheDocument();
  });

  it("shows create errors and remains on customize step", async () => {
    const fetchMock = vi.fn(
      async () =>
        new Response(JSON.stringify({ error: "boom" }), {
          status: 500,
          headers: { "Content-Type": "application/json" },
        }),
    );
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AgentsNewPage />);

    fireEvent.click(screen.getByRole("button", { name: /Marcus/i }));
    expect(await screen.findByText("Customize Marcus")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));

    expect(await screen.findByText("boom")).toBeInTheDocument();
    expect(screen.getByText("Customize Marcus")).toBeInTheDocument();
  });
});
