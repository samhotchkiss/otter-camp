import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AddAgentModal from "./AddAgentModal";

describe("AddAgentModal", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("submits create request and closes on success", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    const onClose = vi.fn();
    const onCreated = vi.fn();
    render(<AddAgentModal isOpen onClose={onClose} onCreated={onCreated} />);

    fireEvent.change(screen.getByLabelText("Slot"), { target: { value: "research" } });
    fireEvent.change(screen.getByLabelText("Display Name"), { target: { value: "Riley" } });
    fireEvent.change(screen.getByLabelText("Model"), { target: { value: "gpt-5.2-codex" } });
    fireEvent.change(screen.getByLabelText("Heartbeat (optional)"), { target: { value: "15m" } });
    fireEvent.change(screen.getByLabelText("Channel (optional)"), { target: { value: "slack:#engineering" } });

    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
    expect(fetchMock.mock.calls[0]?.[0]).toContain("/api/admin/agents");
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body || "{}"))).toMatchObject({
      slot: "research",
      display_name: "Riley",
      model: "gpt-5.2-codex",
      heartbeat_every: "15m",
      channel: "slack:#engineering",
    });
    expect(screen.getByText("Creates a managed OtterCamp identity + memory scaffold for chameleon routing.")).toBeInTheDocument();
    expect(onCreated).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("renders API errors", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ error: "boom" }), { status: 500 }));
    vi.stubGlobal("fetch", fetchMock as unknown as typeof fetch);

    render(<AddAgentModal isOpen onClose={vi.fn()} onCreated={vi.fn()} />);

    fireEvent.change(screen.getByLabelText("Slot"), { target: { value: "research" } });
    fireEvent.change(screen.getByLabelText("Display Name"), { target: { value: "Riley" } });
    fireEvent.click(screen.getByRole("button", { name: "Create Agent" }));

    await waitFor(() => {
      expect(screen.getByText("boom")).toBeInTheDocument();
    });
  });
});
