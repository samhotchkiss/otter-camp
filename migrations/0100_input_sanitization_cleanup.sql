UPDATE agent
SET display_name = COALESCE(
    NULLIF(BTRIM(regexp_replace(display_name, '<[^>]*>', '', 'g')), ''),
    'Agent ' || SUBSTRING(id::text FROM 1 FOR 8)
)
WHERE display_name ~* '<\s*/?\s*[a-z][^>]*>';

UPDATE project
SET display_name = COALESCE(
    NULLIF(BTRIM(regexp_replace(display_name, '<[^>]*>', '', 'g')), ''),
    'Project ' || SUBSTRING(id::text FROM 1 FOR 8)
)
WHERE display_name ~* '<\s*/?\s*[a-z][^>]*>';

UPDATE skill
SET display_name = COALESCE(
    NULLIF(BTRIM(regexp_replace(display_name, '<[^>]*>', '', 'g')), ''),
    'Skill ' || SUBSTRING(id::text FROM 1 FOR 8)
)
WHERE display_name ~* '<\s*/?\s*[a-z][^>]*>';

UPDATE human_user
SET display_name = COALESCE(
    NULLIF(BTRIM(regexp_replace(display_name, '<[^>]*>', '', 'g')), ''),
    'User ' || SUBSTRING(id::text FROM 1 FOR 8)
)
WHERE display_name ~* '<\s*/?\s*[a-z][^>]*>';

UPDATE organization
SET display_name = COALESCE(
    NULLIF(BTRIM(regexp_replace(display_name, '<[^>]*>', '', 'g')), ''),
    'Organization ' || SUBSTRING(id::text FROM 1 FOR 8)
)
WHERE display_name ~* '<\s*/?\s*[a-z][^>]*>';

UPDATE api_key
SET display_name = COALESCE(
    NULLIF(BTRIM(regexp_replace(display_name, '<[^>]*>', '', 'g')), ''),
    'API Key ' || SUBSTRING(id::text FROM 1 FOR 8)
)
WHERE display_name ~* '<\s*/?\s*[a-z][^>]*>';

UPDATE mcp_connection
SET display_name = COALESCE(
    NULLIF(BTRIM(regexp_replace(display_name, '<[^>]*>', '', 'g')), ''),
    'MCP Connection ' || SUBSTRING(id::text FROM 1 FOR 8)
)
WHERE display_name ~* '<\s*/?\s*[a-z][^>]*>';

UPDATE mcp_connection
SET
    is_enabled = false,
    status = 'failed',
    updated_at = now()
WHERE id = '0b391f7c-541d-4cef-8eb1-c8393810561d'
   OR LOWER(COALESCE(transport_config->>'url', '')) ~
      '^(https?://)?(169\\.254\\.|10\\.|127\\.|172\\.(1[6-9]|2[0-9]|3[01])\\.|192\\.168\\.|localhost|\\[::1\\])';
