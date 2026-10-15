package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"math"
	"strings"
	"time"
)

func (s *CommunityController) HallCreate(c *fiber.Ctx) error {
	var in struct {
		Message     string  `json:"message"`
		ClientID    string  `json:"client_request_id"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Category    string  `json:"category"`
		Location    string  `json:"location"`
		Quantity    float64 `json:"quantity"`
		Unit        string  `json:"unit"`
		Price       float64 `json:"target_unit_price"`
	}
	if c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	if _, err := uuid.Parse(in.ClientID); err != nil {
		return fiber.NewError(422, "A request reference is required")
	}
	in.Title = strings.TrimSpace(in.Title)
	messageOnly := strings.TrimSpace(in.Message) != ""
	if messageOnly {
		in.Description = strings.TrimSpace(in.Message)
		title := []rune(strings.Join(strings.Fields(in.Description), " "))
		if len(title) > 80 {
			title = title[:80]
		}
		in.Title = string(title)
		in.Location, in.Unit, in.Category = "", "", ""
		in.Quantity, in.Price = 0, 0
	}
	in.Description = strings.TrimSpace(in.Description)
	in.Location = strings.TrimSpace(in.Location)
	in.Unit = strings.TrimSpace(in.Unit)
	in.Quantity = math.Round(in.Quantity*1000) / 1000
	in.Price = math.Round(in.Price*100) / 100
	if len([]rune(in.Title)) < 3 || len([]rune(in.Title)) > 160 || len([]rune(in.Description)) < 1 || len([]rune(in.Description)) > 2000 || (!messageOnly && (in.Location == "" || len(in.Location) > 200 || in.Unit == "" || len(in.Unit) > 40 || len(in.Category) > 100 || in.Quantity <= 0 || in.Quantity > 1e9 || in.Price <= 0 || in.Price > 1e10)) {
		return fiber.NewError(422, "Enter a title, description, location, quantity, unit and target price")
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	id := uuid.NewString()
	actor := communityActor(c)
	requested := id
	err = tx.QueryRow(c.Context(), `INSERT INTO chat_rooms(id,kind,owner_user_id,title,client_request_id) VALUES($1::uuid,'town_hall',$2::uuid,$3,$4::uuid) ON CONFLICT(owner_user_id,client_request_id) WHERE kind='town_hall' DO UPDATE SET id=chat_rooms.id WHERE chat_rooms.title=EXCLUDED.title RETURNING id::text`, id, actor, in.Title, in.ClientID).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return fiber.NewError(409, "Request reference already used")
		}
		return communityError(err)
	}
	if id != requested {
		var same bool
		err = tx.QueryRow(c.Context(), `SELECT description=$2 AND location=$3 AND COALESCE(quantity,0)=$4 AND unit=$5 AND COALESCE(target_unit_price,0)=$6 AND category=$7 AND message_only=$8 FROM town_hall_requests WHERE room_id=$1::uuid`, id, in.Description, in.Location, in.Quantity, in.Unit, in.Price, in.Category, messageOnly).Scan(&same)
		if err != nil {
			return communityError(err)
		}
		if !same {
			return fiber.NewError(409, "Request reference already used")
		}
		return c.Status(201).JSON(fiber.Map{"id": id})
	}
	_, err = tx.Exec(c.Context(), `INSERT INTO town_hall_requests(room_id,description,category,location,quantity,unit,target_unit_price,message_only) VALUES($1::uuid,$2,$3,$4,NULLIF($5::numeric,0),$6,NULLIF($7::numeric,0),$8)`, id, in.Description, in.Category, in.Location, in.Quantity, in.Unit, in.Price, messageOnly)
	if err != nil {
		return communityError(err)
	}
	_, err = tx.Exec(c.Context(), `INSERT INTO chat_participants(room_id,user_id,role) VALUES($1::uuid,$2::uuid,'buyer')`, id, actor)
	if err != nil {
		return communityError(err)
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.Status(201).JSON(fiber.Map{"id": id})
}

const hallPreviewSQL = `SELECT jsonb_build_object('id',r.id,'title',r.title,'state',CASE WHEN h.first_provider_engaged_at IS NULL AND h.engagement_deadline<=clock_timestamp() AND r.state='open' THEN 'closed' ELSE r.state END,'created_at',r.created_at,
 'expired',h.expired_at IS NOT NULL OR (r.state='open' AND h.first_provider_engaged_at IS NULL AND h.engagement_deadline<=clock_timestamp()),
 'expires_at',CASE WHEN r.state='open' AND h.first_provider_engaged_at IS NULL THEN h.engagement_deadline END,
 'provider_engaged',h.first_provider_engaged_at IS NOT NULL,'is_owner',r.owner_user_id=$1::uuid,
 'buyer_name',buyer.full_name, 'buyer_avatar_url',buyer.avatar_url,
 'description',h.description,'message_only',h.message_only,'category',h.category,'location',h.location,'quantity',h.quantity,'unit',h.unit,'currency_code',h.currency_code,'target_unit_price',h.target_unit_price,
 'seats',(SELECT count(*) FROM chat_participants p WHERE p.room_id=r.id AND p.role='provider' AND p.state='active'),
 'admitted',EXISTS(SELECT 1 FROM chat_participants p WHERE p.room_id=r.id AND p.user_id=$1::uuid AND p.state='active'),
 'offers',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',o.id,'provider_id',o.provider_id,'provider_name',i.name,'badge_tier',i.badge_tier,'provider_identity',jsonb_build_object('id',i.provider_id,'name',i.name,'avatar_url',i.avatar_url,'verified',i.verified,'paid_active',i.paid_active,'tier',i.badge_tier),'unit_price',o.unit_price,'terms',o.terms,'updated_at',o.updated_at) ORDER BY o.unit_price,o.id) FROM town_hall_offers o JOIN provider_public_identity i ON i.provider_id=o.provider_id WHERE o.room_id=r.id AND (r.owner_user_id=$1::uuid OR EXISTS(SELECT 1 FROM chat_participants p WHERE p.room_id=o.room_id AND p.provider_id=o.provider_id AND p.state='active'))),'[]'::jsonb))
 FROM chat_rooms r JOIN town_hall_requests h ON h.room_id=r.id JOIN users buyer ON buyer.id=r.owner_user_id`

func (s *CommunityController) HallList(c *fiber.Ctx) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	mine := c.Query("mine") == "true"
	rows, err := s.db.Query(c.Context(), hallPreviewSQL+` WHERE (CASE WHEN $5::boolean THEN r.owner_user_id=$1::uuid ELSE r.state='open' AND (h.first_provider_engaged_at IS NOT NULL OR h.engagement_deadline>clock_timestamp()) END)
 AND ($2::timestamptz IS NULL OR (r.created_at,r.id)<($2,$3::uuid)) ORDER BY r.created_at DESC,r.id DESC LIMIT $4`, communityActor(c), page.CursorTime, nullID(page.CursorID), page.Limit+1, mine)
	if err != nil {
		return communityError(err)
	}
	defer rows.Close()
	items := []json.RawMessage{}
	next := ""
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return communityError(err)
		}
		if len(items) == page.Limit {
			var last struct {
				ID string    `json:"id"`
				At time.Time `json:"created_at"`
			}
			_ = json.Unmarshal(items[len(items)-1], &last)
			next = encodeAdminCursor(last.At, last.ID)
			break
		}
		items = append(items, json.RawMessage(raw))
	}
	if rows.Err() != nil {
		return communityError(rows.Err())
	}
	rows.Close()
	_, _ = s.db.Exec(c.Context(), `UPDATE social_profiles SET town_hall_seen_at=now() WHERE user_id=$1::uuid`, communityActor(c))
	payload, err := json.Marshal(fiber.Map{"items": items, "next_cursor": next})
	if err != nil {
		return communityError(err)
	}
	etag := fmt.Sprintf(`"hall-%x"`, sha256.Sum256(payload))
	c.Set("ETag", etag)
	c.Set("Cache-Control", "private, no-store")
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(304)
	}
	c.Type("json")
	return c.Send(payload)
}
func (s *CommunityController) HallActivity(c *fiber.Ctx) error {
	if err := s.ensureProfile(c.Context(), communityActor(c)); err != nil {
		return communityError(err)
	}
	var count int
	err := s.db.QueryRow(c.Context(), `SELECT count(*) FROM (SELECT r.id FROM chat_rooms r WHERE r.kind='town_hall' AND r.state='open' AND EXISTS(SELECT 1 FROM town_hall_requests h WHERE h.room_id=r.id AND (h.first_provider_engaged_at IS NOT NULL OR h.engagement_deadline>clock_timestamp())) AND r.last_message_at>(SELECT town_hall_seen_at FROM social_profiles WHERE user_id=$1::uuid) LIMIT 100) recent`, communityActor(c)).Scan(&count)
	if err != nil {
		return communityError(err)
	}
	return c.JSON(fiber.Map{"count": count})
}
func (s *CommunityController) HallDetail(c *fiber.Ctx) error {
	id, err := communityID(c, "room_id")
	if err != nil {
		return err
	}
	actor := communityActor(c)
	var raw []byte
	err = s.db.QueryRow(c.Context(), hallPreviewSQL+` WHERE r.id=$2::uuid AND (r.owner_user_id=$1::uuid OR (r.state='open' AND (h.first_provider_engaged_at IS NOT NULL OR h.engagement_deadline>clock_timestamp())))`, actor, id).Scan(&raw)
	if err != nil {
		return communityError(err)
	}
	var detail fiber.Map
	_ = json.Unmarshal(raw, &detail)
	if detail["is_owner"] == true {
		rows, e := s.db.Query(c.Context(), `SELECT a.provider_id::text,i.name,a.state FROM town_hall_applications a JOIN provider_public_identity i ON i.provider_id=a.provider_id WHERE a.room_id=$1::uuid ORDER BY a.created_at LIMIT 100`, id)
		if e != nil {
			return communityError(e)
		}
		apps := []fiber.Map{}
		for rows.Next() {
			var pid, name, state string
			if e = rows.Scan(&pid, &name, &state); e != nil {
				rows.Close()
				return communityError(e)
			}
			apps = append(apps, fiber.Map{"provider_id": pid, "name": name, "state": state})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return communityError(e)
		}
		detail["applications"] = apps
	}
	var state string
	_ = s.db.QueryRow(c.Context(), `SELECT a.state FROM town_hall_applications a JOIN provider_members pm ON pm.provider_id=a.provider_id AND pm.user_id=$2::uuid AND pm.is_active WHERE a.room_id=$1::uuid LIMIT 1`, id, actor).Scan(&state)
	detail["application_state"] = state
	return c.JSON(detail)
}
func paidHallProvider(ctx context.Context, tx pgx.Tx, provider string) error {
	var paid bool
	err := tx.QueryRow(ctx, `SELECT verified AND paid_active AND badge_tier IN('bronze','silver','gold','diamond') AND EXISTS(SELECT 1 FROM provider_subscriptions sub WHERE sub.provider_id=$1::uuid AND sub.status='active' AND sub.current_period_end>clock_timestamp() AND (sub.starts_at IS NULL OR sub.starts_at<=clock_timestamp()) AND EXISTS(SELECT 1 FROM provider_subscription_payments pay WHERE pay.subscription_id=sub.id AND pay.amount>0 AND pay.currency_code='NGN')) FROM provider_public_identity WHERE provider_id=$1::uuid`, provider).Scan(&paid)
	if err != nil {
		return err
	}
	if !paid {
		return fiber.NewError(403, "Town Hall participation requires verification and an active paid plan")
	}
	return nil
}
func lockHallOwner(ctx context.Context, tx pgx.Tx, id, actor string) error {
	var owner, state string
	err := tx.QueryRow(ctx, `SELECT owner_user_id::text,CASE WHEN EXISTS(SELECT 1 FROM town_hall_requests h WHERE h.room_id=chat_rooms.id AND h.first_provider_engaged_at IS NULL AND h.engagement_deadline<=clock_timestamp()) THEN 'closed' ELSE state END FROM chat_rooms WHERE id=$1::uuid AND kind='town_hall' FOR UPDATE`, id).Scan(&owner, &state)
	if err != nil {
		return err
	}
	if owner != actor {
		return fiber.ErrForbidden
	}
	if state != "open" {
		return fiber.NewError(409, "This request is closed")
	}
	return nil
}
func (s *CommunityController) HallApply(c *fiber.Ctx) error {
	id, err := communityID(c, "room_id")
	if err != nil {
		return err
	}
	actor := communityActor(c)
	provider, _, err := s.market.providerForUser(c.Context(), actor)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	var state, owner string
	err = tx.QueryRow(c.Context(), `SELECT CASE WHEN EXISTS(SELECT 1 FROM town_hall_requests h WHERE h.room_id=chat_rooms.id AND h.first_provider_engaged_at IS NULL AND h.engagement_deadline<=clock_timestamp()) THEN 'closed' ELSE state END,owner_user_id::text FROM chat_rooms WHERE id=$1::uuid AND kind='town_hall' FOR UPDATE`, id).Scan(&state, &owner)
	if err != nil {
		return communityError(err)
	}
	if owner == actor {
		return fiber.NewError(422, "You own this request")
	}
	if state != "open" {
		return fiber.NewError(409, "This request is closed")
	}
	if err = paidHallProvider(c.Context(), tx, provider); err != nil {
		return communityError(err)
	}
	var seats int
	err = tx.QueryRow(c.Context(), `SELECT count(*) FROM chat_participants WHERE room_id=$1::uuid AND role='provider' AND state='active'`, id).Scan(&seats)
	if err != nil {
		return communityError(err)
	}
	if seats >= 10 {
		return fiber.NewError(409, "Town Hall is full")
	}
	_, err = tx.Exec(c.Context(), `INSERT INTO town_hall_applications(room_id,provider_id,requester_user_id) VALUES($1::uuid,$2::uuid,$3::uuid) ON CONFLICT(room_id,provider_id) DO UPDATE SET state=CASE WHEN town_hall_applications.state IN('declined','removed') THEN 'pending' ELSE town_hall_applications.state END,requester_user_id=CASE WHEN town_hall_applications.state IN('declined','removed') THEN EXCLUDED.requester_user_id ELSE town_hall_applications.requester_user_id END,updated_at=now()`, id, provider, actor)
	if err != nil {
		return communityError(err)
	}
	if err = insertNotification(c.Context(), tx, owner, "", nil, "town_hall_application", "Town Hall request", "A provider requested permission to join", map[string]any{"view": "messages", "town_hall_room_id": id}, "hall-apply:"+id+":"+provider); err != nil {
		return communityError(err)
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.SendStatus(204)
}
func (s *CommunityController) HallMembership(c *fiber.Ctx) error {
	id, err := communityID(c, "room_id")
	if err != nil {
		return err
	}
	provider, err := communityID(c, "provider_id")
	if err != nil {
		return err
	}
	var in struct {
		Action string `json:"action"`
	}
	if c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	if in.Action != "approve" && in.Action != "decline" && in.Action != "remove" {
		return fiber.ErrBadRequest
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	if err = lockHallOwner(c.Context(), tx, id, communityActor(c)); err != nil {
		return communityError(err)
	}
	var user, state string
	err = tx.QueryRow(c.Context(), `SELECT requester_user_id::text,state FROM town_hall_applications WHERE room_id=$1::uuid AND provider_id=$2::uuid FOR UPDATE`, id, provider).Scan(&user, &state)
	if err != nil {
		return communityError(err)
	}
	if in.Action == "approve" {
		if state != "pending" {
			return fiber.NewError(409, "Request is no longer pending")
		}
		if err = paidHallProvider(c.Context(), tx, provider); err != nil {
			return communityError(err)
		}
		var seats int
		err = tx.QueryRow(c.Context(), `SELECT count(*) FROM chat_participants WHERE room_id=$1::uuid AND role='provider' AND state='active'`, id).Scan(&seats)
		if err != nil {
			return communityError(err)
		}
		if seats >= 10 {
			return fiber.NewError(409, "Town Hall is full")
		}
		_, err = tx.Exec(c.Context(), `INSERT INTO chat_participants(room_id,user_id,provider_id,role) VALUES($1::uuid,$2::uuid,$3::uuid,'provider') ON CONFLICT(room_id,user_id) DO UPDATE SET provider_id=EXCLUDED.provider_id,role='provider',state='active',joined_at=now()`, id, user, provider)
		state = "admitted"
	} else {
		if in.Action == "decline" && state != "pending" {
			return fiber.NewError(409, "Request is no longer pending")
		}
		if in.Action == "remove" {
			state = "removed"
			_, err = tx.Exec(c.Context(), `UPDATE chat_participants SET state='removed' WHERE room_id=$1::uuid AND provider_id=$2::uuid`, id, provider)
		} else {
			state = "declined"
		}
	}
	if err != nil {
		return communityError(err)
	}
	_, err = tx.Exec(c.Context(), `UPDATE town_hall_applications SET state=$3,updated_at=now() WHERE room_id=$1::uuid AND provider_id=$2::uuid`, id, provider, state)
	if err != nil {
		return communityError(err)
	}
	_, err = tx.Exec(c.Context(), `UPDATE chat_rooms SET revision=revision+1 WHERE id=$1::uuid`, id)
	if err != nil {
		return communityError(err)
	}
	if err = insertNotification(c.Context(), tx, user, "", nil, "town_hall_membership", "Town Hall update", "Your request to join has been updated", map[string]any{"view": "messages", "town_hall_room_id": id}, "hall-member:"+id+":"+provider+":"+state+":"+time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return communityError(err)
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.SendStatus(204)
}
func (s *CommunityController) HallOffer(c *fiber.Ctx) error {
	id, err := communityID(c, "room_id")
	if err != nil {
		return err
	}
	var in struct {
		Price    float64 `json:"unit_price"`
		Terms    string  `json:"terms"`
		ClientID string  `json:"client_message_id"`
	}
	if c.BodyParser(&in) != nil || in.Price <= 0 || in.Price > 1e10 || len([]rune(in.Terms)) > 1000 {
		return fiber.NewError(422, "Enter a positive unit price and terms up to 1000 characters")
	}
	if _, err = uuid.Parse(in.ClientID); err != nil {
		return fiber.NewError(422, "An offer reference is required")
	}
	in.Price = math.Round(in.Price*100) / 100
	if in.Price <= 0 {
		return fiber.NewError(422, "Enter a positive price with up to two decimal places")
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	kind, role, provider, err := s.lockMember(c.Context(), tx, id, communityActor(c))
	if err != nil {
		return communityError(err)
	}
	if kind != "town_hall" || role != "provider" {
		return fiber.ErrForbidden
	}
	var unit string
	err = tx.QueryRow(c.Context(), `SELECT unit FROM town_hall_requests WHERE room_id=$1::uuid`, id).Scan(&unit)
	if err != nil {
		return communityError(err)
	}
	actor := communityActor(c)
	body := fmt.Sprintf("Offer: NGN %.2f per %s. %s", in.Price, unit, strings.TrimSpace(in.Terms))
	if unit == "" {
		body = fmt.Sprintf("Offer: NGN %.2f. %s", in.Price, strings.TrimSpace(in.Terms))
	}
	var previousRoom, previousBody string
	err = tx.QueryRow(c.Context(), `SELECT room_id::text,body FROM chat_messages WHERE sender_user_id=$1::uuid AND client_message_id=$2::uuid`, actor, in.ClientID).Scan(&previousRoom, &previousBody)
	if err == nil {
		if previousRoom != id || previousBody != body {
			return fiber.NewError(409, "Offer reference already used")
		}
		return c.SendStatus(204)
	}
	if err != pgx.ErrNoRows {
		return communityError(err)
	}
	_, err = tx.Exec(c.Context(), `INSERT INTO town_hall_offers(room_id,provider_id,sender_user_id,unit_price,terms) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5) ON CONFLICT(room_id,provider_id) DO UPDATE SET unit_price=EXCLUDED.unit_price,terms=EXCLUDED.terms,updated_at=now()`, id, provider, communityActor(c), in.Price, strings.TrimSpace(in.Terms))
	if err != nil {
		return communityError(err)
	}
	message, _, fresh, err := insertSharedMessage(c.Context(), tx, id, actor, conversationMessageInput{Body: body, MediaKeys: []string{}, ClientID: in.ClientID})
	if err != nil {
		return communityError(err)
	}
	if fresh {
		if err = s.notifyRoom(c.Context(), tx, id, actor, message, "New Town Hall offer", body); err != nil {
			return communityError(err)
		}
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.SendStatus(204)
}
func closeHallTx(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE chat_participants SET state='removed' WHERE room_id=$1::uuid AND role='provider'`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE town_hall_applications SET state='removed',updated_at=now() WHERE room_id=$1::uuid AND state IN('pending','admitted')`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE chat_rooms SET state='closed',closed_at=now(),revision=revision+1 WHERE id=$1::uuid`, id)
	return err
}
func (s *CommunityController) HallClose(c *fiber.Ctx) error {
	id, err := communityID(c, "room_id")
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	if err = lockHallOwner(c.Context(), tx, id, communityActor(c)); err != nil {
		return communityError(err)
	}
	if err = closeHallTx(c.Context(), tx, id); err != nil {
		return communityError(err)
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.SendStatus(204)
}
func (s *CommunityController) HallChoose(c *fiber.Ctx) error {
	id, err := communityID(c, "room_id")
	if err != nil {
		return err
	}
	var in struct {
		Offer string `json:"offer_id"`
		Close bool   `json:"close_room"`
	}
	if c.BodyParser(&in) != nil {
		return fiber.ErrBadRequest
	}
	if _, err = uuid.Parse(in.Offer); err != nil {
		return fiber.ErrBadRequest
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return communityError(err)
	}
	defer tx.Rollback(c.Context())
	actor := communityActor(c)
	var owner, state string
	var chosen []byte
	err = tx.QueryRow(c.Context(), `SELECT owner_user_id::text,state,context FROM chat_rooms WHERE id=$1::uuid AND kind='town_hall' FOR UPDATE`, id).Scan(&owner, &state, &chosen)
	if err != nil {
		return communityError(err)
	}
	if owner != actor {
		return fiber.ErrForbidden
	}
	if state == "closed" {
		var saved map[string]any
		_ = json.Unmarshal(chosen, &saved)
		if saved["selected_offer_id"] == in.Offer && saved["selected_dm_id"] != nil {
			return c.JSON(fiber.Map{"id": saved["selected_dm_id"]})
		}
		return fiber.NewError(409, "This request is closed")
	}
	if err = lockHallOwner(c.Context(), tx, id, actor); err != nil {
		return communityError(err)
	}
	var peer string
	var raw []byte
	err = tx.QueryRow(c.Context(), `SELECT p.user_id::text,jsonb_build_object('town_hall_request_id',r.id,'title',r.title,'offer_id',o.id,'unit_price',o.unit_price,'quantity',h.quantity,'unit',h.unit,'currency_code',h.currency_code,'terms',o.terms) FROM town_hall_offers o JOIN chat_rooms r ON r.id=o.room_id JOIN town_hall_requests h ON h.room_id=r.id JOIN chat_participants p ON p.room_id=r.id AND p.provider_id=o.provider_id AND p.state='active' WHERE o.id=$2::uuid AND r.id=$1::uuid`, id, in.Offer).Scan(&peer, &raw)
	if err != nil {
		return communityError(err)
	}
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
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	direct, err := ensureDirectTx(c.Context(), tx, actor, peer, data)
	if err != nil {
		return communityError(err)
	}
	summary := fmt.Sprintf("Selected Town Hall offer: %s. %v %v at NGN %v per unit. %v", data["title"], data["quantity"], data["unit"], data["unit_price"], data["terms"])
	if data["quantity"] == nil {
		summary = fmt.Sprintf("Selected Town Hall offer: %s. NGN %v. %v", data["title"], data["unit_price"], data["terms"])
	}
	if len([]rune(summary)) > 2000 {
		summary = string([]rune(summary)[:2000])
	}
	ref := uuid.NewSHA1(uuid.NameSpaceOID, []byte("hall-handoff:"+id+":"+in.Offer+":"+string(raw))).String()
	mid, _, fresh, err := insertSharedMessage(c.Context(), tx, direct, actor, conversationMessageInput{Body: summary, MediaKeys: []string{}, ClientID: ref})
	if err != nil {
		return communityError(err)
	}
	if fresh {
		if err = s.notifyRoom(c.Context(), tx, direct, actor, mid, "A buyer selected your offer", summary); err != nil {
			return communityError(err)
		}
	}
	_, err = tx.Exec(c.Context(), `UPDATE chat_rooms SET context=context||jsonb_build_object('selected_offer_id',$2::text,'selected_dm_id',$3::text) WHERE id=$1::uuid`, id, in.Offer, direct)
	if err != nil {
		return communityError(err)
	}
	if in.Close {
		if err = closeHallTx(c.Context(), tx, id); err != nil {
			return communityError(err)
		}
	}
	if err = tx.Commit(c.Context()); err != nil {
		return communityError(err)
	}
	return c.JSON(fiber.Map{"id": direct})
}
