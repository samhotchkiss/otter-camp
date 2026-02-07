import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import DashboardLayout from "./DashboardLayout";
import { KeyboardShortcutsProvider } from "../contexts/KeyboardShortcutsContext";

vi.mock("../components/ShortcutsHelpModal", () => ({
  default: () => null,
}));

vi.mock("../components/DemoBanner", () => ({
  default: () => null,
}));

vi.mock("../components/GlobalSearch", () => ({
  default: () => null,
}));

vi.mock("../components/chat/GlobalChatDock", () => ({
  default: () => null,
}));

vi.mock("../hooks/useKeyboardShortcuts", () => ({
  useKeyboardShortcuts: () => undefined,
}));

vi.mock("../contexts/WebSocketContext", () => ({
  useWS: () => ({ connected: true }),
}));

vi.mock("../lib/api", () => ({
  api: {
    inbox: vi.fn(async () => ({ items: [] })),
  },
}));

describe("DashboardLayout", () => {
  it("shows Connections in navigation", async () => {
    render(
      <MemoryRouter initialEntries={["/connections"]}>
        <KeyboardShortcutsProvider>
          <DashboardLayout>
            <div>child</div>
          </DashboardLayout>
        </KeyboardShortcutsProvider>
      </MemoryRouter>,
    );

    expect(await screen.findAllByText("Connections")).not.toHaveLength(0);
  });
});
