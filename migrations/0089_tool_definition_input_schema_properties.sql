WITH schema_updates(name, input_schema) AS (
    VALUES
        (
            'file.read',
            $$
            {
              "type": "object",
              "properties": {
                "path": {"type": "string", "description": "Path relative to the workspace root."},
                "encoding": {"type": "string", "enum": ["utf8", "base64"]},
                "max_bytes": {"type": "integer", "minimum": 1}
              },
              "required": ["path"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'file.list',
            $$
            {
              "type": "object",
              "properties": {
                "path": {"type": "string"},
                "pattern": {"type": "string"},
                "recursive": {"type": "boolean"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'file.search',
            $$
            {
              "type": "object",
              "properties": {
                "pattern": {"type": "string"},
                "path": {"type": "string"},
                "case_insensitive": {"type": "boolean"},
                "file_pattern": {"type": "string"},
                "recursive": {"type": "boolean"},
                "max_results": {"type": "integer", "minimum": 1}
              },
              "required": ["pattern"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'git.status',
            $$
            {
              "type": "object",
              "properties": {
                "path": {"type": "string"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'git.diff',
            $$
            {
              "type": "object",
              "properties": {
                "path": {"type": "string"},
                "ref": {"type": "string"},
                "staged": {"type": "boolean"},
                "file_path": {"type": "string"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'git.log',
            $$
            {
              "type": "object",
              "properties": {
                "path": {"type": "string"},
                "limit": {"type": "integer", "minimum": 1},
                "since": {"type": "string"},
                "author": {"type": "string"},
                "file_path": {"type": "string"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'memory.query',
            $$
            {
              "type": "object",
              "properties": {
                "query": {"type": "string"},
                "scope": {"type": "string", "enum": ["org", "project", "agent"]},
                "limit": {"type": "integer", "minimum": 1}
              },
              "required": ["query"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'project.list',
            $$
            {
              "type": "object",
              "properties": {
                "cursor": {"type": "string"},
                "limit": {"type": "integer", "minimum": 1}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'project.get',
            $$
            {
              "type": "object",
              "properties": {
                "project_id": {"type": "string", "format": "uuid"}
              },
              "required": ["project_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'task.list',
            $$
            {
              "type": "object",
              "properties": {
                "project_id": {"type": "string", "format": "uuid"},
                "status": {"type": "string"},
                "limit": {"type": "integer", "minimum": 1},
                "cursor": {"type": "string"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'task.get',
            $$
            {
              "type": "object",
              "properties": {
                "task_id": {"type": "string", "format": "uuid"}
              },
              "required": ["task_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'inbox.list',
            $$
            {
              "type": "object",
              "properties": {
                "status": {"type": "string", "enum": ["pending", "actioned", "all"]},
                "limit": {"type": "integer", "minimum": 1},
                "cursor": {"type": "string"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'session.list',
            $$
            {
              "type": "object",
              "properties": {
                "scope_type": {"type": "string"},
                "scope_id": {"type": "string", "format": "uuid"},
                "status": {"type": "string"},
                "limit": {"type": "integer", "minimum": 1},
                "cursor": {"type": "string"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'session.get',
            $$
            {
              "type": "object",
              "properties": {
                "session_id": {"type": "string", "format": "uuid"}
              },
              "required": ["session_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'session.history',
            $$
            {
              "type": "object",
              "properties": {
                "session_id": {"type": "string", "format": "uuid"},
                "limit": {"type": "integer", "minimum": 1},
                "before_sequence": {"type": "integer", "minimum": 1}
              },
              "required": ["session_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'agent.list',
            $$
            {
              "type": "object",
              "properties": {
                "class": {"type": "string"},
                "status": {"type": "string"},
                "limit": {"type": "integer", "minimum": 1},
                "cursor": {"type": "string"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'agent.get',
            $$
            {
              "type": "object",
              "properties": {
                "agent_id": {"type": "string", "format": "uuid"}
              },
              "required": ["agent_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'flow.get_template',
            $$
            {
              "type": "object",
              "properties": {
                "flow_template_id": {"type": "string", "format": "uuid"}
              },
              "required": ["flow_template_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'flow.get_execution',
            $$
            {
              "type": "object",
              "properties": {
                "flow_node_execution_id": {"type": "string", "format": "uuid"}
              },
              "required": ["flow_node_execution_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'schedule.list',
            $$
            {
              "type": "object",
              "properties": {
                "project_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'merge_queue.status',
            $$
            {
              "type": "object",
              "properties": {
                "project_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'file.write',
            $$
            {
              "type": "object",
              "properties": {
                "path": {"type": "string"},
                "content": {"type": "string"},
                "encoding": {"type": "string", "enum": ["utf8", "base64"]},
                "create_dirs": {"type": "boolean"}
              },
              "required": ["path", "content"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'file.edit',
            $$
            {
              "type": "object",
              "properties": {
                "path": {"type": "string"},
                "old_string": {"type": "string"},
                "new_string": {"type": "string"},
                "replace_all": {"type": "boolean"}
              },
              "required": ["path", "old_string", "new_string"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'file.delete',
            $$
            {
              "type": "object",
              "properties": {
                "path": {"type": "string"}
              },
              "required": ["path"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'git.commit',
            $$
            {
              "type": "object",
              "properties": {
                "path": {"type": "string"},
                "message": {"type": "string"},
                "all": {"type": "boolean"},
                "paths": {"type": "array", "items": {"type": "string"}}
              },
              "required": ["message"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'git.push',
            $$
            {
              "type": "object",
              "properties": {
                "path": {"type": "string"},
                "remote": {"type": "string"},
                "branch": {"type": "string"},
                "force": {"type": "boolean"}
              },
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'cli.execute',
            $$
            {
              "type": "object",
              "properties": {
                "command": {"type": "string"},
                "working_directory": {"type": "string"},
                "timeout_seconds": {"type": "integer", "minimum": 1},
                "env_overrides": {"type": "object", "additionalProperties": {"type": "string"}}
              },
              "required": ["command"],
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'memory.record',
            $$
            {
              "type": "object",
              "properties": {
                "content": {"type": "string"},
                "scope": {"type": "string", "enum": ["org", "project", "agent"]},
                "sensitivity": {"type": "string", "enum": ["normal", "private", "secret"]},
                "tags": {"type": "array", "items": {"type": "string"}}
              },
              "required": ["content"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'project.create',
            $$
            {
              "type": "object",
              "properties": {
                "name": {"type": "string"},
                "slug": {"type": "string"},
                "description": {"type": "string"},
                "delivery_mode": {"type": "string"}
              },
              "required": ["name"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'project.update',
            $$
            {
              "type": "object",
              "properties": {
                "project_id": {"type": "string", "format": "uuid"},
                "name": {"type": "string"},
                "description": {"type": "string"},
                "delivery_mode": {"type": "string"}
              },
              "required": ["project_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'task.create',
            $$
            {
              "type": "object",
              "properties": {
                "project_id": {"type": "string", "format": "uuid"},
                "title": {"type": "string"},
                "description": {"type": "string"},
                "flow_template_id": {"type": "string", "format": "uuid"},
                "requires_human_review": {"type": "boolean"}
              },
              "required": ["project_id", "title"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'task.update',
            $$
            {
              "type": "object",
              "properties": {
                "task_id": {"type": "string", "format": "uuid"},
                "title": {"type": "string"},
                "description": {"type": "string"},
                "work_status": {"type": "string"}
              },
              "required": ["task_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'task.add_dependency',
            $$
            {
              "type": "object",
              "properties": {
                "source_type": {"type": "string"},
                "source_id": {"type": "string", "format": "uuid"},
                "depends_on_type": {"type": "string"},
                "depends_on_id": {"type": "string", "format": "uuid"}
              },
              "required": ["source_type", "source_id", "depends_on_type", "depends_on_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'task.remove_dependency',
            $$
            {
              "type": "object",
              "properties": {
                "dependency_id": {"type": "string", "format": "uuid"}
              },
              "required": ["dependency_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'subtask.create',
            $$
            {
              "type": "object",
              "properties": {
                "flow_node_execution_id": {"type": "string", "format": "uuid"},
                "title": {"type": "string"},
                "description": {"type": "string"},
                "assignee_type": {"type": "string"},
                "assignee_id": {"type": "string", "format": "uuid"}
              },
              "required": ["flow_node_execution_id", "title"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'subtask.update',
            $$
            {
              "type": "object",
              "properties": {
                "subtask_id": {"type": "string", "format": "uuid"},
                "status": {"type": "string"},
                "title": {"type": "string"},
                "description": {"type": "string"}
              },
              "required": ["subtask_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'flow.advance',
            $$
            {
              "type": "object",
              "properties": {
                "flow_node_execution_id": {"type": "string", "format": "uuid"},
                "commit_sha": {"type": "string"}
              },
              "required": ["flow_node_execution_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'flow.review_decision',
            $$
            {
              "type": "object",
              "properties": {
                "flow_node_execution_id": {"type": "string", "format": "uuid"},
                "decision": {"type": "string", "enum": ["approve", "reject"]}
              },
              "required": ["flow_node_execution_id", "decision"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'flow.create_template',
            $$
            {
              "type": "object",
              "properties": {
                "project_id": {"type": "string", "format": "uuid"},
                "name": {"type": "string"},
                "nodes": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "display_name": {"type": "string"},
                      "node_type": {"type": "string"},
                      "tool_domains": {"type": "array", "items": {"type": "string"}}
                    }
                  }
                }
              },
              "required": ["project_id", "name"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'schedule.create',
            $$
            {
              "type": "object",
              "properties": {
                "project_id": {"type": "string", "format": "uuid"},
                "flow_template_id": {"type": "string", "format": "uuid"},
                "cron": {"type": "string"},
                "overlap_policy": {"type": "string"},
                "max_duration_ms": {"type": "integer", "minimum": 1}
              },
              "required": ["project_id", "flow_template_id", "cron"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'schedule.update',
            $$
            {
              "type": "object",
              "properties": {
                "schedule_id": {"type": "string", "format": "uuid"},
                "cron": {"type": "string"},
                "overlap_policy": {"type": "string"}
              },
              "required": ["schedule_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'schedule.delete',
            $$
            {
              "type": "object",
              "properties": {
                "schedule_id": {"type": "string", "format": "uuid"}
              },
              "required": ["schedule_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'agent.create_temp',
            $$
            {
              "type": "object",
              "properties": {
                "name": {"type": "string"},
                "system_prompt": {"type": "string"},
                "scope_type": {"type": "string", "enum": ["project"]},
                "scope_id": {"type": "string", "format": "uuid"},
                "ttl_seconds": {"type": "integer", "minimum": 1}
              },
              "required": ["name", "scope_type", "scope_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'agent.update',
            $$
            {
              "type": "object",
              "properties": {
                "agent_id": {"type": "string", "format": "uuid"},
                "system_prompt": {"type": "string"},
                "operator_instructions": {"type": "string"},
                "tool_allow_list": {"type": "array", "items": {"type": "string"}},
                "tool_deny_list": {"type": "array", "items": {"type": "string"}}
              },
              "required": ["agent_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'session.create',
            $$
            {
              "type": "object",
              "properties": {
                "scope_type": {"type": "string"},
                "scope_id": {"type": "string", "format": "uuid"},
                "mode": {"type": "string"},
                "title": {"type": "string"}
              },
              "required": ["scope_type", "scope_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'session.invite_agent',
            $$
            {
              "type": "object",
              "properties": {
                "session_id": {"type": "string", "format": "uuid"},
                "agent_id": {"type": "string", "format": "uuid"}
              },
              "required": ["session_id", "agent_id"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'message.send',
            $$
            {
              "type": "object",
              "properties": {
                "session_id": {"type": "string", "format": "uuid"},
                "content": {"type": "string"},
                "role": {"type": "string"}
              },
              "required": ["session_id", "content"],
              "additionalProperties": false
            }
            $$::jsonb
        ),
        (
            'email.compose',
            $$
            {
              "type": "object",
              "properties": {
                "to": {"type": "array", "items": {"type": "string"}},
                "subject": {"type": "string"},
                "body": {"type": "string"}
              },
              "required": ["subject", "body"],
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'slack.post',
            $$
            {
              "type": "object",
              "properties": {
                "channel": {"type": "string"},
                "text": {"type": "string"}
              },
              "required": ["text"],
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.navigate',
            $$
            {
              "type": "object",
              "properties": {
                "url": {"type": "string"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "required": ["url"],
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.click',
            $$
            {
              "type": "object",
              "properties": {
                "selector": {"type": "string"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "required": ["selector"],
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.type',
            $$
            {
              "type": "object",
              "properties": {
                "selector": {"type": "string"},
                "text": {"type": "string"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "required": ["selector", "text"],
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.select',
            $$
            {
              "type": "object",
              "properties": {
                "selector": {"type": "string"},
                "value": {"type": "string"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "required": ["selector", "value"],
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.hover',
            $$
            {
              "type": "object",
              "properties": {
                "selector": {"type": "string"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "required": ["selector"],
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.scroll',
            $$
            {
              "type": "object",
              "properties": {
                "x": {"type": "integer"},
                "y": {"type": "integer"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.press_key',
            $$
            {
              "type": "object",
              "properties": {
                "key": {"type": "string"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "required": ["key"],
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.screenshot',
            $$
            {
              "type": "object",
              "properties": {
                "selector": {"type": "string"},
                "full_page": {"type": "boolean"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.extract_text',
            $$
            {
              "type": "object",
              "properties": {
                "selector": {"type": "string"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.extract_structured',
            $$
            {
              "type": "object",
              "properties": {
                "schema": {"type": "object"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.get_page_info',
            $$
            {
              "type": "object",
              "properties": {
                "session_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.wait_for',
            $$
            {
              "type": "object",
              "properties": {
                "selector": {"type": "string"},
                "timeout_ms": {"type": "integer", "minimum": 1},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "required": ["selector"],
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.wait_for_navigation',
            $$
            {
              "type": "object",
              "properties": {
                "timeout_ms": {"type": "integer", "minimum": 1},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.back',
            $$
            {
              "type": "object",
              "properties": {
                "session_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.forward',
            $$
            {
              "type": "object",
              "properties": {
                "session_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.refresh',
            $$
            {
              "type": "object",
              "properties": {
                "session_id": {"type": "string", "format": "uuid"}
              },
              "additionalProperties": true
            }
            $$::jsonb
        ),
        (
            'browser.evaluate',
            $$
            {
              "type": "object",
              "properties": {
                "script": {"type": "string"},
                "args": {"type": "array"},
                "session_id": {"type": "string", "format": "uuid"}
              },
              "required": ["script"],
              "additionalProperties": true
            }
            $$::jsonb
        )
)
UPDATE tool_definition td
SET input_schema = schema_updates.input_schema
FROM schema_updates
WHERE td.name = schema_updates.name;

UPDATE tool_definition
SET input_schema = jsonb_set(
    jsonb_set(COALESCE(input_schema, '{}'::jsonb), '{type}', '"object"'::jsonb, true),
    '{properties}',
    COALESCE(input_schema->'properties', '{}'::jsonb),
    true
)
WHERE is_enabled = true
  AND NOT (input_schema ? 'properties');
