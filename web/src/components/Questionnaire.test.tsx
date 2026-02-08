import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import Questionnaire from "./Questionnaire";
import type { MessageQuestionnaire } from "./messaging/types";

describe("Questionnaire", () => {
  it("prevents submit when required answers are missing", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    const questionnaire: MessageQuestionnaire = {
      id: "qn-1",
      context_type: "project_chat",
      context_id: "project-1",
      author: "Planner",
      title: "Required fields",
      questions: [
        {
          id: "q1",
          text: "Protocol?",
          type: "select",
          options: ["WebSocket", "Polling"],
          required: true,
        },
      ],
      created_at: "2026-02-08T00:00:00.000Z",
    };

    render(<Questionnaire questionnaire={questionnaire} onSubmit={onSubmit} />);
    await user.click(screen.getByRole("button", { name: "Submit" }));
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("submits normalized responses for answered fields", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const questionnaire: MessageQuestionnaire = {
      id: "qn-2",
      context_type: "project_chat",
      context_id: "project-1",
      author: "Planner",
      title: "Design decisions",
      questions: [
        {
          id: "q1",
          text: "Target response time?",
          type: "text",
          required: true,
        },
        {
          id: "q2",
          text: "Platforms",
          type: "multiselect",
          options: ["Desktop web", "Mobile web"],
          required: false,
        },
      ],
      created_at: "2026-02-08T00:00:00.000Z",
    };

    render(<Questionnaire questionnaire={questionnaire} onSubmit={onSubmit} />);

    await user.type(screen.getByLabelText("Target response time?"), "  under 2 seconds  ");
    await user.click(screen.getByLabelText("Desktop web"));
    await user.click(screen.getByRole("button", { name: "Submit" }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith({
        q1: "under 2 seconds",
        q2: ["Desktop web"],
      });
    });
  });
});
