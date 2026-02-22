# 20. Tools and Tool Policy

## Status: Stub — to be designed.

## Purpose

Define how agents discover, access, and execute tools. Unify the currently fragmented tool references across agent profiles (doc 05), the control plane (doc 16), MCP (doc 09), and system integration (doc 11).

## Key Questions to Resolve

- What is the tool taxonomy? How do OtterCamp-native tools (create task, advance flow, query Ellie), system tools (file I/O, shell), and external tools (MCP, browser) relate to each other?
- How does an agent get its tool set for a given session? Agent profile policy? Flow node declaration? Session scope?
- Which tool calls go through the control plane execution broker, and which are lightweight enough to bypass it?
- What native tools ship with OtterCamp before any MCP connections are configured?
- How do tool descriptions get included in the prompt (part of the prompt assembly pipeline in doc 05)?
- How does tool policy interact with the capability model in doc 16?

## Likely Tool Categories (to be designed)

- **OtterCamp tools**: task CRUD, flow advancement, blocker filing, memory queries (Ellie), agent management.
- **System tools**: file read/write, shell execution, codebase search.
- **Browser tools**: navigation, interaction, screenshot, extraction.
- **MCP tools**: dynamically discovered from connected MCP servers.

## Relationships

- Agent profile tool policy (doc 05) determines what an agent is allowed to use.
- Control plane capability model (doc 16) gates execution of sensitive tools.
- Flow node declarations (doc 03) may scope which tools are relevant for a step.
- Prompt assembly (doc 05) includes tool descriptions in the agent's context.
