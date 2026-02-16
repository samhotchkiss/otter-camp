DROP POLICY IF EXISTS ellie_memory_taxonomy_org_isolation ON ellie_memory_taxonomy;
DROP POLICY IF EXISTS ellie_taxonomy_nodes_org_isolation ON ellie_taxonomy_nodes;

DROP INDEX IF EXISTS ellie_memory_taxonomy_memory_idx;
DROP INDEX IF EXISTS ellie_memory_taxonomy_node_idx;
DROP TABLE IF EXISTS ellie_memory_taxonomy;

DROP INDEX IF EXISTS ellie_taxonomy_nodes_parent_idx;
DROP INDEX IF EXISTS ellie_taxonomy_nodes_org_parent_idx;
DROP INDEX IF EXISTS ellie_taxonomy_nodes_org_root_slug_uidx;
DROP TABLE IF EXISTS ellie_taxonomy_nodes;
