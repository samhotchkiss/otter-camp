UPDATE tool_definition
SET description = 'Execute a shell command in the project workspace sandbox. Use command for the full shell command. Relative file output via >, >>, and heredoc is supported when file tools are unavailable.',
    input_schema = $${
      "type": "object",
      "properties": {
        "command": {
          "type": "string",
          "description": "Required shell command. Provide the full command string. Relative file output via >, >>, or heredoc is supported inside the workspace."
        },
        "working_directory": {
          "type": "string",
          "description": "Optional workspace-relative working directory. Legacy aliases working_dir and cwd are normalized automatically."
        },
        "timeout_seconds": {
          "type": "integer",
          "minimum": 1,
          "description": "Optional timeout in seconds. Legacy alias timeout_ms is normalized automatically."
        },
        "env_overrides": {
          "type": "object",
          "additionalProperties": {
            "type": "string"
          },
          "description": "Optional environment variable overrides. Legacy alias env is normalized automatically."
        }
      },
      "required": ["command"],
      "additionalProperties": true
    }$$::jsonb
WHERE name = 'cli.execute';
