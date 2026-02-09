import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  formatQuestionnaireForFallback,
  normalizeQuestionnairePayload,
  parseNumberedAnswers,
  parseNumberedQuestionnaireResponse,
  parseQuestionnaireAnswer,
  type QuestionnairePayload,
  type QuestionnaireQuestion,
} from "./openclaw-bridge";

describe("bridge questionnaire helpers", () => {
  it("formats fallback text for all questionnaire field types", () => {
    const questionnaire: QuestionnairePayload = {
      id: "qn-1",
      title: "Design intake",
      author: "Planner",
      questions: [
        { id: "q1", text: "Text", type: "text", required: true },
        { id: "q2", text: "Textarea", type: "textarea" },
        { id: "q3", text: "Boolean", type: "boolean" },
        { id: "q4", text: "Select", type: "select", options: ["A", "B"] },
        { id: "q5", text: "Multiselect", type: "multiselect", options: ["X", "Y"] },
        { id: "q6", text: "Number", type: "number" },
        { id: "q7", text: "Date", type: "date" },
      ],
    };

    const rendered = formatQuestionnaireForFallback(questionnaire);
    assert.ok(rendered.includes("[QUESTIONNAIRE]"));
    assert.ok(rendered.includes("Questionnaire ID: qn-1"));
    assert.ok(rendered.includes("Title: Design intake"));
    assert.ok(rendered.includes("Author: Planner"));
    assert.ok(rendered.includes("1. Text [id=q1; text, required]"));
    assert.ok(rendered.includes("2. Textarea [id=q2; textarea]"));
    assert.ok(rendered.includes("3. Boolean [id=q3; boolean]"));
    assert.ok(rendered.includes("4. Select [id=q4; select]"));
    assert.ok(rendered.includes("options: A | B"));
    assert.ok(rendered.includes("5. Multiselect [id=q5; multiselect]"));
    assert.ok(rendered.includes("options: X | Y"));
    assert.ok(rendered.includes("6. Number [id=q6; number]"));
    assert.ok(rendered.includes("7. Date [id=q7; date]"));
  });

  it("parses numbered answers with multiline bodies and ignores 0-index entries", () => {
    const answers = parseNumberedAnswers(
      [
        "0. ignored",
        "1. first line",
        "continuation line",
        "2) second",
        "3. third",
        "third continuation",
      ].join("\n"),
    );

    assert.equal(answers.has(0), false);
    assert.equal(answers.get(1), "first line\ncontinuation line");
    assert.equal(answers.get(2), "second");
    assert.equal(answers.get(3), "third\nthird continuation");
  });

  it("parses individual questionnaire answers by type", () => {
    const boolQuestion: QuestionnaireQuestion = { id: "q1", text: "Boolean", type: "boolean" };
    const numberQuestion: QuestionnaireQuestion = { id: "q2", text: "Number", type: "number" };
    const selectQuestion: QuestionnaireQuestion = {
      id: "q3",
      text: "Select",
      type: "select",
      options: ["WebSocket", "Polling"],
    };
    const multiselectQuestion: QuestionnaireQuestion = {
      id: "q4",
      text: "Multi",
      type: "multiselect",
      options: ["Desktop", "Mobile"],
    };

    assert.deepEqual(parseQuestionnaireAnswer(boolQuestion, "yes"), { value: true, valid: true });
    assert.deepEqual(parseQuestionnaireAnswer(numberQuestion, "1.5"), { value: 1.5, valid: true });
    assert.deepEqual(parseQuestionnaireAnswer(selectQuestion, "websocket"), {
      value: "WebSocket",
      valid: true,
    });
    assert.deepEqual(parseQuestionnaireAnswer(multiselectQuestion, "desktop, mobile | desktop"), {
      value: ["Desktop", "Mobile"],
      valid: true,
    });
    assert.equal(parseQuestionnaireAnswer(numberQuestion, "not-a-number").valid, false);
  });

  it("maps numbered responses to questionnaire ids with typed values", () => {
    const questionnaire: QuestionnairePayload = {
      id: "qn-typed",
      questions: [
        { id: "q1", text: "Enable?", type: "boolean", required: true },
        { id: "q2", text: "Latency", type: "number", required: true },
        {
          id: "q3",
          text: "Transport",
          type: "select",
          required: true,
          options: ["WebSocket", "Polling"],
        },
        {
          id: "q4",
          text: "Platforms",
          type: "multiselect",
          options: ["Desktop", "Mobile"],
        },
      ],
    };

    const parsed = parseNumberedQuestionnaireResponse(
      ["1. yes", "2. 1.5", "3. websocket", "4. desktop, mobile"].join("\n"),
      questionnaire,
    );

    assert.notEqual(parsed, null);
    assert.equal(parsed?.highConfidence, true);
    assert.deepEqual(parsed?.responses, {
      q1: true,
      q2: 1.5,
      q3: "WebSocket",
      q4: ["Desktop", "Mobile"],
    });
  });

  it("normalizes valid questionnaire payloads and rejects invalid payloads", () => {
    assert.equal(normalizeQuestionnairePayload(null), null);
    assert.equal(normalizeQuestionnairePayload({ id: "qn-1", questions: [] }), null);
    assert.equal(
      normalizeQuestionnairePayload({ questions: [{ id: "q1", text: "Q", type: "text" }] }),
      null,
    );

    const normalized = normalizeQuestionnairePayload({
      id: " qn-2 ",
      context_type: "project_chat",
      context_id: "project-1",
      author: "Planner",
      title: " Intake ",
      questions: [
        { id: "q1", text: "Protocol?", type: "select", options: ["A", "A", " B "] },
        { id: "q2", text: "Enabled?", type: "boolean", required: true },
        { id: "q3", text: "Skip", type: "bad-type" },
      ],
    });

    assert.deepEqual(normalized, {
      id: "qn-2",
      contextType: "project_chat",
      contextID: "project-1",
      author: "Planner",
      title: "Intake",
      questions: [
        { id: "q1", text: "Protocol?", type: "select", required: undefined, options: ["A", "B"] },
        { id: "q2", text: "Enabled?", type: "boolean", required: true, options: undefined },
      ],
      responses: undefined,
    });
  });
});
