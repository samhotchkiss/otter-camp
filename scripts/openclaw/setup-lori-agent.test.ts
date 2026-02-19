import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, it } from "node:test";
import { ensureLoriAgentConfig, ensureLoriWorkspaceScaffold, setupLoriAgent } from "./setup-lori-agent";

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

describe("ensureLoriWorkspaceScaffold", () => {
  it("creates SamsBrain files and workspace symlinks", () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "lori-workspace-create-"));
    const workspacePath = path.join(tempRoot, "workspace-lori");
    const samsBrainPath = path.join(tempRoot, "SamsBrain", "Agents", "Lori");

    const result = ensureLoriWorkspaceScaffold(workspacePath, samsBrainPath);
    assert.equal(result.changed, true);

    const requiredFiles = ["SOUL.md", "IDENTITY.md", "TOOLS.md"];
    for (const fileName of requiredFiles) {
      const samsBrainFile = path.join(samsBrainPath, fileName);
      const workspaceFile = path.join(workspacePath, fileName);
      assert.equal(fs.existsSync(samsBrainFile), true);
      assert.equal(fs.readFileSync(samsBrainFile, "utf8").trim().length > 0, true);
      assert.equal(fs.lstatSync(workspaceFile).isSymbolicLink(), true);
      assert.equal(fs.realpathSync(workspaceFile), fs.realpathSync(samsBrainFile));
    }
  });

  it("reconciles existing non-symlink workspace files into symlinks", () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "lori-workspace-reconcile-"));
    const workspacePath = path.join(tempRoot, "workspace-lori");
    const samsBrainPath = path.join(tempRoot, "SamsBrain", "Agents", "Lori");
    fs.mkdirSync(workspacePath, { recursive: true });
    fs.mkdirSync(samsBrainPath, { recursive: true });
    fs.writeFileSync(path.join(samsBrainPath, "SOUL.md"), "soul", "utf8");
    fs.writeFileSync(path.join(workspacePath, "SOUL.md"), "legacy workspace file", "utf8");

    const result = ensureLoriWorkspaceScaffold(workspacePath, samsBrainPath);
    assert.equal(result.changed, true);
    assert.equal(fs.lstatSync(path.join(workspacePath, "SOUL.md")).isSymbolicLink(), true);
    assert.equal(
      fs.realpathSync(path.join(workspacePath, "SOUL.md")),
      fs.realpathSync(path.join(samsBrainPath, "SOUL.md")),
    );
  });

  it("is idempotent after first scaffold run", () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "lori-workspace-idempotent-"));
    const workspacePath = path.join(tempRoot, "workspace-lori");
    const samsBrainPath = path.join(tempRoot, "SamsBrain", "Agents", "Lori");

    const first = ensureLoriWorkspaceScaffold(workspacePath, samsBrainPath);
    const second = ensureLoriWorkspaceScaffold(workspacePath, samsBrainPath);
    assert.equal(first.changed, true);
    assert.equal(second.changed, false);
  });
});

describe("setupLoriAgent", () => {
  it("applies config and workspace scaffolding in one call", () => {
    const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "lori-setup-all-"));
    const configPath = path.join(tempRoot, ".openclaw", "openclaw.json");
    const workspacePath = path.join(tempRoot, ".openclaw", "workspace-lori");
    const samsBrainPath = path.join(tempRoot, "Documents", "SamsBrain", "Agents", "Lori");

    const result = setupLoriAgent({
      configPath,
      workspacePath,
      samsBrainPath,
    });

    assert.equal(result.config.changed, true);
    assert.equal(result.workspace.changed, true);

    const config = readConfig(configPath);
    const agents = ((config.agents as { list?: Array<Record<string, unknown>> })?.list || []);
    assert.equal(agents.some((entry) => String(entry.id || "").trim() === "lori"), true);
    assert.equal(fs.lstatSync(path.join(workspacePath, "SOUL.md")).isSymbolicLink(), true);
  });
});
