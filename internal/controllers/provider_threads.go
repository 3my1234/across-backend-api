package controllers

// Only static, developer-owned SQL expressions are passed here. JSON lookup
// permits an old-schema rolling deployment to retain its pre-migration behavior.
// Pair predicates use the pair index; the JSON check is bounded to that pair.
func businessThreadMembers(root string) string {
	return `SELECT member.id FROM provider_conversations requested
 JOIN provider_conversations member ON member.id=requested.id OR (member.provider_id=requested.provider_id AND member.user_id=requested.user_id)
 WHERE requested.id=` + root + ` AND COALESCE(to_jsonb(member)->>'thread_id',member.id::text)=COALESCE(to_jsonb(requested)->>'thread_id',requested.id::text)`
}
