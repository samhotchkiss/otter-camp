UPDATE tool_definition
SET description = 'Mark canonical bootstrap setup checklist steps as persisted in the current project bootstrap session once their outputs have been recorded. When persisting select-first-wave and multiple executable tasks exist, include the exact selected first-wave tasks via first_wave_task_ids or first_wave_task_numbers so later-wave work stays draft.',
    input_schema = '{
      "type": "object",
      "properties": {
        "project_id": {"type": "string", "format": "uuid"},
        "completed_step_slugs": {
          "type": "array",
          "items": {"type": "string"},
          "minItems": 1
        },
        "first_wave_task_ids": {
          "type": "array",
          "items": {"type": "string", "format": "uuid"}
        },
        "first_wave_task_numbers": {
          "type": "array",
          "items": {"type": "string"}
        },
        "sign_off_summary": {"type": "string"}
      },
      "required": ["completed_step_slugs"]
    }'::jsonb
WHERE name = 'bootstrap.setup.persist';
