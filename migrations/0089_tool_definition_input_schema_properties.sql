-- Migration 0089: Add proper input_schema properties to native tool definitions
-- Previously all native tools had '{"type":"object"}' with no properties,
-- making it impossible for LLM models to know what parameters to pass.

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"File path to read"},"max_bytes":{"type":"integer","description":"Maximum bytes to read (default 1MB, max 10MB)"},"encoding":{"type":"string","enum":["utf8","base64"],"description":"Content encoding (default utf8)"}},"required":["path"],"additionalProperties":false}'::jsonb WHERE name = 'file.read';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"Directory path to list (default: workspace root)"},"pattern":{"type":"string","description":"Glob pattern for filtering (default *)"},"recursive":{"type":"boolean","description":"Recursive listing (default false)"}},"additionalProperties":false}'::jsonb WHERE name = 'file.list';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"Directory to search in (default: workspace root)"},"pattern":{"type":"string","description":"Regex pattern to match file contents"},"file_pattern":{"type":"string","description":"Glob pattern to filter files (default *)"},"case_insensitive":{"type":"boolean","description":"Case insensitive matching (default false)"},"recursive":{"type":"boolean","description":"Recursive search (default true)"},"max_results":{"type":"integer","description":"Maximum results to return (default 100, max 1000)"}},"required":["pattern"],"additionalProperties":false}'::jsonb WHERE name = 'file.search';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"File path to write"},"content":{"type":"string","description":"Content to write"},"encoding":{"type":"string","enum":["utf8","base64"],"description":"Content encoding (default utf8)"},"create_dirs":{"type":"boolean","description":"Create parent directories if missing (default false)"}},"required":["path"],"additionalProperties":false}'::jsonb WHERE name = 'file.write';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"File path to edit"},"old_string":{"type":"string","description":"Exact text to find and replace"},"new_string":{"type":"string","description":"Replacement text (default empty string)"},"replace_all":{"type":"boolean","description":"Replace all occurrences (default false, replaces first)"}},"required":["path","old_string"],"additionalProperties":false}'::jsonb WHERE name = 'file.edit';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"File path to delete"}},"required":["path"],"additionalProperties":false}'::jsonb WHERE name = 'file.delete';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"Git repository path"}},"required":["path"],"additionalProperties":false}'::jsonb WHERE name = 'git.status';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"Git repository path"},"staged":{"type":"boolean","description":"Show staged changes (default false)"},"ref":{"type":"string","description":"Compare against ref (branch/tag/commit)"},"file_path":{"type":"string","description":"Show diff for a specific file only"}},"required":["path"],"additionalProperties":false}'::jsonb WHERE name = 'git.diff';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"Git repository path"},"limit":{"type":"integer","description":"Number of commits to return (default 10, max 100)"},"since":{"type":"string","description":"Only commits after this date (ISO 8601)"},"author":{"type":"string","description":"Filter by author name or email"},"file_path":{"type":"string","description":"Show commits for specific file"}},"required":["path"],"additionalProperties":false}'::jsonb WHERE name = 'git.log';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"Git repository path"},"message":{"type":"string","description":"Commit message"},"all":{"type":"boolean","description":"Stage all tracked changes before committing (default false)"},"paths":{"type":"array","items":{"type":"string"},"description":"Specific file paths to stage and commit"}},"required":["path","message"],"additionalProperties":false}'::jsonb WHERE name = 'git.commit';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"path":{"type":"string","description":"Git repository path"},"remote":{"type":"string","description":"Remote name (default origin)"},"branch":{"type":"string","description":"Branch to push (auto-detected from HEAD if omitted)"},"force":{"type":"boolean","description":"Force push (default false; blocked for main/master/shared/*)"}},"required":["path"],"additionalProperties":false}'::jsonb WHERE name = 'git.push';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"query":{"type":"string","description":"Natural language search query"},"limit":{"type":"integer","description":"Maximum results to return (default 20, max 100)"},"scope":{"type":"string","enum":["org","project","agent"],"description":"Memory scope to search"}},"required":["query"],"additionalProperties":false}'::jsonb WHERE name = 'memory.query';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"content":{"type":"string","description":"Memory content to record"},"scope":{"type":"string","enum":["org","project","agent"],"description":"Memory scope (default org)"},"sensitivity":{"type":"string","enum":["normal","sensitive"],"description":"Sensitivity level (default normal)"},"tags":{"type":"array","items":{"type":"string"},"description":"Optional tags for categorization"}},"required":["content"],"additionalProperties":false}'::jsonb WHERE name = 'memory.record';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"cursor":{"type":"string","description":"Pagination cursor from previous response"},"limit":{"type":"integer","description":"Maximum results (default 50, max 200)"}},"additionalProperties":false}'::jsonb WHERE name = 'project.list';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"project_id":{"type":"string","format":"uuid","description":"Project ID"}},"required":["project_id"],"additionalProperties":false}'::jsonb WHERE name = 'project.get';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"name":{"type":"string","description":"Project display name"},"slug":{"type":"string","description":"URL-friendly slug (auto-generated if omitted)"},"description":{"type":"string","description":"Project description"},"delivery_mode":{"type":"string","enum":["gated","direct"],"description":"Delivery mode (default gated)"}},"required":["name"],"additionalProperties":false}'::jsonb WHERE name = 'project.create';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"project_id":{"type":"string","format":"uuid","description":"Project ID to update"},"name":{"type":"string","description":"New display name"},"description":{"type":"string","description":"New description"},"delivery_mode":{"type":"string","enum":["gated","direct"],"description":"New delivery mode"}},"required":["project_id"],"additionalProperties":false}'::jsonb WHERE name = 'project.update';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"project_id":{"type":"string","format":"uuid","description":"Filter by project ID"},"status":{"type":"string","description":"Filter by work status (e.g. todo, in_progress, done)"},"cursor":{"type":"string","description":"Pagination cursor"},"limit":{"type":"integer","description":"Maximum results (default 50, max 200)"}},"additionalProperties":false}'::jsonb WHERE name = 'task.list';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"task_id":{"type":"string","format":"uuid","description":"Task ID"}},"required":["task_id"],"additionalProperties":false}'::jsonb WHERE name = 'task.get';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"project_id":{"type":"string","format":"uuid","description":"Project ID to create task in"},"title":{"type":"string","description":"Task title"},"description":{"type":"string","description":"Task description"},"flow_template_id":{"type":"string","format":"uuid","description":"Flow template to apply"},"requires_human_review":{"type":"boolean","description":"Whether task requires human review (default false)"}},"required":["project_id","title"],"additionalProperties":false}'::jsonb WHERE name = 'task.create';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"task_id":{"type":"string","format":"uuid","description":"Task ID to update"},"title":{"type":"string","description":"New title"},"description":{"type":"string","description":"New description"},"work_status":{"type":"string","description":"New work status (e.g. todo, in_progress, done, blocked)"}},"required":["task_id"],"additionalProperties":false}'::jsonb WHERE name = 'task.update';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"source_id":{"type":"string","format":"uuid","description":"Task that depends on another"},"source_type":{"type":"string","description":"Type of source (default task)"},"depends_on_id":{"type":"string","format":"uuid","description":"Task being depended on"},"depends_on_type":{"type":"string","description":"Type of dependency (default task)"}},"required":["source_id","depends_on_id"],"additionalProperties":false}'::jsonb WHERE name = 'task.add_dependency';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"dependency_id":{"type":"string","format":"uuid","description":"Dependency relationship ID to remove"}},"required":["dependency_id"],"additionalProperties":false}'::jsonb WHERE name = 'task.remove_dependency';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"status":{"type":"string","enum":["all","pending","actioned"],"description":"Filter by status (default all)"},"limit":{"type":"integer","description":"Maximum results (default 50, max 200)"}},"additionalProperties":false}'::jsonb WHERE name = 'inbox.list';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"cursor":{"type":"string","description":"Pagination cursor"},"limit":{"type":"integer","description":"Maximum results (default 50, max 200)"},"scope_type":{"type":"string","enum":["organization","project","project_task"],"description":"Filter by scope type"},"scope_id":{"type":"string","format":"uuid","description":"Filter by scope ID"},"status":{"type":"string","description":"Filter by session status"}},"additionalProperties":false}'::jsonb WHERE name = 'session.list';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"session_id":{"type":"string","format":"uuid","description":"Session ID"}},"required":["session_id"],"additionalProperties":false}'::jsonb WHERE name = 'session.get';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"session_id":{"type":"string","format":"uuid","description":"Session ID"},"limit":{"type":"integer","description":"Maximum messages (default 100, max 500)"},"before_sequence":{"type":"integer","description":"Return messages before this sequence number"}},"required":["session_id"],"additionalProperties":false}'::jsonb WHERE name = 'session.history';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"scope_type":{"type":"string","enum":["organization","project","project_task"],"description":"Scope type"},"scope_id":{"type":"string","format":"uuid","description":"Scope ID (org/project/task ID)"},"mode":{"type":"string","enum":["sync","async"],"description":"Session mode (default async)"},"title":{"type":"string","description":"Session title"}},"required":["scope_type","scope_id"],"additionalProperties":false}'::jsonb WHERE name = 'session.create';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"session_id":{"type":"string","format":"uuid","description":"Session ID"},"agent_id":{"type":"string","format":"uuid","description":"Agent ID to invite"}},"required":["session_id","agent_id"],"additionalProperties":false}'::jsonb WHERE name = 'session.invite_agent';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"session_id":{"type":"string","format":"uuid","description":"Session ID to send message to"},"content":{"type":"string","description":"Message content"},"role":{"type":"string","enum":["user","assistant"],"description":"Message role (default user)"}},"required":["session_id","content"],"additionalProperties":false}'::jsonb WHERE name = 'message.send';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"cursor":{"type":"string","description":"Pagination cursor"},"limit":{"type":"integer","description":"Maximum results (default 50, max 200)"},"class":{"type":"string","description":"Filter by agent class"},"status":{"type":"string","description":"Filter by lifecycle status"}},"additionalProperties":false}'::jsonb WHERE name = 'agent.list';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"agent_id":{"type":"string","format":"uuid","description":"Agent ID"}},"required":["agent_id"],"additionalProperties":false}'::jsonb WHERE name = 'agent.get';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"name":{"type":"string","description":"Agent display name"},"system_prompt":{"type":"string","description":"System prompt / instructions"},"scope_type":{"type":"string","enum":["project"],"description":"Must be project"},"scope_id":{"type":"string","format":"uuid","description":"Project ID"},"ttl_seconds":{"type":"integer","description":"Time-to-live in seconds before automatic removal"}},"required":["name","scope_id"],"additionalProperties":false}'::jsonb WHERE name = 'agent.create_temp';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"agent_id":{"type":"string","format":"uuid","description":"Agent ID to update"},"system_prompt":{"type":"string","description":"New system prompt"},"operator_instructions":{"type":"string","description":"New operator instructions"},"tool_allow_list":{"type":"array","items":{"type":"string"},"description":"Tool names to allow"},"tool_deny_list":{"type":"array","items":{"type":"string"},"description":"Tool names to deny"}},"required":["agent_id"],"additionalProperties":false}'::jsonb WHERE name = 'agent.update';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"flow_template_id":{"type":"string","format":"uuid","description":"Flow template ID"}},"required":["flow_template_id"],"additionalProperties":false}'::jsonb WHERE name = 'flow.get_template';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"flow_node_execution_id":{"type":"string","format":"uuid","description":"Flow node execution ID"}},"required":["flow_node_execution_id"],"additionalProperties":false}'::jsonb WHERE name = 'flow.get_execution';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"flow_node_execution_id":{"type":"string","format":"uuid","description":"Flow node execution ID to advance"},"commit_sha":{"type":"string","description":"Git commit SHA to record (if applicable)"}},"required":["flow_node_execution_id"],"additionalProperties":false}'::jsonb WHERE name = 'flow.advance';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"flow_node_execution_id":{"type":"string","format":"uuid","description":"Flow node execution ID"},"decision":{"type":"string","enum":["approve","reject"],"description":"Review decision"}},"required":["flow_node_execution_id","decision"],"additionalProperties":false}'::jsonb WHERE name = 'flow.review_decision';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"project_id":{"type":"string","format":"uuid","description":"Project ID"},"name":{"type":"string","description":"Template display name"},"nodes":{"type":"array","items":{"type":"object","properties":{"display_name":{"type":"string"},"node_type":{"type":"string"},"tool_domains":{"type":"array","items":{"type":"string"}}},"required":["display_name","node_type"]},"description":"Flow node definitions"}},"required":["project_id","name"],"additionalProperties":false}'::jsonb WHERE name = 'flow.create_template';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"flow_node_execution_id":{"type":"string","format":"uuid","description":"Flow node execution ID to create subtask in"},"title":{"type":"string","description":"Subtask title"},"description":{"type":"string","description":"Subtask description"},"assignee_type":{"type":"string","description":"Assignee type (agent or user)"},"assignee_id":{"type":"string","format":"uuid","description":"Assignee ID"}},"required":["flow_node_execution_id","title"],"additionalProperties":false}'::jsonb WHERE name = 'subtask.create';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"subtask_id":{"type":"string","format":"uuid","description":"Subtask ID to update"},"status":{"type":"string","description":"New work status"},"title":{"type":"string","description":"New title"},"description":{"type":"string","description":"New description"}},"required":["subtask_id"],"additionalProperties":false}'::jsonb WHERE name = 'subtask.update';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"project_id":{"type":"string","format":"uuid","description":"Project ID"}},"required":["project_id"],"additionalProperties":false}'::jsonb WHERE name = 'schedule.list';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"project_id":{"type":"string","format":"uuid","description":"Project ID"},"flow_template_id":{"type":"string","format":"uuid","description":"Flow template to run on schedule"},"cron":{"type":"string","description":"Cron expression (e.g. 0 9 * * 1-5)"},"overlap_policy":{"type":"string","enum":["skip","queue"],"description":"How to handle overlapping runs (default skip)"},"max_duration_ms":{"type":"integer","description":"Maximum run duration in milliseconds"}},"required":["project_id","flow_template_id","cron"],"additionalProperties":false}'::jsonb WHERE name = 'schedule.create';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"schedule_id":{"type":"string","format":"uuid","description":"Schedule ID to update"},"cron":{"type":"string","description":"New cron expression"},"overlap_policy":{"type":"string","enum":["skip","queue"],"description":"New overlap policy"}},"required":["schedule_id"],"additionalProperties":false}'::jsonb WHERE name = 'schedule.update';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"schedule_id":{"type":"string","format":"uuid","description":"Schedule ID to delete"}},"required":["schedule_id"],"additionalProperties":false}'::jsonb WHERE name = 'schedule.delete';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"project_id":{"type":"string","format":"uuid","description":"Project ID"}},"required":["project_id"],"additionalProperties":false}'::jsonb WHERE name = 'merge_queue.status';

-- CLI execute is a passthrough tool; no fixed schema
UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"command":{"type":"string","description":"CLI command to execute"},"args":{"type":"array","items":{"type":"string"},"description":"Command arguments"},"timeout_seconds":{"type":"integer","description":"Timeout in seconds"}},"required":["command"],"additionalProperties":false}'::jsonb WHERE name = 'cli.execute';

-- Email compose (action review)
UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"to":{"type":"string","description":"Recipient email address"},"subject":{"type":"string","description":"Email subject"},"body":{"type":"string","description":"Email body content"},"cc":{"type":"string","description":"CC recipient"}},"required":["to","subject","body"],"additionalProperties":false}'::jsonb WHERE name = 'email.compose';

-- Slack post (action review)
UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"channel":{"type":"string","description":"Slack channel (e.g. #general)"},"message":{"type":"string","description":"Message text"},"thread_ts":{"type":"string","description":"Thread timestamp for replies"}},"required":["channel","message"],"additionalProperties":false}'::jsonb WHERE name = 'slack.post';

-- Browser tools
UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"url":{"type":"string","description":"URL to navigate to"}},"required":["url"],"additionalProperties":false}'::jsonb WHERE name = 'browser.navigate';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"selector":{"type":"string","description":"CSS selector or XPath for element to click"}},"required":["selector"],"additionalProperties":false}'::jsonb WHERE name = 'browser.click';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"selector":{"type":"string","description":"CSS selector or XPath for input element"},"text":{"type":"string","description":"Text to type"}},"required":["selector","text"],"additionalProperties":false}'::jsonb WHERE name = 'browser.type';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"selector":{"type":"string","description":"CSS selector for select element"},"value":{"type":"string","description":"Option value to select"}},"required":["selector","value"],"additionalProperties":false}'::jsonb WHERE name = 'browser.select';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"selector":{"type":"string","description":"CSS selector or XPath for element to hover over"}},"required":["selector"],"additionalProperties":false}'::jsonb WHERE name = 'browser.hover';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"key":{"type":"string","description":"Key to press (e.g. Enter, Tab, Escape, ArrowDown)"},"selector":{"type":"string","description":"CSS selector of focused element (optional)"}},"required":["key"],"additionalProperties":false}'::jsonb WHERE name = 'browser.press_key';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"direction":{"type":"string","enum":["up","down","left","right"],"description":"Scroll direction (default down)"},"amount":{"type":"integer","description":"Scroll amount in pixels (default 300)"},"selector":{"type":"string","description":"CSS selector of element to scroll (default window)"}},"additionalProperties":false}'::jsonb WHERE name = 'browser.scroll';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"selector":{"type":"string","description":"CSS selector of element to extract text from (default: body)"}},"additionalProperties":false}'::jsonb WHERE name = 'browser.extract_text';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"selector":{"type":"string","description":"CSS selector of element to extract"},"schema":{"type":"object","description":"JSON schema describing the structure to extract"}},"required":["schema"],"additionalProperties":false}'::jsonb WHERE name = 'browser.extract_structured';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"script":{"type":"string","description":"JavaScript to evaluate in browser context"}},"required":["script"],"additionalProperties":false}'::jsonb WHERE name = 'browser.evaluate';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"selector":{"type":"string","description":"CSS selector to wait for"},"timeout_ms":{"type":"integer","description":"Timeout in milliseconds (default 5000)"},"visible":{"type":"boolean","description":"Wait for element to be visible (default true)"}},"additionalProperties":false}'::jsonb WHERE name = 'browser.wait_for';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{"timeout_ms":{"type":"integer","description":"Timeout in milliseconds (default 5000)"}},"additionalProperties":false}'::jsonb WHERE name = 'browser.wait_for_navigation';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{},"additionalProperties":false}'::jsonb WHERE name = 'browser.screenshot';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{},"additionalProperties":false}'::jsonb WHERE name = 'browser.get_page_info';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{},"additionalProperties":false}'::jsonb WHERE name = 'browser.back';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{},"additionalProperties":false}'::jsonb WHERE name = 'browser.forward';

UPDATE tool_definition SET input_schema = '{"type":"object","properties":{},"additionalProperties":false}'::jsonb WHERE name = 'browser.refresh';
