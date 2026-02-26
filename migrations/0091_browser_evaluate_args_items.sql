-- Migration 0091: Fix browser.evaluate args schema missing items field
-- OpenAI function calling rejects arrays that don't specify an items schema.
-- The args parameter for browser.evaluate can hold any JSON values,
-- so we set items to an empty schema (accepts any value type).

UPDATE tool_definition
SET input_schema = jsonb_set(
    input_schema,
    '{properties,args}',
    '{"type": "array", "items": {}}'::jsonb
)
WHERE name = 'browser.evaluate'
  AND is_enabled = true
  AND (input_schema->'properties'->'args')::text = '{"type": "array"}';
