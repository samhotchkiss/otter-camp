import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import LoginPage from "./LoginPage";

const { mockLogin } = vi.hoisted(() => ({
  mockLogin: vi.fn(),
}));

vi.mock("../contexts/AuthContext", () => ({
  useAuth: () => ({
    login: mockLogin,
  }),
}));

describe("LoginPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("defaults to local onboarding mode", () => {
    render(<LoginPage />);

    const localButton = screen.getByRole("button", { name: /local setup/i });
    const hostedButton = screen.getByRole("button", { name: /hosted setup/i });

    expect(localButton).toHaveAttribute("aria-pressed", "true");
    expect(hostedButton).toHaveAttribute("aria-pressed", "false");

    expect(screen.getByLabelText(/name/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/organization/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/email address/i)).toBeInTheDocument();
  });

  it("shows hosted setup guidance when hosted mode is selected", async () => {
    const user = userEvent.setup();
    render(<LoginPage />);

    await user.click(screen.getByRole("button", { name: /hosted setup/i }));

    expect(screen.getByText(/hosted onboarding is moving to otter\.camp\/setup/i)).toBeInTheDocument();

    const hostedLink = screen.getByRole("link", { name: /go to hosted setup/i });
    expect(hostedLink).toHaveAttribute("href", "https://otter.camp/setup");

    expect(screen.queryByLabelText(/name/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/email address/i)).not.toBeInTheDocument();
  });
});
