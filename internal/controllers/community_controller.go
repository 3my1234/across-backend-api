package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"regexp"
	"strings"
	"time"

	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type CommunityController struct {
	db     *pgxpool.Pool
	market *ProviderMarketplaceController
	cache  *redis.Client
}

func NewCommunityController(db *pgxpool.Pool, cfg config.Config, cache ...*redis.Client) *CommunityController {
	controller := &CommunityController{db: db, market: NewProviderMarketplaceController(db, cfg)}
	if len(cache) > 0 {
		controller.cache = cache[0]
	}
	return controller
}
func communityActor(c *fiber.Ctx) string { id, _ := c.Locals("user_id").(string); return id }
func ProviderNativeNotificationsReady(ctx context.Context, db *pgxpool.Pool) bool {
	var ready bool
	err := db.QueryRow(ctx, `SELECT to_regprocedure('enqueue_provider_native_notification()') IS NOT NULL`).Scan(&ready)
	return err == nil && ready
}

// Verify the database used by API requests, not only a migration connection.
func CommunitySchemaReady(ctx context.Context, db *pgxpool.Pool) bool {
	var ready bool
	err := db.QueryRow(ctx, `SELECT to_regclass('social_profiles') IS NOT NULL
	 AND to_regclass('chat_rooms') IS NOT NULL AND to_regclass('chat_messages') IS NOT NULL
	 AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('chat_messages') AND attname='reply_to_id' AND NOT attisdropped)
	 AND to_regclass('social_statuses') IS NOT NULL AND to_regclass('social_promotion_jobs') IS NOT NULL
	 AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('town_hall_requests') AND attname='message_only' AND NOT attisdropped)
 AND to_regprocedure('expire_unengaged_town_hall_requests(integer)') IS NOT NULL
 AND to_regprocedure('purge_ended_town_hall_requests(integer)') IS NOT NULL
 AND to_regclass('provider_driver_presence') IS NOT NULL AND to_regprocedure('valid_chat_location(jsonb)') IS NOT NULL`).Scan(&ready)
	return err == nil && ready
}
func (s *CommunityController) InvitePage(c *fiber.Ctx) error {
	handle := strings.ToLower(c.Params("handle"))
	if !socialHandle.MatchString(handle) {
		return fiber.ErrNotFound
	}
	var visible bool
	if err := s.db.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM social_profiles p JOIN users u ON u.id=p.user_id WHERE p.handle=$1 AND p.discoverable AND u.is_active)`, handle).Scan(&visible); err != nil {
		return communityError(err)
	}
	if !visible {
		return fiber.ErrNotFound
	}
	c.Type("html")
	c.Set("Cache-Control", "no-store")
	return c.SendString(fmt.Sprintf(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Join a friend on Atl xpress</title><style>body{font:16px system-ui;background:#12221c;color:#fff;margin:0;padding:24px}main{max-width:440px;margin:10vh auto}a{display:block;border-radius:12px;background:#ff4747;color:#fff;padding:16px;text-decoration:none;text-align:center;margin:16px 0}p{line-height:1.6}code{font-size:20px}</style><main><h1>Join me on Atl xpress</h1><p>Find your friend by username:</p><code>@%s</code><a href="across://friend/%s">Open Atl</a><p>If you have not installed Atl yet, keep this username. After signing in, open Chat, then Friends, enter the username and send a request. Your friend chooses whether to accept.</p></main></html>`, html.EscapeString(handle), handle))
}
func communityID(c *fiber.Ctx, key string) (string, error) {
	id := c.Params(key)
	if _, err := uuid.Parse(id); err != nil {
		return "", fiber.NewError(400, "Invalid reference")
	}
	return id, nil
}
func communityError(err error) error {
	if quota := providerCapacityError(err); quota != nil {
		return quota
	}
	var p *pgconn.PgError
	if err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	}
	if e, ok := err.(*fiber.Error); ok {
		return e
	}
	if asPG(err, &p) && p.Code == "P0409" {
		return fiber.NewError(409, "This request has closed or expired. Ask the buyer to repost it.")
	}
	if asPG(err, &p) && p.Code == "23505" {
		return fiber.NewError(409, "That name or request is already in use")
	}
	// Log diagnostics without SQL arguments, tokens, message bodies or contact data.
	if asPG(err, &p) {
		log.Printf("community database failure code=%s table=%s column=%s constraint=%s", p.Code, p.TableName, p.ColumnName, p.ConstraintName)
	} else {
		log.Printf("community failure type=%T", err)
	}
	return fiber.NewError(503, "Community is temporarily unavailable. Please retry")
}
func asPG(err error, out **pgconn.PgError) bool {
	p, ok := err.(*pgconn.PgError)
	if ok {
		*out = p
	}
	return ok
}
func (s *CommunityController) ensureProfile(ctx context.Context, actor string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO social_profiles(user_id,handle) VALUES($1::uuid,'atl_'||replace($1::text,'-','')) ON CONFLICT(user_id) DO NOTHING`, actor)
	return err
}
func (s *CommunityController) Profile(c *fiber.Ctx) error {
	actor := communityActor(c)
	if err := s.ensureProfile(c.Context(), actor); err != nil {
		return communityError(err)
	}
	var handle, name, avatar string
	var discovery, promotions, verified bool
	err := s.db.QueryRow(c.Context(), `SELECT p.handle,COALESCE(u.full_name,''),COALESCE(u.avatar_url,''),p.discoverable,p.connection_promotions,p.phone_verified_at IS NOT NULL FROM social_profiles p JOIN users u ON u.id=p.user_id WHERE p.user_id=$1::uuid`, actor).Scan(&handle, &name, &avatar, &discovery, &promotions, &verified)
	if err != nil {
		return communityError(err)
	}
	return c.JSON(fiber.Map{"user_id": actor, "handle": handle, "name": name, "avatar_url": avatar, "discoverable": discovery, "connection_promotions": promotions, "phone_verified": verified})
}

var socialHandle = regexp.MustCompile(`^[a-z][a-z0-9_]{2,35}$`)

func (s *CommunityController) UpdateProfile(c *fiber.Ctx) error {
	var in struct {
		Handle       *string `json:"handle"`
		Discoverable *bool   `json:"discoverable"`
		Promotions   *bool   `json:"connection_promotions"`
	}
	if c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	if in.Handle != nil {
		value := strings.ToLower(strings.TrimSpace(*in.Handle))
		if regexp.MustCompile(`^atl_[0-9a-f]{32}$`).MatchString(value) && value != "atl_"+strings.ReplaceAll(communityActor(c), "-", "") {
			return fiber.NewError(422, "Choose a personal username")
		}
		if !socialHandle.MatchString(value) {
			return fiber.NewError(422, "Use 3 to 36 letters, numbers or underscores, starting with a letter")
		}
		in.Handle = &value
	}
	if err := s.ensureProfile(c.Context(), communityActor(c)); err != nil {
		return communityError(err)
	}
	_, err := s.db.Exec(c.Context(), `UPDATE social_profiles SET handle=COALESCE($2,handle),discoverable=COALESCE($3,discoverable),connection_promotions=COALESCE($4,connection_promotions) WHERE user_id=$1::uuid`, communityActor(c), in.Handle, in.Discoverable, in.Promotions)
	if err != nil {
		return communityError(err)
	}
	return s.Profile(c)
}
func (s *CommunityController) FindUser(c *fiber.Ctx) error {
	handle := strings.ToLower(strings.TrimSpace(c.Query("handle")))
	if !socialHandle.MatchString(handle) {
		return fiber.NewError(422, "Enter the exact username")
	}
	var id, name, avatar string
	err := s.db.QueryRow(c.Context(), `SELECT p.user_id::text,u.full_name,u.avatar_url FROM social_profiles p JOIN users u ON u.id=p.user_id WHERE p.handle=$2 AND p.discoverable AND u.is_active AND u.id<>$1::uuid AND NOT EXISTS(SELECT 1 FROM social_blocks b WHERE (b.blocker_id=$1::uuid AND b.blocked_id=u.id) OR (b.blocked_id=$1::uuid AND b.blocker_id=u.id))`, communityActor(c), handle).Scan(&id, &name, &avatar)
	if err != nil {
		return fiber.NewError(404, "No discoverable account uses that username")
	}
	return c.JSON(fiber.Map{"user_id": id, "handle": handle, "name": name, "avatar_url": avatar})
}
func (s *CommunityController) Friends(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	actor := communityActor(c)
	rows, err := s.db.Query(c.Context(), `SELECT f.id::text,peer.id::text,peer.full_name,COALESCE(p.handle,''),peer.avatar_url,f.state,f.requested_by::text,f.updated_at FROM social_connections f JOIN users peer ON peer.id=CASE WHEN f.user_low=$1::uuid THEN f.user_high ELSE f.user_low END LEFT JOIN social_profiles p ON p.user_id=peer.id WHERE $1::uuid IN(f.user_low,f.user_high) AND f.state<>'declined' AND NOT EXISTS(SELECT 1 FROM social_blocks b WHERE (b.blocker_id=$1::uuid AND b.blocked_id=peer.id) OR (b.blocked_id=$1::uuid AND b.blocker_id=peer.id)) AND ($2::timestamptz IS NULL OR (f.updated_at,f.id)<($2,$3::uuid)) ORDER BY f.updated_at DESC,f.id DESC LIMIT $4`, actor, page.CursorTime, nullID(page.CursorID), page.Limit+1)
	if err != nil {
		return communityError(err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	next := ""
	var last time.Time
	var lastID string
	for rows.Next() {
		var id, peer, name, handle, avatar, state, by string
		var at time.Time
		if err = rows.Scan(&id, &peer, &name, &handle, &avatar, &state, &by, &at); err != nil {
			return communityError(err)
		}
		if len(items) == page.Limit {
			next = encodeAdminCursor(last, lastID)
			break
		}
		items = append(items, fiber.Map{"id": id, "user_id": peer, "name": name, "handle": handle, "avatar_url": avatar, "state": state, "incoming": by != actor})
		last, lastID = at, id
	}
	if err = rows.Err(); err != nil {
		return communityError(err)
	}
	return c.JSON(fiber.Map{"items": items, "next_cursor": next, "has_more": next != ""})
}
func nullID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
func (s *CommunityController) RequestFriend(c *fiber.Ctx) error {
	var in struct {
		Handle string `json:"handle"`
	}
	if c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	in.Handle = strings.ToLower(strings.TrimSpace(in.Handle))
	if !socialHandle.MatchString(in.Handle) {
		return fiber.NewError(422, "Enter a username")
	}
	actor := communityActor(c)
	var peer string
	if err := s.db.QueryRow(c.Context(), `SELECT p.user_id::text FROM social_profiles p JOIN users u ON u.id=p.user_id WHERE p.handle=$2 AND p.discoverable AND u.is_active AND u.id<>$1::uuid`, actor, in.Handle).Scan(&peer); err != nil {
		return fiber.ErrNotFound
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	if err = lockSocialPair(c.Context(), tx, actor, peer); err != nil {
		return communityError(err)
	}
	var blocked bool
	err = tx.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM social_blocks WHERE (blocker_id=$1::uuid AND blocked_id=$2::uuid) OR (blocker_id=$2::uuid AND blocked_id=$1::uuid))`, actor, peer).Scan(&blocked)
	if err != nil {
		return communityError(err)
	}
	if blocked {
		return fiber.ErrForbidden
	}
	var id, state, by string
	var requestedAt time.Time
	err = tx.QueryRow(c.Context(), `INSERT INTO social_connections(user_low,user_high,requested_by) VALUES(LEAST($1::uuid,$2::uuid),GREATEST($1::uuid,$2::uuid),$1::uuid) ON CONFLICT(user_low,user_high) DO UPDATE SET state=CASE WHEN social_connections.state='declined' THEN 'pending' ELSE social_connections.state END,requested_by=CASE WHEN social_connections.state='declined' THEN EXCLUDED.requested_by ELSE social_connections.requested_by END,updated_at=CASE WHEN social_connections.state='declined' THEN now() ELSE social_connections.updated_at END RETURNING id::text,state,requested_by::text,updated_at`, actor, peer).Scan(&id, &state, &by, &requestedAt)
	if err != nil {
		return communityError(err)
	}
	if state == "pending" && by == actor {
		if err = insertNotification(c.Context(), tx, peer, "", nil, "friend_request", "Friend request", "Someone wants to connect with you", map[string]any{"view": "messages", "connection_id": id}, "friend-request:"+id+":"+requestedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return communityError(err)
		}
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.JSON(fiber.Map{"id": id, "state": state})
}
func (s *CommunityController) ReplyFriend(c *fiber.Ctx) error {
	id, err := communityID(c, "connection_id")
	if err != nil {
		return err
	}
	var in struct {
		Accept bool `json:"accept"`
	}
	if c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	state := "declined"
	if in.Accept {
		state = "accepted"
	}
	actor := communityActor(c)
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	var low, high string
	err = tx.QueryRow(c.Context(), `SELECT user_low::text,user_high::text FROM social_connections WHERE id=$1::uuid AND $2::uuid IN(user_low,user_high)`, id, actor).Scan(&low, &high)
	if err == pgx.ErrNoRows {
		return fiber.NewError(409, "This request is no longer pending")
	}
	if err != nil {
		return communityError(err)
	}
	if err = lockSocialPair(c.Context(), tx, low, high); err != nil {
		return communityError(err)
	}
	result, err := tx.Exec(c.Context(), `UPDATE social_connections f SET state=$3,updated_at=now() WHERE id=$2::uuid AND $1::uuid IN(user_low,user_high) AND requested_by<>$1::uuid AND state='pending' AND NOT EXISTS(SELECT 1 FROM social_blocks b WHERE (b.blocker_id=f.user_low AND b.blocked_id=f.user_high) OR (b.blocker_id=f.user_high AND b.blocked_id=f.user_low))`, actor, id, state)
	if err != nil {
		return communityError(err)
	}
	if result.RowsAffected() != 1 {
		return fiber.NewError(409, "This request is no longer pending")
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.SendStatus(204)
}
func (s *CommunityController) Block(c *fiber.Ctx) error {
	peer, err := communityID(c, "user_id")
	if err != nil {
		return err
	}
	actor := communityActor(c)
	if peer == actor {
		return fiber.ErrBadRequest
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	if err = lockSocialPair(c.Context(), tx, actor, peer); err != nil {
		return communityError(err)
	}
	// Serialize with sends that already locked the direct room. Once this
	// transaction commits, the next sender must observe the block.
	_, err = tx.Exec(c.Context(), `SELECT id FROM chat_rooms WHERE kind='direct' AND direct_low=LEAST($1::uuid,$2::uuid) AND direct_high=GREATEST($1::uuid,$2::uuid) FOR UPDATE`, actor, peer)
	if err != nil {
		return communityError(err)
	}
	if c.Method() == fiber.MethodDelete {
		_, err = tx.Exec(c.Context(), `DELETE FROM social_blocks WHERE blocker_id=$1::uuid AND blocked_id=$2::uuid`, actor, peer)
	} else {
		_, err = tx.Exec(c.Context(), `INSERT INTO social_blocks(blocker_id,blocked_id) VALUES($1::uuid,$2::uuid) ON CONFLICT DO NOTHING`, actor, peer)
		if err == nil {
			_, err = tx.Exec(c.Context(), `UPDATE social_connections SET state='declined',updated_at=now() WHERE user_low=LEAST($1::uuid,$2::uuid) AND user_high=GREATEST($1::uuid,$2::uuid)`, actor, peer)
		}
	}
	if err == nil {
		err = tx.Commit(c.Context())
	}
	if err != nil {
		return communityError(err)
	}
	return c.SendStatus(204)
}
func lockSocialPair(ctx context.Context, tx pgx.Tx, actor, peer string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(LEAST($1::uuid,$2::uuid)::text||':'||GREATEST($1::uuid,$2::uuid)::text,0))`, actor, peer)
	return err
}
func (s *CommunityController) Blocked(c *fiber.Ctx) error {
	rows, err := s.db.Query(c.Context(), `SELECT b.blocked_id::text,u.full_name,COALESCE(p.handle,'') FROM social_blocks b JOIN users u ON u.id=b.blocked_id LEFT JOIN social_profiles p ON p.user_id=u.id WHERE b.blocker_id=$1::uuid ORDER BY b.created_at DESC LIMIT 100`, communityActor(c))
	if err != nil {
		return communityError(err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var id, name, handle string
		if err = rows.Scan(&id, &name, &handle); err != nil {
			return communityError(err)
		}
		items = append(items, fiber.Map{"user_id": id, "name": name, "handle": handle})
	}
	if rows.Err() != nil {
		return communityError(rows.Err())
	}
	return c.JSON(fiber.Map{"items": items})
}
func (s *CommunityController) RemoveFriend(c *fiber.Ctx) error {
	id, err := communityID(c, "connection_id")
	if err != nil {
		return err
	}
	result, err := s.db.Exec(c.Context(), `UPDATE social_connections SET state='declined',updated_at=now() WHERE id=$2::uuid AND $1::uuid IN(user_low,user_high)`, communityActor(c), id)
	if err != nil {
		return communityError(err)
	}
	if result.RowsAffected() != 1 {
		return fiber.ErrNotFound
	}
	return c.SendStatus(204)
}
func ensureDirectTx(ctx context.Context, tx pgx.Tx, actor, peer string, data map[string]any) (string, error) {
	raw, _ := json.Marshal(data)
	if data == nil {
		raw = []byte("{}")
	}
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO chat_rooms(kind,owner_user_id,direct_low,direct_high,context) VALUES('direct',$1::uuid,LEAST($1::uuid,$2::uuid),GREATEST($1::uuid,$2::uuid),$3::jsonb) ON CONFLICT(direct_low,direct_high) WHERE kind='direct' DO UPDATE SET context=chat_rooms.context||EXCLUDED.context RETURNING id::text`, actor, peer, raw).Scan(&id)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,role) VALUES($1::uuid,$2::uuid,'member'),($1::uuid,$3::uuid,'member') ON CONFLICT(room_id,user_id) DO NOTHING`, id, actor, peer)
	return id, err
}
func (s *CommunityController) StartDirect(c *fiber.Ctx) error {
	var in struct {
		UserID string `json:"user_id"`
	}
	if c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	actor := communityActor(c)
	if _, err := uuid.Parse(in.UserID); err != nil || actor == in.UserID {
		return fiber.ErrBadRequest
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	if err = lockSocialPair(c.Context(), tx, actor, in.UserID); err != nil {
		return communityError(err)
	}
	var permitted bool
	err = tx.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM social_connections WHERE user_low=LEAST($1::uuid,$2::uuid) AND user_high=GREATEST($1::uuid,$2::uuid) AND state='accepted') AND NOT EXISTS(SELECT 1 FROM social_blocks WHERE (blocker_id=$1::uuid AND blocked_id=$2::uuid) OR (blocker_id=$2::uuid AND blocked_id=$1::uuid))`, actor, in.UserID).Scan(&permitted)
	if err != nil {
		return communityError(err)
	}
	if !permitted {
		return fiber.NewError(403, "Connect with this friend before opening a private chat")
	}
	id, err := ensureDirectTx(c.Context(), tx, actor, in.UserID, nil)
	if err != nil {
		return communityError(err)
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.JSON(fiber.Map{"id": id})
}

// lock first, then authorize with a fresh statement. Removal/closure use the
// same room lock, so a delayed sender cannot rely on stale admission state.
func (s *CommunityController) lockMember(ctx context.Context, tx pgx.Tx, room, actor string) (string, string, string, error) {
	var kind, state, role, provider string
	err := tx.QueryRow(ctx, `SELECT kind,CASE WHEN EXISTS(SELECT 1 FROM town_hall_requests h WHERE h.room_id=chat_rooms.id AND h.engagement_deadline<=clock_timestamp()) THEN 'closed' ELSE state END FROM chat_rooms WHERE id=$1::uuid FOR UPDATE`, room).Scan(&kind, &state)
	if err != nil {
		return "", "", "", err
	}
	if state != "open" {
		return "", "", "", fiber.NewError(409, "This request is closed")
	}
	err = tx.QueryRow(ctx, `SELECT role,COALESCE(provider_id::text,'') FROM chat_participants WHERE room_id=$1::uuid AND user_id=$2::uuid AND state='active'`, room, actor).Scan(&role, &provider)
	if err != nil {
		return "", "", "", fiber.ErrForbidden
	}
	if kind == "direct" {
		var blocked bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_blocks b JOIN chat_rooms r ON r.id=$1::uuid WHERE (b.blocker_id=r.direct_low AND b.blocked_id=r.direct_high) OR (b.blocker_id=r.direct_high AND b.blocked_id=r.direct_low))`, room).Scan(&blocked)
		if err != nil {
			return "", "", "", err
		}
		if blocked {
			return "", "", "", fiber.NewError(403, "This conversation is blocked")
		}
	}
	if role == "provider" {
		if kind == "town_hall" {
			if err = independentHallProvider(ctx, tx, room, provider); err != nil {
				return "", "", "", err
			}
		}
		var eligible bool
		err = tx.QueryRow(ctx, `SELECT verified AND paid_active AND badge_tier IN('bronze','silver','gold','diamond') AND EXISTS(SELECT 1 FROM provider_subscriptions sub WHERE sub.provider_id=$1::uuid AND sub.status='active' AND sub.current_period_end>clock_timestamp() AND (sub.starts_at IS NULL OR sub.starts_at<=clock_timestamp()) AND EXISTS(SELECT 1 FROM provider_subscription_payments pay WHERE pay.subscription_id=sub.id AND pay.amount>0 AND pay.currency_code='NGN')) FROM provider_public_identity WHERE provider_id=$1::uuid`, provider).Scan(&eligible)
		if err != nil || !eligible {
			return "", "", "", fiber.NewError(402, "Renew your paid plan before sending Town Hall messages or offers")
		}
	}
	return kind, role, provider, nil
}
func (s *CommunityController) legacyActor(c *fiber.Ctx) (bool, error) {
	var buyer, provider string
	err := s.db.QueryRow(c.Context(), `SELECT user_id::text,provider_id::text FROM provider_conversations WHERE id=$1::uuid`, c.Params("conversation_id")).Scan(&buyer, &provider)
	if err != nil {
		return false, fiber.ErrNotFound
	}
	if buyer == communityActor(c) {
		return false, nil
	}
	p, _, err := s.market.providerForUser(c.Context(), communityActor(c))
	if err != nil || p != provider {
		return false, fiber.ErrForbidden
	}
	return true, nil
}
func (s *CommunityController) isShared(c *fiber.Ctx) (bool, error) {
	id, err := communityID(c, "conversation_id")
	if err != nil {
		return false, err
	}
	var yes bool
	err = s.db.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM chat_rooms WHERE id=$1::uuid)`, id).Scan(&yes)
	return yes, err
}
func (s *CommunityController) Inbox(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	actor := communityActor(c)
	provider, _, _ := s.market.providerForUser(c.Context(), actor)
	// Keep the authorized base inventory/cursor predicates unchanged. Defer unread
	// counts to the returned page; bound each physical thread's latest-message
	// scan before merging, using the same ownership/grouping helper.
	rows, err := s.db.Query(c.Context(), `WITH inbox AS (
 SELECT r.id,r.kind,'shared'::text AS source,r.last_message_at,
 CASE WHEN r.kind='town_hall' THEN r.title ELSE COALESCE((SELECT u.full_name FROM chat_participants peer JOIN users u ON u.id=peer.user_id WHERE peer.room_id=r.id AND peer.user_id<>$1::uuid LIMIT 1),'Private chat') END AS title,
 COALESCE((SELECT peer.user_id::text FROM chat_participants peer WHERE peer.room_id=r.id AND peer.user_id<>$1::uuid AND r.kind='direct' LIMIT 1),'') AS peer_id,
 COALESCE((SELECT o.id::text FROM provider_organizations o JOIN chat_participants peer ON peer.user_id=o.owner_user_id WHERE peer.room_id=r.id AND peer.user_id<>$1::uuid AND r.kind='direct' AND o.is_active LIMIT 1),'') AS provider_id,'member'::text AS actor_role,r.state,
 member.last_read_at AS shared_last_read_at,r.context
 FROM chat_rooms r JOIN chat_participants member ON member.room_id=r.id AND member.user_id=$1::uuid AND member.state='active'
 WHERE r.kind<>'town_hall' OR (r.state='open' AND EXISTS(SELECT 1 FROM town_hall_requests h WHERE h.room_id=r.id AND h.engagement_deadline>clock_timestamp()))
 UNION ALL
 SELECT c.id,'business','legacy',c.last_message_at,CASE WHEN c.user_id=$1::uuid THEN p.business_name ELSE COALESCE(u.full_name,'Customer') END,CASE WHEN c.user_id=$1::uuid THEN p.owner_user_id::text ELSE c.user_id::text END,c.provider_id::text,
 CASE WHEN c.user_id=$1::uuid THEN 'buyer' ELSE 'provider' END,c.status,
 NULL::timestamptz,
 jsonb_build_object('listing_id',c.listing_id,'product_id',c.product_id,'title',COALESCE(l.title,pr.title))
 FROM provider_conversations c JOIN provider_organizations p ON p.id=c.provider_id JOIN users u ON u.id=c.user_id LEFT JOIN provider_listings l ON l.id=c.listing_id LEFT JOIN products pr ON pr.id=c.product_id
 WHERE (c.user_id=$1::uuid OR c.provider_id=NULLIF($2,'')::uuid) AND c.id::text=COALESCE(to_jsonb(c)->>'thread_id',c.id::text)
 ) SELECT id::text,kind,source,last_message_at,title,peer_id,provider_id,actor_role,state,CASE WHEN source='shared' THEN
 (SELECT count(*)::int FROM chat_messages msg WHERE msg.room_id=inbox.id AND msg.sender_user_id<>$1::uuid AND msg.created_at>COALESCE(inbox.shared_last_read_at,'epoch'::timestamptz))
 ELSE (SELECT count(*)::int FROM provider_conversation_messages msg JOIN provider_conversations physical ON physical.id=msg.conversation_id WHERE physical.id IN (`+businessThreadMembers("inbox.id")+`) AND msg.sender_type=CASE WHEN inbox.actor_role='buyer' THEN 'provider' ELSE 'buyer' END AND msg.created_at>COALESCE(CASE WHEN inbox.actor_role='buyer' THEN physical.buyer_last_read_at ELSE physical.provider_last_read_at END,'epoch'::timestamptz)) END AS unread_count,context,COALESCE((SELECT u.avatar_url FROM users u WHERE u.id=NULLIF(inbox.peer_id,'')::uuid),''),COALESCE(CASE WHEN source='shared' THEN (SELECT jsonb_build_object('id',msg.id,'body',msg.body,'is_mine',msg.sender_user_id=$1::uuid) FROM chat_messages msg WHERE msg.room_id=inbox.id ORDER BY msg.created_at DESC,msg.id DESC LIMIT 1) ELSE (SELECT jsonb_build_object('id',msg.id,'body',to_jsonb(msg)->>'body','is_mine',msg.sender_type=inbox.actor_role) FROM (`+businessThreadMembers("inbox.id")+`) physical CROSS JOIN LATERAL (SELECT msg.* FROM provider_conversation_messages msg WHERE msg.conversation_id=physical.id ORDER BY msg.created_at DESC,msg.id DESC LIMIT 1) msg ORDER BY msg.created_at DESC,msg.id DESC LIMIT 1) END,'{}'::jsonb) FROM inbox WHERE ($3::timestamptz IS NULL OR (last_message_at,id)<($3,$4::uuid)) AND ($6='' OR id=NULLIF($6,'')::uuid OR (source='legacy' AND NULLIF($6,'')::uuid IN (`+businessThreadMembers("inbox.id")+`))) ORDER BY last_message_at DESC,id DESC LIMIT $5`, actor, provider, page.CursorTime, nullID(page.CursorID), page.Limit+1, c.Query("conversation_id"))
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
func (s *CommunityController) Messages(c *fiber.Ctx) error {
	shared, err := s.isShared(c)
	if err != nil {
		return communityError(err)
	}
	if !shared {
		provider, err := s.legacyActor(c)
		if err != nil {
			return err
		}
		if provider {
			return s.market.ProviderConversationMessages(c)
		}
		return s.market.BuyerConversationMessages(c)
	}
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	id := c.Params("conversation_id")
	actor := communityActor(c)
	var revision int64
	var kind, state, role string
	var owner string
	err = s.db.QueryRow(c.Context(), `SELECT r.revision,r.kind,CASE WHEN EXISTS(SELECT 1 FROM town_hall_requests h WHERE h.room_id=r.id AND h.engagement_deadline<=clock_timestamp()) THEN 'closed' ELSE r.state END,p.role,r.owner_user_id::text FROM chat_rooms r JOIN chat_participants p ON p.room_id=r.id AND p.user_id=$2::uuid AND p.state='active' WHERE r.id=$1::uuid`, id, actor).Scan(&revision, &kind, &state, &role, &owner)
	if err != nil {
		return fiber.ErrForbidden
	}
	if kind == "town_hall" && state != "open" {
		return fiber.NewError(410, "This Town Hall request has ended or expired")
	}
	canSend := state == "open"
	if role == "provider" {
		var active bool
		err = s.db.QueryRow(c.Context(), `SELECT COALESCE(bool_or(i.verified AND i.paid_active),false) FROM chat_participants p JOIN provider_public_identity i ON i.provider_id=p.provider_id WHERE p.room_id=$1::uuid AND p.user_id=$2::uuid`, id, actor).Scan(&active)
		canSend = canSend && err == nil && active
	}
	if kind == "direct" {
		var blocked bool
		if err = s.db.QueryRow(c.Context(), `SELECT EXISTS(SELECT 1 FROM social_blocks b JOIN chat_rooms r ON r.id=$1::uuid WHERE (b.blocker_id=r.direct_low AND b.blocked_id=r.direct_high) OR (b.blocker_id=r.direct_high AND b.blocked_id=r.direct_low))`, id).Scan(&blocked); err != nil {
			return communityError(err)
		}
		canSend = canSend && !blocked
	}
	etag := fmt.Sprintf(`"chat-%d-%t-%d"`, revision, canSend, time.Now().Unix()/600)
	c.Set("ETag", etag)
	c.Set("Cache-Control", "private, no-store")
	if c.Get("If-None-Match") == etag && page.CursorTime == nil {
		return c.SendStatus(304)
	}
	rows, err := s.db.Query(c.Context(), `SELECT m.id::text,m.sender_user_id::text,COALESCE(u.full_name,'Member'),m.body,m.media_keys,m.created_at,to_jsonb(m)->'location',CASE WHEN parent.id IS NOT NULL THEN jsonb_build_object('id',parent.id,'sender_name',COALESCE(parent_user.full_name,'Member'),'body',left(parent.body,240),'has_media',cardinality(parent.media_keys)>0,'has_location',to_jsonb(parent)->'location' IS NOT NULL AND to_jsonb(parent)->'location'<>'null'::jsonb) END FROM chat_messages m LEFT JOIN chat_messages parent ON parent.id=m.reply_to_id AND parent.room_id=m.room_id LEFT JOIN users parent_user ON parent_user.id=parent.sender_user_id JOIN users u ON u.id=m.sender_user_id WHERE m.room_id=$1::uuid AND EXISTS(SELECT 1 FROM chat_participants WHERE room_id=m.room_id AND user_id=$2::uuid AND state='active') AND ($3::timestamptz IS NULL OR (m.created_at,m.id)<($3,$4::uuid)) ORDER BY m.created_at DESC,m.id DESC LIMIT $5`, id, actor, page.CursorTime, nullID(page.CursorID), page.Limit+1)
	if err != nil {
		return communityError(err)
	}
	defer rows.Close()
	items := []fiber.Map{}
	next := ""
	var last time.Time
	var lastID string
	for rows.Next() {
		var mid, sender, name, body string
		var keys []string
		var location, reply json.RawMessage
		var at time.Time
		if err = rows.Scan(&mid, &sender, &name, &body, &keys, &at, &location, &reply); err != nil {
			return communityError(err)
		}
		if len(items) == page.Limit {
			next = encodeAdminCursor(last, lastID)
			break
		}
		items = append(items, fiber.Map{"id": mid, "sender_user_id": sender, "sender_name": name, "body": body, "media_urls": s.market.chatMediaURLs(keys), "location": location, "reply_to": reply, "created_at": at, "is_mine": sender == actor, "sender_type": "member"})
		last, lastID = at, mid
	}
	if err = rows.Err(); err != nil {
		return communityError(err)
	}
	rows.Close()
	return c.JSON(fiber.Map{"items": items, "next_cursor": next, "has_more": next != "", "kind": kind, "can_send": canSend, "is_owner": owner == actor})
}
func (s *CommunityController) Send(c *fiber.Ctx) error {
	shared, err := s.isShared(c)
	if err != nil {
		return communityError(err)
	}
	if !shared {
		provider, err := s.legacyActor(c)
		if err != nil {
			return err
		}
		if provider {
			return s.market.SendProviderConversationMessage(c)
		}
		return s.market.SendBuyerConversationMessage(c)
	}
	input, err := s.market.parseConversationMessage(c)
	if err != nil {
		return err
	}
	if input.ClientID == "" {
		return fiber.NewError(422, "A message reference is required")
	}
	actor := communityActor(c)
	room := c.Params("conversation_id")
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	if _, _, _, err = s.lockMember(c.Context(), tx, room, actor); err != nil {
		return communityError(err)
	}
	var reply json.RawMessage
	if input.ReplyTo != "" {
		err = tx.QueryRow(c.Context(), `SELECT jsonb_build_object('id',m.id,'sender_name',COALESCE(u.full_name,'Member'),'body',left(m.body,240),'has_media',cardinality(m.media_keys)>0,'has_location',to_jsonb(m)->'location' IS NOT NULL AND to_jsonb(m)->'location'<>'null'::jsonb) FROM chat_messages m JOIN chat_rooms r ON r.id=m.room_id JOIN users u ON u.id=m.sender_user_id WHERE m.id=$1::uuid AND m.room_id=$2::uuid AND r.kind='town_hall'`, input.ReplyTo, room).Scan(&reply)
		if err == pgx.ErrNoRows {
			return fiber.NewError(422, "That message is unavailable in this Town Hall room. Choose another message.")
		}
		if err != nil {
			return communityError(err)
		}
	}
	mid, at, fresh, err := insertSharedMessage(c.Context(), tx, room, actor, input)
	if err != nil {
		return communityError(err)
	}
	if fresh {
		if err = s.notifyRoom(c.Context(), tx, room, actor, mid, "New chat message", input.Body); err != nil {
			return communityError(err)
		}
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.Status(201).JSON(fiber.Map{"id": mid, "body": input.Body, "created_at": at, "is_mine": true, "media_urls": s.market.chatMediaURLs(input.MediaKeys), "location": input.Location, "reply_to": reply})
}
func insertSharedMessage(ctx context.Context, tx pgx.Tx, room, actor string, input conversationMessageInput) (string, time.Time, bool, error) {
	requested := uuid.NewString()
	var id string
	var at time.Time
	err := tx.QueryRow(ctx, `INSERT INTO chat_messages(id,room_id,sender_user_id,body,media_keys,client_message_id,location,reply_to_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid,$7::jsonb,NULLIF($8,'')::uuid) ON CONFLICT(sender_user_id,client_message_id) DO UPDATE SET id=chat_messages.id WHERE chat_messages.room_id=EXCLUDED.room_id AND chat_messages.body=EXCLUDED.body AND chat_messages.media_keys=EXCLUDED.media_keys AND chat_messages.location IS NOT DISTINCT FROM EXCLUDED.location AND chat_messages.reply_to_id IS NOT DISTINCT FROM EXCLUDED.reply_to_id RETURNING id::text,created_at`, requested, room, actor, input.Body, input.MediaKeys, input.ClientID, chatLocationJSON(input.Location), input.ReplyTo).Scan(&id, &at)
	if err == pgx.ErrNoRows {
		return "", at, false, fiber.NewError(409, "Message reference already used")
	}
	if err != nil {
		return "", at, false, err
	}
	fresh := id == requested
	if fresh {
		_, err = tx.Exec(ctx, `UPDATE chat_rooms SET revision=revision+1,last_message_at=$2 WHERE id=$1::uuid;`, room, at)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE chat_participants SET last_read_at=GREATEST(COALESCE(last_read_at,'epoch'::timestamptz),$3) WHERE room_id=$1::uuid AND user_id=$2::uuid`, room, actor, at)
		}
	}
	return id, at, fresh, err
}

// A normal direct/Town Hall room fits in one batch. The bound also protects
// transaction round trips if the recipient model grows later. Cursor progress
// follows selected recipients, not inserted rows, so retries cannot skip a page.
const communityNotificationBatchSize = 64
const communityNotificationBatchSQL = `WITH recipients AS MATERIALIZED (
 SELECT p.user_id FROM chat_participants p
 WHERE p.room_id=$1::uuid AND p.state='active' AND p.user_id<>$2::uuid
 AND ($6::uuid IS NULL OR p.user_id>$6::uuid)
 AND NOT EXISTS(SELECT 1 FROM social_blocks b WHERE (b.blocker_id=p.user_id AND b.blocked_id=$2::uuid) OR (b.blocked_id=p.user_id AND b.blocker_id=$2::uuid))
 ORDER BY p.user_id LIMIT $7
), inserted AS (
 INSERT INTO notifications(user_id,order_id,batch_id,type,title,body,data,event_key)
 SELECT user_id,NULL::uuid,NULL::uuid,'social_message',$4::text,'Open your chat to read the message.',$5::jsonb,
 'shared-chat:'||$3::text||':'||user_id::text FROM recipients
 ON CONFLICT DO NOTHING RETURNING id
)
SELECT (SELECT user_id::text FROM recipients ORDER BY user_id DESC LIMIT 1),
 (SELECT count(*) FROM recipients),(SELECT count(*) FROM inserted)`

func (s *CommunityController) notifyRoom(ctx context.Context, tx pgx.Tx, room, actor, message, title, body string) error {
	// All batches remain in the caller's business transaction. Per-row database
	// triggers still create each recipient's durable delivery records.
	data, err := marshalNotificationData(map[string]any{"view": "messages", "chat_room_id": room, "sender_user_id": actor})
	if err != nil {
		return err
	}
	var cursor *string
	for {
		var last *string
		var selected, inserted int
		if err = tx.QueryRow(ctx, communityNotificationBatchSQL, room, actor, message, title, data, cursor, communityNotificationBatchSize).Scan(&last, &selected, &inserted); err != nil {
			return err
		}
		if selected < communityNotificationBatchSize {
			return nil
		}
		cursor = last
	}
}

func (s *CommunityController) MarkRead(c *fiber.Ctx) error {
	shared, err := s.isShared(c)
	if err != nil {
		return communityError(err)
	}
	if !shared {
		provider, err := s.legacyActor(c)
		if err != nil {
			return err
		}
		if provider {
			return s.market.ProviderMarkConversationRead(c)
		}
		return s.market.BuyerMarkConversationRead(c)
	}
	var input struct {
		MessageID string `json:"message_id"`
	}
	if c.BodyParser(&input) != nil {
		return fiber.ErrBadRequest
	}
	if _, err := uuid.Parse(input.MessageID); err != nil {
		return fiber.ErrBadRequest
	}
	result, err := s.db.Exec(c.Context(), `UPDATE chat_participants p SET last_read_at=GREATEST(COALESCE(p.last_read_at,'epoch'::timestamptz),m.created_at) FROM chat_messages m WHERE p.room_id=$1::uuid AND p.user_id=$2::uuid AND p.state='active' AND m.room_id=p.room_id AND m.id=$3::uuid`, c.Params("conversation_id"), communityActor(c), input.MessageID)
	if err != nil {
		return communityError(err)
	}
	if result.RowsAffected() != 1 {
		return fiber.ErrForbidden
	}
	return c.SendStatus(204)
}
func (s *CommunityController) Report(c *fiber.Ctx) error {
	id, err := communityID(c, "conversation_id")
	if err != nil {
		return err
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if c.BodyParser(&input) != nil || len([]rune(strings.TrimSpace(input.Reason))) < 3 || len([]rune(input.Reason)) > 1000 {
		return fiber.NewError(422, "Describe the problem in up to 1000 characters")
	}
	result, err := s.db.Exec(c.Context(), `INSERT INTO chat_reports(reporter_user_id,room_id,reason) SELECT $1::uuid,r.id,$3 FROM chat_rooms r WHERE r.id=$2::uuid AND (r.owner_user_id=$1::uuid OR EXISTS(SELECT 1 FROM chat_participants p WHERE p.room_id=r.id AND p.user_id=$1::uuid))`, communityActor(c), id, strings.TrimSpace(input.Reason))
	if err != nil {
		return communityError(err)
	}
	if result.RowsAffected() != 1 {
		return fiber.ErrForbidden
	}
	return c.SendStatus(201)
}
