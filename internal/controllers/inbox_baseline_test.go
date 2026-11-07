// Exact pre-optimization handler from 15fe53b, test-only comparison oracle.
package controllers

import (
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"time"
)

func (s *CommunityController) legacyInbox(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	actor := communityActor(c)
	provider, _, _ := s.market.providerForUser(c.Context(), actor)
	rows, err := s.db.Query(c.Context(), `WITH inbox AS (
 SELECT r.id,r.kind,'shared'::text AS source,r.last_message_at,
 CASE WHEN r.kind='town_hall' THEN r.title ELSE COALESCE((SELECT u.full_name FROM chat_participants peer JOIN users u ON u.id=peer.user_id WHERE peer.room_id=r.id AND peer.user_id<>$1::uuid LIMIT 1),'Private chat') END AS title,
 COALESCE((SELECT peer.user_id::text FROM chat_participants peer WHERE peer.room_id=r.id AND peer.user_id<>$1::uuid AND r.kind='direct' LIMIT 1),'') AS peer_id,
 COALESCE((SELECT o.id::text FROM provider_organizations o JOIN chat_participants peer ON peer.user_id=o.owner_user_id WHERE peer.room_id=r.id AND peer.user_id<>$1::uuid AND r.kind='direct' AND o.is_active LIMIT 1),'') AS provider_id,'member'::text AS actor_role,r.state,
 (SELECT count(*)::int FROM chat_messages msg WHERE msg.room_id=r.id AND msg.sender_user_id<>$1::uuid AND msg.created_at>COALESCE(member.last_read_at,'epoch'::timestamptz)) AS unread_count,r.context
 FROM chat_rooms r JOIN chat_participants member ON member.room_id=r.id AND member.user_id=$1::uuid AND member.state='active'
 WHERE r.kind<>'town_hall' OR (r.state='open' AND EXISTS(SELECT 1 FROM town_hall_requests h WHERE h.room_id=r.id AND h.engagement_deadline>clock_timestamp()))
 UNION ALL
 SELECT c.id,'business','legacy',c.last_message_at,CASE WHEN c.user_id=$1::uuid THEN p.business_name ELSE COALESCE(u.full_name,'Customer') END,CASE WHEN c.user_id=$1::uuid THEN p.owner_user_id::text ELSE c.user_id::text END,c.provider_id::text,
 CASE WHEN c.user_id=$1::uuid THEN 'buyer' ELSE 'provider' END,c.status,
 (SELECT count(*)::int FROM provider_conversation_messages msg JOIN provider_conversations physical ON physical.id=msg.conversation_id WHERE physical.id IN (`+businessThreadMembers("c.id")+`) AND msg.sender_type=CASE WHEN c.user_id=$1::uuid THEN 'provider' ELSE 'buyer' END AND msg.created_at>COALESCE(CASE WHEN c.user_id=$1::uuid THEN physical.buyer_last_read_at ELSE physical.provider_last_read_at END,'epoch'::timestamptz)),
 jsonb_build_object('listing_id',c.listing_id,'product_id',c.product_id,'title',COALESCE(l.title,pr.title))
 FROM provider_conversations c JOIN provider_organizations p ON p.id=c.provider_id JOIN users u ON u.id=c.user_id LEFT JOIN provider_listings l ON l.id=c.listing_id LEFT JOIN products pr ON pr.id=c.product_id
 WHERE (c.user_id=$1::uuid OR c.provider_id=NULLIF($2,'')::uuid) AND c.id::text=COALESCE(to_jsonb(c)->>'thread_id',c.id::text)
 ) SELECT id::text,kind,source,last_message_at,title,peer_id,provider_id,actor_role,state,unread_count,context,COALESCE((SELECT u.avatar_url FROM users u WHERE u.id=NULLIF(inbox.peer_id,'')::uuid),''),COALESCE(CASE WHEN source='shared' THEN (SELECT jsonb_build_object('id',msg.id,'body',msg.body,'is_mine',msg.sender_user_id=$1::uuid) FROM chat_messages msg WHERE msg.room_id=inbox.id ORDER BY msg.created_at DESC,msg.id DESC LIMIT 1) ELSE (SELECT jsonb_build_object('id',msg.id,'body',to_jsonb(msg)->>'body','is_mine',msg.sender_type=inbox.actor_role) FROM provider_conversation_messages msg WHERE msg.conversation_id IN (`+businessThreadMembers("inbox.id")+`) ORDER BY msg.created_at DESC,msg.id DESC LIMIT 1) END,'{}'::jsonb) FROM inbox WHERE ($3::timestamptz IS NULL OR (last_message_at,id)<($3,$4::uuid)) AND ($6='' OR id=NULLIF($6,'')::uuid OR (source='legacy' AND NULLIF($6,'')::uuid IN (`+businessThreadMembers("inbox.id")+`))) ORDER BY last_message_at DESC,id DESC LIMIT $5`, actor, provider, page.CursorTime, nullID(page.CursorID), page.Limit+1, c.Query("conversation_id"))
	if err != nil {
		return communityError(err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	next := ""
	var last time.Time
	var lastID string
	for rows.Next() {
		var id, kind, source, title, peer, provider, role, state, avatar string
		var preview json.RawMessage
		var at time.Time
		var unread int
		var raw []byte
		if err = rows.Scan(&id, &kind, &source, &at, &title, &peer, &provider, &role, &state, &unread, &raw, &avatar, &preview); err != nil {
			return communityError(err)
		}
		if len(items) == page.Limit {
			next = encodeAdminCursor(last, lastID)
			break
		}
		var data any
		_ = json.Unmarshal(raw, &data)
		var latest struct {
			ID   string `json:"id"`
			Body string `json:"body"`
			Mine bool   `json:"is_mine"`
		}
		if err = json.Unmarshal(preview, &latest); err != nil {
			return communityError(err)
		}
		items = append(items, fiber.Map{"id": id, "kind": kind, "source": source, "title": title, "peer_user_id": peer, "provider_id": provider, "actor_role": role, "state": state, "unread_count": unread, "context": data, "last_message_at": at, "avatar_url": avatar, "last_message": latest.Body, "last_message_id": latest.ID, "last_message_is_mine": latest.Mine})
		last, lastID = at, id
	}
	if err = rows.Err(); err != nil {
		return communityError(err)
	}
	rows.Close()
	return providerIdentityJSON(c, s.db, fiber.Map{"items": items, "next_cursor": next, "has_more": next != ""}, s.market.s3)
}
