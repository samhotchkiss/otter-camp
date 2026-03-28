UPDATE tool_definition
SET input_schema = '{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path relative to the workspace root."},
    "encoding": {"type": "string", "enum": ["utf8", "base64"]},
    "max_bytes": {"type": "integer", "minimum": 1},
    "offset_bytes": {"type": "integer", "minimum": 0}
  },
  "required": ["path"],
  "additionalProperties": false
}'::jsonb
WHERE name = 'file.read';
