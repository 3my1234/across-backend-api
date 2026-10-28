package controllers

// Only static, developer-owned SQL expressions are passed here. JSON lookup
// permits an old-schema rolling deployment to retain its pre-migration behavior.
// Pair predicates use the pair index; the JSON check is bounded to that pair.
func businessThreadMembers(root string) string {
	return `SELECT member.id FROM provider_conversations requested
 JOIN provider_conversations member ON member.id=requested.id OR (member.provider_id=requested.provider_id AND member.user_id=requested.user_id)
 WHERE requested.id=` + root + ` AND COALESCE(to_jsonb(member)->>'thread_id',member.id::text)=COALESCE(to_jsonb(requested)->>'thread_id',requested.id::text)`
}

// Each physical conversation can contribute at most the requested page size to
// the globally newest page. Bound its ordered index scan before merging thread
// members, rather than reading/projecting their entire histories and sorting.
// Keep the exact membership helper and optional JSON fields for old schemas.
// Arguments: conversation UUID, cursor timestamp, cursor UUID, page size + 1.
func businessHistoryPageSQL() string {
	return `SELECT msg.id::text,msg.sender_type,msg.body,msg.created_at,msg.media_keys,to_jsonb(msg)->'location',COALESCE(to_jsonb(msg)->'context','{}'::jsonb)
	FROM (` + businessThreadMembers("$1::uuid") + `) member
	CROSS JOIN LATERAL (
	 SELECT msg.* FROM provider_conversation_messages msg
	 WHERE msg.conversation_id=member.id
	 AND ($2::timestamptz IS NULL OR (msg.created_at,msg.id)<($2,$3::uuid))
	 ORDER BY msg.created_at DESC,msg.id DESC LIMIT $4
	) msg ORDER BY msg.created_at DESC,msg.id DESC LIMIT $4`
}

// Select at most one indexed message per physical conversation before merging.
// Ownership and blocked-thread checks remain in the caller and this query.
func businessWriteSelectionSQL() string {
	return `SELECT COALESCE(to_jsonb(root)->>'thread_id',root.id::text),COALESCE((SELECT msg.conversation_id::text FROM (` + businessThreadMembers("root.id") + `) physical CROSS JOIN LATERAL (SELECT msg.conversation_id,msg.created_at,msg.id FROM provider_conversation_messages msg WHERE msg.conversation_id=physical.id ORDER BY msg.created_at DESC,msg.id DESC LIMIT 1) msg ORDER BY msg.created_at DESC,msg.id DESC LIMIT 1),root.id::text),EXISTS(SELECT 1 FROM provider_conversations b WHERE b.id IN (` + businessThreadMembers("root.id") + `) AND b.status='blocked') FROM provider_conversations root WHERE root.id=$1::uuid`
}
