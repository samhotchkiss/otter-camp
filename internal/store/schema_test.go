package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func requirePQCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		require.Equal(t, code, string(pqErr.Code))
		return
	}
	require.Fail(t, "expected pq.Error", "got %T: %v", err, err)
}

func insertSchemaAgent(t *testing.T, db *sql.DB, orgID, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, 'active') RETURNING id",
		orgID,
		slug,
		"Agent "+slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertSchemaProject(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO projects (org_id, name, status) VALUES ($1, $2, 'active') RETURNING id",
		orgID,
		name,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertSchemaTask(t *testing.T, db *sql.DB, orgID string, projectID, agentID *string, title string) string {
	t.Helper()
	var projectValue interface{}
	var agentValue interface{}
	if projectID != nil {
		projectValue = *projectID
	}
	if agentID != nil {
		agentValue = *agentID
	}
	var id string
	err := db.QueryRow(
		"INSERT INTO tasks (org_id, project_id, assigned_agent_id, title, status, priority) VALUES ($1, $2, $3, $4, 'queued', 'P2') RETURNING id",
		orgID,
		projectValue,
		agentValue,
		title,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertSchemaIssue(t *testing.T, db *sql.DB, orgID, projectID, title, state, origin string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, state, origin)
		 VALUES ($1, $2, COALESCE((SELECT MAX(issue_number) + 1 FROM project_issues WHERE project_id = $2), 1), $3, $4, $5)
		 RETURNING id`,
		orgID,
		projectID,
		title,
		state,
		origin,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestSchemaMigrationsUpDown(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() {
		_ = db.Close()
	}()

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	m, err := migrate.New("file://"+getMigrationsDir(t), connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}
}

func TestMigration049WorkflowAgentFKUsesOnDeleteSetNull(t *testing.T) {
	path := filepath.Join(getMigrationsDir(t), "049_project_workflow_fields.up.sql")
	content, err := os.ReadFile(path)
	require.NoError(t, err)

	require.Contains(
		t,
		strings.ToLower(string(content)),
		"workflow_agent_id uuid references agents(id) on delete set null",
	)
}

func TestMigration058MemoryInfrastructureFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"058_create_memory_infrastructure.up.sql",
		"058_create_memory_infrastructure.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "058_create_memory_infrastructure.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "create extension if not exists vector")
	require.Contains(t, upContent, "create extension if not exists pgcrypto")
	require.Contains(t, upContent, "create table if not exists memory_entries")
	require.Contains(t, upContent, "create table if not exists memory_entry_embeddings")
	require.Contains(t, upContent, "create table if not exists shared_knowledge")
	require.Contains(t, upContent, "create table if not exists shared_knowledge_embeddings")
	require.Contains(t, upContent, "create table if not exists agent_memory_config")
	require.Contains(t, upContent, "create table if not exists compaction_events")
	require.Contains(t, upContent, "create table if not exists agent_teams")
	require.Contains(t, upContent, "create table if not exists working_memory")
	require.Contains(t, upContent, "create table if not exists memory_events")
	require.Contains(t, upContent, "on memory_events (org_id, event_type, created_at desc)")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "058_create_memory_infrastructure.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop table if exists memory_events")
	require.Contains(t, downContent, "drop table if exists working_memory")
	require.Contains(t, downContent, "drop table if exists memory_entries")
	require.Contains(t, downContent, "drop extension if exists vector")
}

func TestSchemaWorkflowAgentDeleteSetsProjectFieldNull(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "workflow-agent-fk-org")
	agentID := insertSchemaAgent(t, db, orgID, "workflow-fk-agent")
	projectID := createTestProject(t, db, orgID, "workflow-fk-project")

	_, err := db.Exec(
		`UPDATE projects
		 SET workflow_enabled = true,
		     workflow_schedule = $1::jsonb,
		     workflow_agent_id = $2
		 WHERE id = $3 AND org_id = $4`,
		`{"kind":"every","everyMs":600000}`,
		agentID,
		projectID,
		orgID,
	)
	require.NoError(t, err)

	_, err = db.Exec("DELETE FROM agents WHERE id = $1 AND org_id = $2", agentID, orgID)
	require.NoError(t, err)

	var workflowAgentID sql.NullString
	err = db.QueryRow(
		"SELECT workflow_agent_id FROM projects WHERE id = $1 AND org_id = $2",
		projectID,
		orgID,
	).Scan(&workflowAgentID)
	require.NoError(t, err)
	require.False(t, workflowAgentID.Valid)
}

func TestSchemaNativeIssueTablesCreateAndRollback(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	m, err := migrate.New("file://"+getMigrationsDir(t), connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	for _, table := range []string{
		"project_issues",
		"project_issue_github_links",
		"project_issue_sync_checkpoints",
	} {
		require.True(t, schemaTableExists(t, db, table), table)
	}

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	for _, table := range []string{
		"project_issues",
		"project_issue_github_links",
		"project_issue_sync_checkpoints",
	} {
		require.False(t, schemaTableExists(t, db, table), table)
	}
}

func TestSchemaIssueApprovalStateMigrationMapsLegacyStatuses(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	m, err := migrate.New("file://"+getMigrationsDir(t), connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	err = m.Steps(25)
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	orgID := createTestOrganization(t, db, "issue-approval-migration-org")
	projectID := createTestProject(t, db, orgID, "Issue Approval Migration Project")

	var openIssueID string
	err = db.QueryRow(
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, state, origin)
		 VALUES ($1, $2, 1, 'Open legacy issue', 'open', 'local')
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&openIssueID)
	require.NoError(t, err)

	var closedIssueID string
	err = db.QueryRow(
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, state, origin)
		 VALUES ($1, $2, 2, 'Closed legacy issue', 'closed', 'local')
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&closedIssueID)
	require.NoError(t, err)

	err = m.Steps(1)
	require.NoError(t, err)

	var openApprovalState string
	err = db.QueryRow(`SELECT approval_state FROM project_issues WHERE id = $1`, openIssueID).Scan(&openApprovalState)
	require.NoError(t, err)
	require.Equal(t, "draft", openApprovalState)

	var closedApprovalState string
	err = db.QueryRow(`SELECT approval_state FROM project_issues WHERE id = $1`, closedIssueID).Scan(&closedApprovalState)
	require.NoError(t, err)
	require.Equal(t, "approved", closedApprovalState)
}

func TestSchemaConnectionEventsTableCreateAndRollback(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	m, err := migrate.New("file://"+getMigrationsDir(t), connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	require.True(t, schemaTableExists(t, db, "connection_events"))

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	require.False(t, schemaTableExists(t, db, "connection_events"))
}

func TestSchemaMemoryInfrastructureTablesCreateAndRollback(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	m, err := migrate.New("file://"+getMigrationsDir(t), connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	var vectorExtension bool
	err = db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')`,
	).Scan(&vectorExtension)
	require.NoError(t, err)
	require.True(t, vectorExtension)

	for _, table := range []string{
		"memory_entries",
		"memory_entry_embeddings",
		"shared_knowledge",
		"shared_knowledge_embeddings",
		"agent_memory_config",
		"compaction_events",
		"agent_teams",
		"working_memory",
		"memory_events",
	} {
		require.True(t, schemaTableExists(t, db, table), table)
	}

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	for _, table := range []string{
		"memory_entries",
		"memory_entry_embeddings",
		"shared_knowledge",
		"shared_knowledge_embeddings",
		"agent_memory_config",
		"compaction_events",
		"agent_teams",
		"working_memory",
		"memory_events",
	} {
		require.False(t, schemaTableExists(t, db, table), table)
	}
}

func TestSchemaMemoryEntriesLifecycleColumnsAndDedupIndex(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	var hasStatus bool
	err := db.QueryRow(
		`SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'memory_entries'
			  AND column_name = 'status'
		)`,
	).Scan(&hasStatus)
	require.NoError(t, err)
	require.True(t, hasStatus)

	var hasContentHash bool
	err = db.QueryRow(
		`SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'memory_entries'
			  AND column_name = 'content_hash'
		)`,
	).Scan(&hasContentHash)
	require.NoError(t, err)
	require.True(t, hasContentHash)

	var dedupIndex sql.NullString
	err = db.QueryRow(
		`SELECT to_regclass('public.idx_memory_entries_dedup_active')::text`,
	).Scan(&dedupIndex)
	require.NoError(t, err)
	require.True(t, dedupIndex.Valid)
}

func schemaTableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var regclass sql.NullString
	err := db.QueryRow("SELECT to_regclass('public.' || $1)::text", name).Scan(&regclass)
	require.NoError(t, err)
	return regclass.Valid && regclass.String != ""
}

func TestSchemaForeignKeys(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "fk-org")
	projectID := insertSchemaProject(t, db, orgID, "FK Project")
	agentID := insertSchemaAgent(t, db, orgID, "fk-agent")
	_ = insertSchemaTask(t, db, orgID, &projectID, &agentID, "FK Task")

	var missingID string
	err := db.QueryRow("SELECT gen_random_uuid()::text").Scan(&missingID)
	require.NoError(t, err)

	_, err = db.Exec(
		"INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, 'active')",
		missingID,
		"bad-agent",
		"Bad Agent",
	)
	requirePQCode(t, err, "23503")

	_, err = db.Exec(
		"INSERT INTO tasks (org_id, project_id, title, status, priority) VALUES ($1, $2, $3, 'queued', 'P2')",
		orgID,
		missingID,
		"Bad Task",
	)
	requirePQCode(t, err, "23503")
}

func TestSchemaUniqueConstraints(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "uniq-org")
	otherOrgID := createTestOrganization(t, db, "uniq-org-2")

	_, err := db.Exec(
		"INSERT INTO tags (org_id, name, color) VALUES ($1, $2, $3)",
		orgID,
		"backend",
		"#fff",
	)
	require.NoError(t, err)

	_, err = db.Exec(
		"INSERT INTO tags (org_id, name, color) VALUES ($1, $2, $3)",
		orgID,
		"backend",
		"#000",
	)
	requirePQCode(t, err, "23505")

	_, err = db.Exec(
		"INSERT INTO tags (org_id, name, color) VALUES ($1, $2, $3)",
		otherOrgID,
		"backend",
		"#123",
	)
	require.NoError(t, err)

	_ = insertSchemaTask(t, db, orgID, nil, nil, "First")
	secondTaskID := insertSchemaTask(t, db, orgID, nil, nil, "Second")

	_, err = db.Exec("UPDATE tasks SET number = 1 WHERE id = $1", secondTaskID)
	requirePQCode(t, err, "23505")
}

func TestSchemaAgentMemoriesDailyUniqueIndexIncludesOrgScope(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	var indexDef string
	err := db.QueryRow(
		`SELECT indexdef
		 FROM pg_indexes
		 WHERE schemaname = 'public'
		   AND indexname = 'idx_agent_memories_daily_unique'`,
	).Scan(&indexDef)
	require.NoError(t, err)
	require.Contains(t, indexDef, "(org_id, agent_id, date)")
	require.Contains(t, strings.ToLower(indexDef), "where")
	require.Contains(t, indexDef, "kind")
}

func TestSchemaCascadeDeletes(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "cascade-org")
	agentID := insertSchemaAgent(t, db, orgID, "cascade-agent")
	projectID := insertSchemaProject(t, db, orgID, "Cascade Project")
	_ = insertSchemaTask(t, db, orgID, &projectID, &agentID, "Cascade Task")

	_, err := db.Exec("DELETE FROM organizations WHERE id = $1", orgID)
	require.NoError(t, err)

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM agents").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	err = db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)

	err = db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestSchemaCheckConstraints(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "check-org")

	_, err := db.Exec(
		"INSERT INTO tasks (org_id, title, status, priority) VALUES ($1, $2, $3, $4)",
		orgID,
		"Bad Status",
		"not_a_status",
		"P2",
	)
	requirePQCode(t, err, "23514")

	_, err = db.Exec(
		"INSERT INTO tasks (org_id, title, status, priority) VALUES ($1, $2, $3, $4)",
		orgID,
		"Bad Priority",
		"queued",
		"P9",
	)
	requirePQCode(t, err, "23514")
}

func TestSchemaIssueParticipantAndCommentConstraints(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "issue-schema-org")
	projectID := insertSchemaProject(t, db, orgID, "Issue Schema Project")
	ownerAgentID := insertSchemaAgent(t, db, orgID, "issue-owner")
	collabAgentID := insertSchemaAgent(t, db, orgID, "issue-collab")
	issueID := insertSchemaIssue(t, db, orgID, projectID, "Schema issue", "open", "local")

	_, err := db.Exec(
		`INSERT INTO project_issue_participants (org_id, issue_id, agent_id, role)
		 VALUES ($1, $2, $3, 'owner')`,
		orgID,
		issueID,
		ownerAgentID,
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO project_issue_participants (org_id, issue_id, agent_id, role)
		 VALUES ($1, $2, $3, 'owner')`,
		orgID,
		issueID,
		collabAgentID,
	)
	requirePQCode(t, err, "23505")

	_, err = db.Exec(
		`INSERT INTO project_issue_comments (org_id, issue_id, author_agent_id, body)
		 VALUES ($1, $2, $3, '')`,
		orgID,
		issueID,
		ownerAgentID,
	)
	requirePQCode(t, err, "23514")
}

func TestSchemaProjectChatAttachmentColumnsAndForeignKey(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	var hasProjectChatAttachments bool
	err := db.QueryRow(
		`SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'project_chat_messages'
			  AND column_name = 'attachments'
		)`,
	).Scan(&hasProjectChatAttachments)
	require.NoError(t, err)
	require.True(t, hasProjectChatAttachments)

	var hasAttachmentChatMessageID bool
	err = db.QueryRow(
		`SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'attachments'
			  AND column_name = 'chat_message_id'
		)`,
	).Scan(&hasAttachmentChatMessageID)
	require.NoError(t, err)
	require.True(t, hasAttachmentChatMessageID)

	var projectChatAttachmentsIdx sql.NullString
	err = db.QueryRow(
		`SELECT to_regclass('public.project_chat_messages_attachments_idx')::text`,
	).Scan(&projectChatAttachmentsIdx)
	require.NoError(t, err)
	require.True(t, projectChatAttachmentsIdx.Valid)

	var attachmentsChatMessageIdx sql.NullString
	err = db.QueryRow(
		`SELECT to_regclass('public.attachments_chat_message_idx')::text`,
	).Scan(&attachmentsChatMessageIdx)
	require.NoError(t, err)
	require.True(t, attachmentsChatMessageIdx.Valid)

	orgID := createTestOrganization(t, db, "chat-attachment-schema-org")
	projectID := insertSchemaProject(t, db, orgID, "Chat Attachment Schema Project")

	var chatMessageID string
	err = db.QueryRow(
		`INSERT INTO project_chat_messages (org_id, project_id, author, body)
		 VALUES ($1, $2, 'Sam', 'attachment test')
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&chatMessageID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO attachments (org_id, chat_message_id, filename, size_bytes, mime_type, storage_key, url)
		 VALUES ($1, $2, 'file.txt', 12, 'text/plain', $3, '/uploads/test/file.txt')`,
		orgID,
		chatMessageID,
		"schema-test-chat-message-link-"+chatMessageID,
	)
	require.NoError(t, err)

	var missingMessageID string
	err = db.QueryRow(`SELECT gen_random_uuid()::text`).Scan(&missingMessageID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO attachments (org_id, chat_message_id, filename, size_bytes, mime_type, storage_key, url)
		 VALUES ($1, $2, 'missing.txt', 1, 'text/plain', $3, '/uploads/test/missing.txt')`,
		orgID,
		missingMessageID,
		"schema-test-missing-chat-message-"+missingMessageID,
	)
	requirePQCode(t, err, "23503")
}
