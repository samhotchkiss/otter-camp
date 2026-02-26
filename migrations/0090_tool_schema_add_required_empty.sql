-- Migration 0090: Fix tool schemas for all-optional-parameter tools
-- Migration 0089 added additionalProperties:false to tools with all-optional params,
-- which causes OpenAI function calling to silently reject those tool definitions.
-- For tools with no required parameters, remove additionalProperties:false and
-- add an empty required array so OpenAI processes them correctly.

UPDATE tool_definition
SET input_schema = jsonb_set(
    input_schema - 'additionalProperties',
    '{required}',
    '[]'::jsonb
)
WHERE is_enabled = true
  AND input_schema ? 'additionalProperties'
  AND (input_schema->'additionalProperties')::text = 'false'
  AND NOT (input_schema ? 'required')
  AND input_schema ? 'properties';
