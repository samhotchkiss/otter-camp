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

func TestMigration060ChatThreadsFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"060_create_chat_threads.up.sql",
		"060_create_chat_threads.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "060_create_chat_threads.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "create table if not exists chat_threads")
	require.Contains(t, upContent, "thread_key text not null")
	require.Contains(t, upContent, "thread_type text not null")
	require.Contains(t, upContent, "archived_at timestamptz")
	require.Contains(t, upContent, "auto_archived_reason text")
	require.Contains(t, upContent, "last_message_at timestamptz not null")
	require.Contains(t, upContent, "enable row level security")
	require.Contains(t, upContent, "chat_threads_org_isolation")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "060_create_chat_threads.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop table if exists chat_threads")
}

func TestSchemaChatThreadsRLSAndIndexes(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	var tableRegClass sql.NullString
	err := db.QueryRow(`SELECT to_regclass('public.chat_threads')::text`).Scan(&tableRegClass)
	require.NoError(t, err)
	require.True(t, tableRegClass.Valid)

	var activeIdx sql.NullString
	err = db.QueryRow(`SELECT to_regclass('public.chat_threads_user_active_idx')::text`).Scan(&activeIdx)
	require.NoError(t, err)
	require.True(t, activeIdx.Valid)

	var archivedIdx sql.NullString
	err = db.QueryRow(`SELECT to_regclass('public.chat_threads_user_archived_idx')::text`).Scan(&archivedIdx)
	require.NoError(t, err)
	require.True(t, archivedIdx.Valid)

	var issueIdx sql.NullString
	err = db.QueryRow(`SELECT to_regclass('public.chat_threads_issue_idx')::text`).Scan(&issueIdx)
	require.NoError(t, err)
	require.True(t, issueIdx.Valid)

	var projectIdx sql.NullString
	err = db.QueryRow(`SELECT to_regclass('public.chat_threads_project_idx')::text`).Scan(&projectIdx)
	require.NoError(t, err)
	require.True(t, projectIdx.Valid)

	var rlsEnabled bool
	err = db.QueryRow(
		`SELECT relrowsecurity
		 FROM pg_class
		 WHERE relname = 'chat_threads'`,
	).Scan(&rlsEnabled)
	require.NoError(t, err)
	require.True(t, rlsEnabled)
}

func TestMigration061ChatThreadsLengthLimitFilesExistAndContainConstraints(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"061_chat_threads_length_limits.up.sql",
		"061_chat_threads_length_limits.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "061_chat_threads_length_limits.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "chat_threads_thread_key_length_chk")
	require.Contains(t, upContent, "length(thread_key) <= 512")
	require.Contains(t, upContent, "chat_threads_last_message_preview_length_chk")
	require.Contains(t, upContent, "length(last_message_preview) <= 500")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "061_chat_threads_length_limits.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop constraint if exists chat_threads_last_message_preview_length_chk")
	require.Contains(t, downContent, "drop constraint if exists chat_threads_thread_key_length_chk")
}

func TestMigration063ConversationSchemaFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"063_create_conversation_core_schema.up.sql",
		"063_create_conversation_core_schema.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "063_create_conversation_core_schema.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "create table if not exists rooms")
	require.Contains(t, upContent, "create table if not exists room_participants")
	require.Contains(t, upContent, "create table if not exists conversations")
	require.Contains(t, upContent, "create table if not exists chat_messages")
	require.Contains(t, upContent, "create table if not exists memories")
	require.Contains(t, upContent, "chat_messages_embedding_idx")
	require.Contains(t, upContent, "memories_dedup_active")
	require.Contains(t, upContent, "enable row level security")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "063_create_conversation_core_schema.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop table if exists room_participants")
	require.Contains(t, downContent, "drop table if exists rooms")
	require.Contains(t, downContent, "drop table if exists chat_messages")
	require.Contains(t, downContent, "drop table if exists conversations")
	require.Contains(t, downContent, "drop table if exists memories")
}

func TestSchemaConversationCoreTablesCreateAndRollback(t *testing.T) {
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
		"rooms",
		"room_participants",
		"conversations",
		"chat_messages",
		"memories",
	} {
		require.True(t, schemaTableExists(t, db, table), table)
	}

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	for _, table := range []string{
		"rooms",
		"room_participants",
		"conversations",
		"chat_messages",
		"memories",
	} {
		require.False(t, schemaTableExists(t, db, table), table)
	}
}

func TestSchemaConversationCoreRLSAndIndexes(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	for _, indexName := range []string{
		"rooms_org_type_context_idx",
		"room_participants_room_joined_idx",
		"chat_messages_room_created_idx",
		"chat_messages_conversation_idx",
		"chat_messages_search_idx",
		"chat_messages_embedding_idx",
		"chat_messages_unembedded_idx",
		"memories_dedup_active",
		"memories_embedding_idx",
		"memories_org_kind_idx",
		"memories_org_status_idx",
		"memories_conversation_idx",
	} {
		var indexRegClass sql.NullString
		err := db.QueryRow(`SELECT to_regclass('public.' || $1)::text`, indexName).Scan(&indexRegClass)
		require.NoError(t, err)
		require.True(t, indexRegClass.Valid, indexName)
	}

	for _, table := range []string{
		"rooms",
		"room_participants",
		"conversations",
		"chat_messages",
		"memories",
	} {
		var rlsEnabled bool
		err := db.QueryRow(`SELECT relrowsecurity FROM pg_class WHERE relname = $1`, table).Scan(&rlsEnabled)
		require.NoError(t, err)
		require.True(t, rlsEnabled, table)
	}
}

func TestSchemaProjectChatBackfillCreatesProjectRooms(t *testing.T) {
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

	err = m.Steps(63)
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	orgID := createTestOrganization(t, db, "project-chat-backfill-room-org")
	projectID := createTestProject(t, db, orgID, "Project Chat Backfill Room Project")

	_, err = db.Exec(
		`INSERT INTO project_chat_messages (org_id, project_id, author, body, attachments)
		 VALUES ($1, $2, 'Sam', 'room bootstrap', '[]'::jsonb)`,
		orgID,
		projectID,
	)
	require.NoError(t, err)

	err = m.Steps(1)
	require.NoError(t, err)

	var roomCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM rooms
		 WHERE org_id = $1
		   AND type = 'project'
		   AND context_id = $2`,
		orgID,
		projectID,
	).Scan(&roomCount)
	require.NoError(t, err)
	require.Equal(t, 1, roomCount)
}

func TestMigration064ProjectChatBackfillFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"064_backfill_project_chat_rooms_and_messages.up.sql",
		"064_backfill_project_chat_rooms_and_messages.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "064_backfill_project_chat_rooms_and_messages.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "insert into rooms")
	require.Contains(t, upContent, "insert into chat_messages")
	require.Contains(t, upContent, "on conflict (id) do nothing")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "064_backfill_project_chat_rooms_and_messages.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "delete from chat_messages")
}

func TestMigration066EllieIngestionCursorsDownIncludesPolicyDrop(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"066_create_ellie_ingestion_cursors.up.sql",
		"066_create_ellie_ingestion_cursors.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "066_create_ellie_ingestion_cursors.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop policy if exists ellie_ingestion_cursors_org_isolation on ellie_ingestion_cursors")
	require.Contains(t, downContent, "drop table if exists ellie_ingestion_cursors")
}

func TestMigration067EllieRetrievalStrategiesFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"067_create_ellie_retrieval_strategies.up.sql",
		"067_create_ellie_retrieval_strategies.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "067_create_ellie_retrieval_strategies.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "create table if not exists ellie_retrieval_strategies")
	require.Contains(t, upContent, "create index if not exists ellie_retrieval_strategies_org_active_idx")
	require.Contains(t, upContent, "create policy ellie_retrieval_strategies_org_isolation")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "067_create_ellie_retrieval_strategies.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop trigger if exists ellie_retrieval_strategies_updated_at_trg")
	require.Contains(t, downContent, "drop index if exists ellie_retrieval_strategies_org_active_idx")
	require.Contains(t, downContent, "drop policy if exists ellie_retrieval_strategies_org_isolation on ellie_retrieval_strategies")
	require.Contains(t, downContent, "drop table if exists ellie_retrieval_strategies")
}

func TestMigration068EllieRetrievalQualityEventsFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"068_create_ellie_retrieval_quality_events.up.sql",
		"068_create_ellie_retrieval_quality_events.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "068_create_ellie_retrieval_quality_events.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "create table if not exists ellie_retrieval_quality_events")
	require.Contains(t, upContent, "create index if not exists ellie_retrieval_quality_events_org_project_idx")
	require.Contains(t, upContent, "create index if not exists ellie_retrieval_quality_events_org_created_idx")
	require.Contains(t, upContent, "create policy ellie_retrieval_quality_events_org_isolation")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "068_create_ellie_retrieval_quality_events.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop index if exists ellie_retrieval_quality_events_org_created_idx")
	require.Contains(t, downContent, "drop index if exists ellie_retrieval_quality_events_org_project_idx")
	require.Contains(t, downContent, "drop table if exists ellie_retrieval_quality_events")
}

func TestMigration069ConversationSensitivityFilesExistAndContainConstraint(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"069_add_conversations_sensitivity.down.sql",
		"069_add_conversations_sensitivity.up.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "069_add_conversations_sensitivity.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "alter table conversations")
	require.Contains(t, upContent, "add column if not exists sensitivity")
	require.Contains(t, upContent, "default 'normal'")
	require.Contains(t, upContent, "'normal'")
	require.Contains(t, upContent, "'sensitive'")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "069_add_conversations_sensitivity.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop column if exists sensitivity")
}

func TestMigration071MemoriesSensitivityFilesExistAndContainConstraint(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"071_add_memories_sensitivity.down.sql",
		"071_add_memories_sensitivity.up.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "071_add_memories_sensitivity.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "alter table memories")
	require.Contains(t, upContent, "add column if not exists sensitivity")
	require.Contains(t, upContent, "default 'normal'")
	require.Contains(t, upContent, "'normal'")
	require.Contains(t, upContent, "'sensitive'")
	require.Contains(t, upContent, "create index if not exists memories_org_sensitivity_idx")
	require.Contains(t, upContent, "where sensitivity = 'sensitive'")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "071_add_memories_sensitivity.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop index if exists memories_org_sensitivity_idx")
	require.Contains(t, downContent, "drop column if exists sensitivity")
}

func TestMigration072ComplianceRulesFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"072_create_compliance_rules.down.sql",
		"072_create_compliance_rules.up.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "072_create_compliance_rules.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "create table if not exists compliance_rules")
	require.Contains(t, upContent, "category text not null")
	require.Contains(t, upContent, "'code_quality'")
	require.Contains(t, upContent, "'security'")
	require.Contains(t, upContent, "severity text not null default 'required'")
	require.Contains(t, upContent, "'required'")
	require.Contains(t, upContent, "'recommended'")
	require.Contains(t, upContent, "'informational'")
	require.Contains(t, upContent, "create index if not exists compliance_rules_org_idx")
	require.Contains(t, upContent, "create index if not exists compliance_rules_project_idx")
	require.Contains(t, upContent, "create policy compliance_rules_org_isolation")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "072_create_compliance_rules.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop policy if exists compliance_rules_org_isolation")
	require.Contains(t, downContent, "drop index if exists compliance_rules_project_idx")
	require.Contains(t, downContent, "drop index if exists compliance_rules_org_idx")
	require.Contains(t, downContent, "drop table if exists compliance_rules")
}

func TestMigration074MigrationProgressFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"074_create_migration_progress.down.sql",
		"074_create_migration_progress.up.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "074_create_migration_progress.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "create table if not exists migration_progress")
	require.Contains(t, upContent, "migration_type text not null")
	require.Contains(t, upContent, "status text not null default 'pending'")
	require.Contains(t, upContent, "processed_items int not null default 0")
	require.Contains(t, upContent, "create unique index if not exists migration_progress_org_type_uidx")
	require.Contains(t, upContent, "create policy migration_progress_org_isolation")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "074_create_migration_progress.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop policy if exists migration_progress_org_isolation")
	require.Contains(t, downContent, "drop index if exists migration_progress_org_type_uidx")
	require.Contains(t, downContent, "drop table if exists migration_progress")
}

func TestMigration075ConversationTokenTrackingFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"075_add_conversation_token_tracking.down.sql",
		"075_add_conversation_token_tracking.up.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "075_add_conversation_token_tracking.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "alter table chat_messages")
	require.Contains(t, upContent, "add column if not exists token_count")
	require.Contains(t, upContent, "alter table conversations")
	require.Contains(t, upContent, "add column if not exists total_tokens")
	require.Contains(t, upContent, "alter table rooms")
	require.Contains(t, upContent, "create index if not exists chat_messages_room_created_tokens_idx")
	require.Contains(t, upContent, "create or replace function otter_estimate_token_count")
	require.Contains(t, upContent, "create or replace function otter_chat_messages_token_rollup")
	require.Contains(t, upContent, "create trigger chat_messages_token_rollup_trg")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "075_add_conversation_token_tracking.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop trigger if exists chat_messages_token_rollup_trg on chat_messages")
	require.Contains(t, downContent, "drop function if exists otter_chat_messages_token_rollup")
	require.Contains(t, downContent, "drop function if exists otter_estimate_token_count")
	require.Contains(t, downContent, "drop index if exists chat_messages_room_created_tokens_idx")
	require.Contains(t, downContent, "drop column if exists token_count")
	require.Contains(t, downContent, "drop column if exists total_tokens")
}

func TestMigration078Embedding1536ColumnsFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"078_add_1536_embedding_columns.down.sql",
		"078_add_1536_embedding_columns.up.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "078_add_1536_embedding_columns.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "alter table memories")
	require.Contains(t, upContent, "add column if not exists embedding_1536 vector(1536)")
	require.Contains(t, upContent, "alter table chat_messages")
	require.Contains(t, upContent, "add column if not exists embedding_1536 vector(1536)")
	require.Contains(t, upContent, "create index if not exists memories_embedding_1536_idx")
	require.Contains(t, upContent, "create index if not exists chat_messages_embedding_1536_idx")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "078_add_1536_embedding_columns.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop index if exists memories_embedding_1536_idx")
	require.Contains(t, downContent, "drop index if exists chat_messages_embedding_1536_idx")
	require.Contains(t, downContent, "drop column if exists embedding_1536")
}

func TestSchemaIncludesEllieProjectDocsMigration(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"081_create_ellie_project_docs.up.sql",
		"081_create_ellie_project_docs.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "081_create_ellie_project_docs.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "create table if not exists ellie_project_docs")
	require.Contains(t, upContent, "summary_embedding vector(1536)")
	require.Contains(t, upContent, "unique (org_id, project_id, file_path)")
	require.Contains(t, upContent, "create index if not exists ellie_project_docs_org_project_active_idx")
	require.Contains(t, upContent, "create index if not exists ellie_project_docs_embedding_idx")
	require.Contains(t, upContent, "create trigger ellie_project_docs_updated_at_trg")
	require.Contains(t, upContent, "create policy ellie_project_docs_org_isolation")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "081_create_ellie_project_docs.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop trigger if exists ellie_project_docs_updated_at_trg")
	require.Contains(t, downContent, "drop index if exists ellie_project_docs_embedding_idx")
	require.Contains(t, downContent, "drop index if exists ellie_project_docs_org_project_active_idx")
	require.Contains(t, downContent, "drop policy if exists ellie_project_docs_org_isolation on ellie_project_docs")
	require.Contains(t, downContent, "drop table if exists ellie_project_docs")
}

func TestSchemaConversationsSensitivityColumnAndConstraint(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	var isNullable string
	var defaultExpr sql.NullString
	err := db.QueryRow(
		`SELECT is_nullable, column_default
		 FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND table_name = 'conversations'
		   AND column_name = 'sensitivity'`,
	).Scan(&isNullable, &defaultExpr)
	require.NoError(t, err)
	require.Equal(t, "NO", strings.ToUpper(strings.TrimSpace(isNullable)))
	require.True(t, defaultExpr.Valid)
	require.Contains(t, strings.ToLower(defaultExpr.String), "normal")

	var constraintDef string
	err = db.QueryRow(
		`SELECT pg_get_constraintdef(oid)
		 FROM pg_constraint
		 WHERE conrelid = 'conversations'::regclass
		   AND contype = 'c'
		   AND pg_get_constraintdef(oid) ILIKE '%sensitivity%'
		 ORDER BY oid DESC
		 LIMIT 1`,
	).Scan(&constraintDef)
	require.NoError(t, err)
	lowered := strings.ToLower(constraintDef)
	require.Contains(t, lowered, "sensitivity")
	require.Contains(t, lowered, "'normal'")
	require.Contains(t, lowered, "'sensitive'")
}

func TestMigration070ContextInjectionsFilesExistAndContainDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"070_create_context_injections.down.sql",
		"070_create_context_injections.up.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "070_create_context_injections.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "create table if not exists context_injections")
	require.Contains(t, upContent, "unique (room_id, memory_id)")
	require.Contains(t, upContent, "create index if not exists idx_context_injections_room")
	require.Contains(t, upContent, "enable row level security")
	require.Contains(t, upContent, "context_injections_org_isolation")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "070_create_context_injections.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop policy if exists context_injections_org_isolation")
	require.Contains(t, downContent, "drop index if exists idx_context_injections_room")
	require.Contains(t, downContent, "drop table if exists context_injections")
}

func TestSchemaContextInjectionsTableAndConstraint(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	var tableRegClass sql.NullString
	err := db.QueryRow(`SELECT to_regclass('public.context_injections')::text`).Scan(&tableRegClass)
	require.NoError(t, err)
	require.True(t, tableRegClass.Valid)

	var roomIdx sql.NullString
	err = db.QueryRow(`SELECT to_regclass('public.idx_context_injections_room')::text`).Scan(&roomIdx)
	require.NoError(t, err)
	require.True(t, roomIdx.Valid)

	var rlsEnabled bool
	err = db.QueryRow(
		`SELECT relrowsecurity
		 FROM pg_class
		 WHERE relname = 'context_injections'`,
	).Scan(&rlsEnabled)
	require.NoError(t, err)
	require.True(t, rlsEnabled)

	var uniqueConstraintCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM pg_constraint
		 WHERE conrelid = 'context_injections'::regclass
		   AND contype = 'u'
		   AND pg_get_constraintdef(oid) ILIKE '%(room_id, memory_id)%'`,
	).Scan(&uniqueConstraintCount)
	require.NoError(t, err)
	require.Equal(t, 1, uniqueConstraintCount)
}

func TestSchemaMemoriesAndConversationsSensitivityColumnsAndConstraints(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	for _, tableName := range []string{"memories", "conversations"} {
		var isNullable string
		var defaultExpr sql.NullString
		err := db.QueryRow(
			`SELECT is_nullable, column_default
			 FROM information_schema.columns
			 WHERE table_schema = 'public'
			   AND table_name = $1
			   AND column_name = 'sensitivity'`,
			tableName,
		).Scan(&isNullable, &defaultExpr)
		require.NoError(t, err)
		require.Equal(t, "NO", strings.ToUpper(strings.TrimSpace(isNullable)))
		require.True(t, defaultExpr.Valid)
		require.Contains(t, strings.ToLower(defaultExpr.String), "normal")

		var constraintDef string
		err = db.QueryRow(
			`SELECT pg_get_constraintdef(oid)
			 FROM pg_constraint
			 WHERE conrelid = to_regclass('public.' || $1)
			   AND contype = 'c'
			   AND pg_get_constraintdef(oid) ILIKE '%sensitivity%'
			 ORDER BY oid DESC
			 LIMIT 1`,
			tableName,
		).Scan(&constraintDef)
		require.NoError(t, err)
		lowered := strings.ToLower(constraintDef)
		require.Contains(t, lowered, "sensitivity")
		require.Contains(t, lowered, "'normal'")
		require.Contains(t, lowered, "'sensitive'")
	}

	var sensitivityIndexDef string
	err := db.QueryRow(
		`SELECT indexdef
		 FROM pg_indexes
		 WHERE schemaname = 'public'
		   AND indexname = 'memories_org_sensitivity_idx'`,
	).Scan(&sensitivityIndexDef)
	require.NoError(t, err)
	loweredIndexDef := strings.ToLower(sensitivityIndexDef)
	require.Contains(t, loweredIndexDef, "where")
	require.Contains(t, loweredIndexDef, "sensitivity")
	require.Contains(t, loweredIndexDef, "sensitive")
}

func TestSchemaComplianceRulesTableAndConstraints(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	var tableRegClass sql.NullString
	err := db.QueryRow(`SELECT to_regclass('public.compliance_rules')::text`).Scan(&tableRegClass)
	require.NoError(t, err)
	require.True(t, tableRegClass.Valid)

	var orgIdx sql.NullString
	err = db.QueryRow(`SELECT to_regclass('public.compliance_rules_org_idx')::text`).Scan(&orgIdx)
	require.NoError(t, err)
	require.True(t, orgIdx.Valid)

	var projectIdx sql.NullString
	err = db.QueryRow(`SELECT to_regclass('public.compliance_rules_project_idx')::text`).Scan(&projectIdx)
	require.NoError(t, err)
	require.True(t, projectIdx.Valid)

	var rlsEnabled bool
	err = db.QueryRow(
		`SELECT relrowsecurity
		 FROM pg_class
		 WHERE relname = 'compliance_rules'`,
	).Scan(&rlsEnabled)
	require.NoError(t, err)
	require.True(t, rlsEnabled)

	var enabledDefault sql.NullString
	err = db.QueryRow(
		`SELECT column_default
		 FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND table_name = 'compliance_rules'
		   AND column_name = 'enabled'`,
	).Scan(&enabledDefault)
	require.NoError(t, err)
	require.True(t, enabledDefault.Valid)
	require.Contains(t, strings.ToLower(enabledDefault.String), "true")

	var severityDefault sql.NullString
	err = db.QueryRow(
		`SELECT column_default
		 FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND table_name = 'compliance_rules'
		   AND column_name = 'severity'`,
	).Scan(&severityDefault)
	require.NoError(t, err)
	require.True(t, severityDefault.Valid)
	require.Contains(t, strings.ToLower(severityDefault.String), "required")

	var categoryConstraint string
	err = db.QueryRow(
		`SELECT pg_get_constraintdef(oid)
		 FROM pg_constraint
		 WHERE conrelid = 'compliance_rules'::regclass
		   AND contype = 'c'
		   AND pg_get_constraintdef(oid) ILIKE '%category%'
		 ORDER BY oid DESC
		 LIMIT 1`,
	).Scan(&categoryConstraint)
	require.NoError(t, err)
	loweredCategory := strings.ToLower(categoryConstraint)
	require.Contains(t, loweredCategory, "'code_quality'")
	require.Contains(t, loweredCategory, "'security'")
	require.Contains(t, loweredCategory, "'scope'")
	require.Contains(t, loweredCategory, "'style'")
	require.Contains(t, loweredCategory, "'process'")
	require.Contains(t, loweredCategory, "'technical'")

	var severityConstraint string
	err = db.QueryRow(
		`SELECT pg_get_constraintdef(oid)
		 FROM pg_constraint
		 WHERE conrelid = 'compliance_rules'::regclass
		   AND contype = 'c'
		   AND pg_get_constraintdef(oid) ILIKE '%severity%'
		 ORDER BY oid DESC
		 LIMIT 1`,
	).Scan(&severityConstraint)
	require.NoError(t, err)
	loweredSeverity := strings.ToLower(severityConstraint)
	require.Contains(t, loweredSeverity, "'required'")
	require.Contains(t, loweredSeverity, "'recommended'")
	require.Contains(t, loweredSeverity, "'informational'")
}

func TestSchemaProjectChatBackfillCopiesMessagesWithParity(t *testing.T) {
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

	err = m.Steps(63)
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	orgID := createTestOrganization(t, db, "project-chat-backfill-parity-org")
	projectID := createTestProject(t, db, orgID, "Project Chat Backfill Parity Project")

	var firstID string
	err = db.QueryRow(
		`INSERT INTO project_chat_messages (org_id, project_id, author, body, attachments)
		 VALUES ($1, $2, 'Sam', 'first body', '[{\"id\":\"file-1\"}]'::jsonb)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&firstID)
	require.NoError(t, err)

	var secondID string
	err = db.QueryRow(
		`INSERT INTO project_chat_messages (org_id, project_id, author, body, attachments)
		 VALUES ($1, $2, 'Frank', 'second body', '[]'::jsonb)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&secondID)
	require.NoError(t, err)

	err = m.Steps(1)
	require.NoError(t, err)

	var copiedCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM chat_messages cm
		 JOIN rooms r ON r.id = cm.room_id
		 WHERE cm.org_id = $1
		   AND r.type = 'project'
		   AND r.context_id = $2`,
		orgID,
		projectID,
	).Scan(&copiedCount)
	require.NoError(t, err)
	require.Equal(t, 2, copiedCount)

	var firstBody, firstAttachments string
	err = db.QueryRow(
		`SELECT body, attachments::text
		 FROM chat_messages
		 WHERE id = $1`,
		firstID,
	).Scan(&firstBody, &firstAttachments)
	require.NoError(t, err)
	require.Equal(t, "first body", firstBody)
	require.Contains(t, firstAttachments, `"id": "file-1"`)

	var secondBody string
	err = db.QueryRow(`SELECT body FROM chat_messages WHERE id = $1`, secondID).Scan(&secondBody)
	require.NoError(t, err)
	require.Equal(t, "second body", secondBody)
}

func TestSchemaProjectChatBackfillIsIdempotent(t *testing.T) {
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

	err = m.Steps(63)
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	orgID := createTestOrganization(t, db, "project-chat-backfill-idempotent-org")
	projectID := createTestProject(t, db, orgID, "Project Chat Backfill Idempotent Project")

	_, err = db.Exec(
		`INSERT INTO project_chat_messages (org_id, project_id, author, body, attachments)
		 VALUES ($1, $2, 'Sam', 'idempotent body', '[{\"id\":\"doc\"}]'::jsonb)`,
		orgID,
		projectID,
	)
	require.NoError(t, err)

	upRaw, err := os.ReadFile(filepath.Join(getMigrationsDir(t), "064_backfill_project_chat_rooms_and_messages.up.sql"))
	require.NoError(t, err)
	upSQL := string(upRaw)

	_, err = db.Exec(upSQL)
	require.NoError(t, err)

	var firstMessageCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM chat_messages cm
		 JOIN rooms r ON r.id = cm.room_id
		 WHERE cm.org_id = $1
		   AND r.type = 'project'
		   AND r.context_id = $2`,
		orgID,
		projectID,
	).Scan(&firstMessageCount)
	require.NoError(t, err)

	var firstRoomCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM rooms
		 WHERE org_id = $1
		   AND type = 'project'
		   AND context_id = $2`,
		orgID,
		projectID,
	).Scan(&firstRoomCount)
	require.NoError(t, err)

	_, err = db.Exec(upSQL)
	require.NoError(t, err)

	var secondMessageCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM chat_messages cm
		 JOIN rooms r ON r.id = cm.room_id
		 WHERE cm.org_id = $1
		   AND r.type = 'project'
		   AND r.context_id = $2`,
		orgID,
		projectID,
	).Scan(&secondMessageCount)
	require.NoError(t, err)

	var secondRoomCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM rooms
		 WHERE org_id = $1
		   AND type = 'project'
		   AND context_id = $2`,
		orgID,
		projectID,
	).Scan(&secondRoomCount)
	require.NoError(t, err)

	require.Equal(t, firstMessageCount, secondMessageCount)
	require.Equal(t, firstRoomCount, secondRoomCount)
}

func TestMigration065MemoriesBackfillFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"065_backfill_legacy_memory_tables_into_memories.up.sql",
		"065_backfill_legacy_memory_tables_into_memories.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "065_backfill_legacy_memory_tables_into_memories.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "insert into memories")
	require.Contains(t, upContent, "memory_entries")
	require.Contains(t, upContent, "shared_knowledge")
	require.Contains(t, upContent, "agent_memories")
	require.Contains(t, upContent, "on conflict (org_id, content_hash) where status = 'active' do nothing")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "065_backfill_legacy_memory_tables_into_memories.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "delete from memories")
	require.Contains(t, downContent, "source_table")
}

func TestSchemaMemoriesBackfillCopiesLegacyRows(t *testing.T) {
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

	err = m.Steps(64)
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	orgID := createTestOrganization(t, db, "memories-backfill-copy-org")
	projectID := createTestProject(t, db, orgID, "Memories Backfill Copy Project")
	agentID := insertSchemaAgent(t, db, orgID, "memories-copy-agent")

	var memoryEntryID string
	err = db.QueryRow(
		`INSERT INTO memory_entries (
			org_id, agent_id, kind, title, content, metadata, importance, confidence, sensitivity, status, source_project
		) VALUES (
			$1, $2, 'decision', 'Entry Decision', 'Memory entry content', '{}'::jsonb, 3, 0.8, 'internal', 'active', $3
		) RETURNING id`,
		orgID,
		agentID,
		projectID,
	).Scan(&memoryEntryID)
	require.NoError(t, err)

	var sharedKnowledgeID string
	err = db.QueryRow(
		`INSERT INTO shared_knowledge (
			org_id, source_agent_id, kind, title, content, metadata, status
		) VALUES (
			$1, $2, 'pattern', 'Shared Pattern', 'Shared knowledge content', '{}'::jsonb, 'active'
		) RETURNING id`,
		orgID,
		agentID,
	).Scan(&sharedKnowledgeID)
	require.NoError(t, err)

	var agentMemoryID string
	err = db.QueryRow(
		`INSERT INTO agent_memories (
			org_id, agent_id, kind, date, content
		) VALUES (
			$1, $2, 'long_term', CURRENT_DATE, 'Agent memory content'
		) RETURNING id`,
		orgID,
		agentID,
	).Scan(&agentMemoryID)
	require.NoError(t, err)

	err = m.Steps(1)
	require.NoError(t, err)

	var copiedCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM memories
		 WHERE org_id = $1
		   AND metadata->>'source_table' IN ('memory_entries', 'shared_knowledge', 'agent_memories')`,
		orgID,
	).Scan(&copiedCount)
	require.NoError(t, err)
	require.Equal(t, 3, copiedCount)

	for _, check := range [][2]string{
		{"memory_entries", memoryEntryID},
		{"shared_knowledge", sharedKnowledgeID},
		{"agent_memories", agentMemoryID},
	} {
		var exists bool
		err = db.QueryRow(
			`SELECT EXISTS (
				SELECT 1
				FROM memories
				WHERE org_id = $1
				  AND metadata->>'source_table' = $2
				  AND metadata->>'source_id' = $3
			)`,
			orgID,
			check[0],
			check[1],
		).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, check[0])
	}
}

func TestSchemaMemoriesBackfillMapsStatusesAndKinds(t *testing.T) {
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

	err = m.Steps(64)
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	orgID := createTestOrganization(t, db, "memories-backfill-mapping-org")
	agentID := insertSchemaAgent(t, db, orgID, "memories-mapping-agent")

	var memoryEntryID string
	err = db.QueryRow(
		`INSERT INTO memory_entries (
			org_id, agent_id, kind, title, content, metadata, importance, confidence, sensitivity, status
		) VALUES (
			$1, $2, 'decision', 'Warm Decision', 'Warm memory entry', '{}'::jsonb, 3, 0.7, 'internal', 'warm'
		) RETURNING id`,
		orgID,
		agentID,
	).Scan(&memoryEntryID)
	require.NoError(t, err)

	var sharedKnowledgeID string
	err = db.QueryRow(
		`INSERT INTO shared_knowledge (
			org_id, source_agent_id, kind, title, content, metadata, status
		) VALUES (
			$1, $2, 'correction', 'Superseded Correction', 'Old correction', '{}'::jsonb, 'superseded'
		) RETURNING id`,
		orgID,
		agentID,
	).Scan(&sharedKnowledgeID)
	require.NoError(t, err)

	var agentMemoryID string
	err = db.QueryRow(
		`INSERT INTO agent_memories (
			org_id, agent_id, kind, date, content
		) VALUES (
			$1, $2, 'note', CURRENT_DATE, 'Agent note content'
		) RETURNING id`,
		orgID,
		agentID,
	).Scan(&agentMemoryID)
	require.NoError(t, err)

	err = m.Steps(1)
	require.NoError(t, err)

	var (
		entryKind   string
		entryStatus string
	)
	err = db.QueryRow(
		`SELECT kind, status
		 FROM memories
		 WHERE org_id = $1
		   AND metadata->>'source_table' = 'memory_entries'
		   AND metadata->>'source_id' = $2`,
		orgID,
		memoryEntryID,
	).Scan(&entryKind, &entryStatus)
	require.NoError(t, err)
	require.Equal(t, "technical_decision", entryKind)
	require.Equal(t, "archived", entryStatus)

	var (
		sharedKind   string
		sharedStatus string
	)
	err = db.QueryRow(
		`SELECT kind, status
		 FROM memories
		 WHERE org_id = $1
		   AND metadata->>'source_table' = 'shared_knowledge'
		   AND metadata->>'source_id' = $2`,
		orgID,
		sharedKnowledgeID,
	).Scan(&sharedKind, &sharedStatus)
	require.NoError(t, err)
	require.Equal(t, "correction", sharedKind)
	require.Equal(t, "deprecated", sharedStatus)

	var (
		agentKind   string
		agentStatus string
	)
	err = db.QueryRow(
		`SELECT kind, status
		 FROM memories
		 WHERE org_id = $1
		   AND metadata->>'source_table' = 'agent_memories'
		   AND metadata->>'source_id' = $2`,
		orgID,
		agentMemoryID,
	).Scan(&agentKind, &agentStatus)
	require.NoError(t, err)
	require.Equal(t, "context", agentKind)
	require.Equal(t, "active", agentStatus)
}

func TestSchemaMemoriesBackfillIsIdempotent(t *testing.T) {
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

	err = m.Steps(64)
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	orgID := createTestOrganization(t, db, "memories-backfill-idempotent-org")
	agentID := insertSchemaAgent(t, db, orgID, "memories-idempotent-agent")

	_, err = db.Exec(
		`INSERT INTO memory_entries (
			org_id, agent_id, kind, title, content, metadata, importance, confidence, sensitivity, status
		) VALUES (
			$1, $2, 'fact', 'Idempotent Fact', 'Idempotent memory content', '{}'::jsonb, 2, 0.9, 'internal', 'active'
		)`,
		orgID,
		agentID,
	)
	require.NoError(t, err)

	upRaw, err := os.ReadFile(filepath.Join(getMigrationsDir(t), "065_backfill_legacy_memory_tables_into_memories.up.sql"))
	require.NoError(t, err)
	upSQL := string(upRaw)

	_, err = db.Exec(upSQL)
	require.NoError(t, err)

	var firstCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM memories
		 WHERE org_id = $1
		   AND metadata->>'source_table' = 'memory_entries'`,
		orgID,
	).Scan(&firstCount)
	require.NoError(t, err)

	_, err = db.Exec(upSQL)
	require.NoError(t, err)

	var secondCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM memories
		 WHERE org_id = $1
		   AND metadata->>'source_table' = 'memory_entries'`,
		orgID,
	).Scan(&secondCount)
	require.NoError(t, err)

	require.Equal(t, firstCount, secondCount)
}

func TestSchemaChatThreadsLengthConstraints(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	var threadKeyConstraint bool
	err := db.QueryRow(
		`SELECT EXISTS (
			SELECT 1
			FROM pg_constraint
			WHERE conname = 'chat_threads_thread_key_length_chk'
		)`,
	).Scan(&threadKeyConstraint)
	require.NoError(t, err)
	require.True(t, threadKeyConstraint)

	var previewConstraint bool
	err = db.QueryRow(
		`SELECT EXISTS (
			SELECT 1
			FROM pg_constraint
			WHERE conname = 'chat_threads_last_message_preview_length_chk'
		)`,
	).Scan(&previewConstraint)
	require.NoError(t, err)
	require.True(t, previewConstraint)
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

func TestMigration073AgentJobsFilesExistAndContainCoreDDL(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	files := []string{
		"073_create_agent_jobs.up.sql",
		"073_create_agent_jobs.down.sql",
	}
	for _, filename := range files {
		_, err := os.Stat(filepath.Join(migrationsDir, filename))
		require.NoError(t, err)
	}

	upRaw, err := os.ReadFile(filepath.Join(migrationsDir, "073_create_agent_jobs.up.sql"))
	require.NoError(t, err)
	upContent := strings.ToLower(string(upRaw))
	require.Contains(t, upContent, "create table if not exists agent_jobs")
	require.Contains(t, upContent, "create table if not exists agent_job_runs")
	require.Contains(t, upContent, "schedule_kind in ('cron', 'interval', 'once')")
	require.Contains(t, upContent, "payload_kind in ('message', 'system_event')")
	require.Contains(t, upContent, "status in ('active', 'paused', 'completed', 'failed')")
	require.Contains(t, upContent, "for update skip locked")
	require.Contains(t, upContent, "enable row level security")
	require.Contains(t, upContent, "agent_jobs_org_isolation")
	require.Contains(t, upContent, "agent_job_runs_org_isolation")

	downRaw, err := os.ReadFile(filepath.Join(migrationsDir, "073_create_agent_jobs.down.sql"))
	require.NoError(t, err)
	downContent := strings.ToLower(string(downRaw))
	require.Contains(t, downContent, "drop table if exists agent_job_runs")
	require.Contains(t, downContent, "drop table if exists agent_jobs")
}
