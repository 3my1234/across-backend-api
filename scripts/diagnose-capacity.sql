-- Read-only launch snapshot. Run in psql connected to across_db.
-- Does not print SQL statements, message bodies, account data or credentials.
BEGIN READ ONLY;
SET LOCAL statement_timeout = '10s';
SELECT current_setting('max_connections')::int AS max_connections,
 count(*) FILTER (WHERE backend_type='client backend') AS client_connections,
 count(*) FILTER (WHERE backend_type='client backend' AND state='active') AS active_connections,
 count(*) FILTER (WHERE backend_type='client backend' AND wait_event_type='Lock') AS waiting_for_locks
FROM pg_stat_activity;
SELECT datname,numbackends,xact_commit,xact_rollback,blks_read,blks_hit,temp_bytes,deadlocks
FROM pg_stat_database WHERE datname=current_database();
SELECT relname,n_live_tup,n_dead_tup,last_autovacuum,last_autoanalyze,seq_scan,idx_scan
FROM pg_stat_user_tables WHERE relname IN ('provider_conversations','provider_conversation_messages','support_tickets','support_messages','chat_rooms','chat_messages','chat_participants','town_hall_requests','town_hall_offers','notification_push_deliveries','email_outbox') ORDER BY relname;
SELECT tablename,indexname,indexdef FROM pg_indexes WHERE schemaname=current_schema()
 AND tablename IN ('provider_conversations','provider_conversation_messages','support_tickets','support_messages','chat_rooms','chat_messages','chat_participants','town_hall_requests','town_hall_offers') ORDER BY tablename,indexname;
SELECT pg_size_pretty(pg_database_size(current_database())) AS database_size;
COMMIT;
