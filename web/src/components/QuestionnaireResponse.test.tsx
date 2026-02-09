import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import QuestionnaireResponse from "./QuestionnaireResponse";
import type { MessageQuestionnaire } from "./messaging/types";

describe("QuestionnaireResponse", () => {
  it("renders formatted answered values", () => {
    const questionnaire: MessageQuestionnaire = {
      id: "qn-1",
      context_type: "issue",
      context_id: "issue-1",
      author: "Planner",
      title: "Deployment checklist",
      questions: [
        { id: "q1", text: "Protocol?", type: "select", options: ["WebSocket", "Polling"], required: true },
        { id: "q2", text: "Offline support?", type: "boolean", required: true },
        { id: "q3", text: "Platforms", type: "multiselect", options: ["Desktop web", "Mobile web"], required: false },
      ],
      responses: {
        q1: "WebSocket",
        q2: true,
        q3: ["Desktop web", "Mobile web"],
      },
      responded_by: "Sam",
      responded_at: "2026-02-08T01:00:00.000Z",
      created_at: "2026-02-08T00:00:00.000Z",
    };

    render(<QuestionnaireResponse questionnaire={questionnaire} />);

    expect(screen.getByText("WebSocket")).toBeInTheDocument();
    expect(screen.getByText("Yes")).toBeInTheDocument();
    expect(screen.getByText("Desktop web, Mobile web")).toBeInTheDocument();
    expect(screen.getByText(/Answered by Sam/i)).toBeInTheDocument();
  });
});
