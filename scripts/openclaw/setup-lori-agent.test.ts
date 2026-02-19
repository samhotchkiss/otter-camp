import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, it } from "node:test";
import { ensureLoriAgentConfig } from "./setup-lori-agent";

function writeConfig(configPath: string, payload: Record<string, unknown>): void {
  fs.mkdirSync(path.dirname(configPath), { recursive: true });
  fs.writeFileSync(configPath, `${JSON.stringify(payload, null, 2)}\n`, "utf8");
}

function readConfig(configPath: string): Record<string, unknown> {
  return JSON.parse(fs.readFileSync(configPath, "utf8")) as Record<string, unknown>;
}

describe("ensureLoriAgentConfig", () => {
  it("adds lori to agents.list when missing", () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "lori-config-add-"));
    const configPath = path.join(tempRoot, "openclaw.json");
    writeConfig(configPath, {
      agents: {
        list: [{ id: "main" }],
      },
    });

    const result = ensureLoriAgentConfig(configPath);
    assert.equal(result.changed, true);

    const updated = readConfig(configPath);
    const agents = ((updated.agents as { list?: Array<Record<string, unknown>> })?.list || []);
    const lori = agents.find((entry) => String(entry.id || "").trim() === "lori");
    assert.ok(lori);
    assert.equal(lori?.name, "Lori");
    assert.equal(lori?.model, "anthropic/claude-opus-4-6");
    assert.equal(lori?.workspace, "~/.openclaw/workspace-lori");
  });

  it("updates an existing lori entry to required values", () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "lori-config-update-"));
    const configPath = path.join(tempRoot, "openclaw.json");
    writeConfig(configPath, {
      agents: {
        list: [
          { id: "main" },
          {
            id: "lori",
            name: "Wrong",
            model: "anthropic/claude-sonnet-4-6",
            workspace: "/tmp/wrong",
          },
        ],
      },
    });

    const result = ensureLoriAgentConfig(configPath);
    assert.equal(result.changed, true);

    const updated = readConfig(configPath);
    const agents = ((updated.agents as { list?: Array<Record<string, unknown>> })?.list || []);
    const lori = agents.find((entry) => String(entry.id || "").trim() === "lori");
    assert.equal(lori?.name, "Lori");
    assert.equal(lori?.model, "anthropic/claude-opus-4-6");
    assert.equal(lori?.workspace, "~/.openclaw/workspace-lori");
  });

  it("is idempotent on repeat runs", () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "lori-config-idempotent-"));
    const configPath = path.join(tempRoot, "openclaw.json");
    writeConfig(configPath, {
      agents: {
        list: [{ id: "main" }],
      },
    });

    const first = ensureLoriAgentConfig(configPath);
    const firstContent = fs.readFileSync(configPath, "utf8");
    const second = ensureLoriAgentConfig(configPath);
    const secondContent = fs.readFileSync(configPath, "utf8");

    assert.equal(first.changed, true);
    assert.equal(second.changed, false);
    assert.equal(secondContent, firstContent);
  });
});
