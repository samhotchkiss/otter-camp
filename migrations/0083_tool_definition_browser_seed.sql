INSERT INTO tool_definition (
    name,
    display_name,
    description,
    tool_tier,
    tool_domain,
    required_capability,
    input_schema,
    is_enabled
) VALUES
    ('browser.navigate', 'Browser Navigate', 'Navigate browser to a URL', 'tier2', 'browser', 'browser.navigate', '{"type":"object"}'::jsonb, true),
    ('browser.click', 'Browser Click', 'Click an element in the browser', 'tier2', 'browser', 'system.browser.interact', '{"type":"object"}'::jsonb, true),
    ('browser.type', 'Browser Type', 'Type text into an element', 'tier2', 'browser', 'system.browser.interact', '{"type":"object"}'::jsonb, true),
    ('browser.select', 'Browser Select', 'Select an option from a control', 'tier2', 'browser', 'system.browser.interact', '{"type":"object"}'::jsonb, true),
    ('browser.hover', 'Browser Hover', 'Hover over an element', 'tier2', 'browser', 'system.browser.interact', '{"type":"object"}'::jsonb, true),
    ('browser.scroll', 'Browser Scroll', 'Scroll the current page', 'tier2', 'browser', 'system.browser.interact', '{"type":"object"}'::jsonb, true),
    ('browser.press_key', 'Browser Press Key', 'Send a keyboard key press', 'tier2', 'browser', 'system.browser.interact', '{"type":"object"}'::jsonb, true),
    ('browser.screenshot', 'Browser Screenshot', 'Capture a browser screenshot', 'tier2', 'browser', 'browser.read', '{"type":"object"}'::jsonb, true),
    ('browser.extract_text', 'Browser Extract Text', 'Extract text content from the page', 'tier2', 'browser', 'browser.read', '{"type":"object"}'::jsonb, true),
    ('browser.extract_structured', 'Browser Extract Structured', 'Extract structured data from the page', 'tier2', 'browser', 'browser.read', '{"type":"object"}'::jsonb, true),
    ('browser.get_page_info', 'Browser Get Page Info', 'Get metadata about the current page', 'tier2', 'browser', 'browser.read', '{"type":"object"}'::jsonb, true),
    ('browser.wait_for', 'Browser Wait For', 'Wait for a selector or condition', 'tier2', 'browser', 'browser.read', '{"type":"object"}'::jsonb, true),
    ('browser.wait_for_navigation', 'Browser Wait For Navigation', 'Wait for page navigation', 'tier2', 'browser', 'browser.navigate', '{"type":"object"}'::jsonb, true),
    ('browser.back', 'Browser Back', 'Navigate browser history back', 'tier2', 'browser', 'browser.navigate', '{"type":"object"}'::jsonb, true),
    ('browser.forward', 'Browser Forward', 'Navigate browser history forward', 'tier2', 'browser', 'browser.navigate', '{"type":"object"}'::jsonb, true),
    ('browser.refresh', 'Browser Refresh', 'Refresh the current page', 'tier2', 'browser', 'browser.navigate', '{"type":"object"}'::jsonb, true),
    ('browser.evaluate', 'Browser Evaluate', 'Evaluate JavaScript in the page context', 'tier2', 'browser', 'system.browser.interact', '{"type":"object"}'::jsonb, true)
ON CONFLICT (name) DO NOTHING;
