package controllers

// Exact pre-optimization query retained only for differential testing.
func baselineWriteSelectionSQL() string {
	return `SELECT COALESCE(to_jsonb(root)->>'thread_id',root.id::text),COALESCE((SELECT msg.conversation_id::text FROM provider_conversation_messages msg WHERE msg.conversation_id IN (` + businessThreadMembers("root.id") + `) ORDER BY msg.created_at DESC,msg.id DESC LIMIT 1),root.id::text),EXISTS(SELECT 1 FROM provider_conversations b WHERE b.id IN (` + businessThreadMembers("root.id") + `) AND b.status='blocked') FROM provider_conversations root WHERE root.id=$1::uuid`
}
