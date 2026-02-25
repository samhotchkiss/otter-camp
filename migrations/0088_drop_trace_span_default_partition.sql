DO $$
BEGIN
    IF to_regclass('trace_span_p_default') IS NULL THEN
        RETURN;
    END IF;

    IF EXISTS (SELECT 1 FROM trace_span_p_default LIMIT 1) THEN
        RAISE NOTICE 'trace_span_p_default not dropped because it contains data';
        RETURN;
    END IF;

    DROP TABLE trace_span_p_default;
END $$;
