package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const hallSearchVector = "to_tsvector('simple'::regconfig,coalesce(h.description,'')||' '||coalesce(h.category,'')||' '||coalesce(h.location,''))"

var hallSynonyms = map[string][]string{
	"bean": {"bean", "beans", "cowpea", "cowpeas"}, "beans": {"bean", "beans", "cowpea", "cowpeas"},
	"foodstuff": {"foodstuff", "foodstuffs", "grocery", "groceries", "provisions", "rice", "beans", "cowpea", "cowpeas"}, "groceries": {"groceries", "grocery", "foodstuff", "provisions", "rice", "beans", "cowpea", "cowpeas"}, "provisions": {"provisions", "groceries", "foodstuff", "rice", "beans", "cowpea", "cowpeas"},
	"courier": {"courier", "couriers", "dispatch", "rider", "riders", "delivery"}, "rider": {"rider", "riders", "courier", "couriers", "dispatch"}, "dispatch": {"dispatch", "courier", "rider", "delivery"},
	"driver": {"driver", "drivers", "chauffeur", "taxi", "ride", "pickup"}, "drivers": {"driver", "drivers", "chauffeur", "taxi", "ride"},
}

// Bounded, explicit vocabulary expansion. No inferred classification or ML grant.
func hallTextQuery(raw string, all bool) string { return hallTextQueryLimit(raw, all, 8) }
func hallTextQueryLimit(raw string, all bool, limit int) string {
	words := strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	seen := map[string]bool{}
	groups := []string{}
	for _, word := range words {
		if len(word) < 3 || len(word) > 40 || strings.Contains(" need looking want bags bag kg the and for with please service services ", " "+word+" ") || seen[word] {
			continue
		}
		numeric := true
		for _, r := range word {
			if !unicode.IsNumber(r) {
				numeric = false
				break
			}
		}
		if numeric {
			continue
		}
		seen[word] = true
		terms := hallSynonyms[word]
		if len(terms) == 0 {
			terms = []string{word}
			if strings.HasSuffix(word, "s") {
				terms = append(terms, strings.TrimSuffix(word, "s"))
			} else {
				terms = append(terms, word+"s")
			}
		}
		groups = append(groups, "("+strings.Join(terms, "|")+")")
		if len(groups) == limit {
			break
		}
	}
	join := "|"
	if all {
		join = "&"
	}
	return strings.Join(groups, join)
}
func hallHasDiscoveryFilters(c *fiber.Ctx) bool {
	for _, name := range []string{"q", "category", "request_kind", "location", "for_business", "since_hours", "sort"} {
		if c.Query(name) != "" {
			return true
		}
	}
	return false
}

type hallSearchCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
	Rank      int       `json:"rank"`
	Filter    string    `json:"filter"`
}

func (s *CommunityController) HallSearch(c *fiber.Ctx) error {
	c.Set("Cache-Control", "private, no-store")
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	actor := communityActor(c)
	q, category, location, kind := strings.TrimSpace(c.Query("q")), strings.TrimSpace(c.Query("category")), strings.TrimSpace(c.Query("location")), c.Query("request_kind")
	if len(q) > 120 || len(category) > 100 || len(location) > 100 {
		return fiber.NewError(422, "Shorten your search or area")
	}
	if kind != "" && kind != "product" && kind != "service" && kind != "transport" {
		return fiber.ErrBadRequest
	}
	sort := c.Query("sort", "relevance")
	if sort != "relevance" && sort != "latest" {
		return fiber.ErrBadRequest
	}
	hours := 0
	if c.Query("since_hours") != "" {
		hours, err = strconv.Atoi(c.Query("since_hours"))
		if err != nil || hours < 1 || hours > 720 {
			return fiber.NewError(422, "Choose a recency between 1 and 720 hours")
		}
	}
	businessQuery, city := "", ""
	if c.Query("for_business") == "true" {
		var provider string
		err = s.db.QueryRow(ctx, `SELECT p.id::text,p.city FROM provider_organizations p JOIN provider_members pm ON pm.provider_id=p.id AND pm.is_active WHERE pm.user_id=$1::uuid AND p.is_active LIMIT 1`, actor).Scan(&provider, &city)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return communityError(err)
			}
			return fiber.NewError(403, "For my business requires an active provider profile. You can still search all requests.")
		}
		rows, e := s.db.Query(ctx, `SELECT text FROM (
 (SELECT array_to_string(p.category_path,' ')||' '||p.title AS text FROM products p WHERE p.provider_id=$1::uuid AND p.moderation_status='approved' ORDER BY p.id LIMIT 64)
 UNION ALL (SELECT l.title||' '||l.category FROM provider_listings l WHERE l.provider_id=$1::uuid AND l.status='approved' ORDER BY l.id LIMIT 64)) topics`, provider)
		if e != nil {
			return communityError(e)
		}
		topics := []string{}
		for rows.Next() {
			var text string
			if e = rows.Scan(&text); e != nil {
				rows.Close()
				return communityError(e)
			}
			topics = append(topics, text)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return communityError(e)
		}
		businessQuery = hallTextQueryLimit(strings.Join(topics, " "), false, 32)
		if businessQuery == "" {
			return c.JSON(fiber.Map{"items": []any{}, "next_cursor": "", "can_create": true, "notice": "No approved business topics yet. Search manually or clear For my business."})
		}
	}
	query, catQuery, areaQuery := hallTextQuery(q, true), hallTextQuery(category, false), hallTextQuery(location, true)
	if (q != "" && query == "") || (category != "" && catQuery == "") || (location != "" && areaQuery == "") {
		return fiber.NewError(422, "Enter meaningful search words, category or area")
	}
	c.Set("Cache-Control", "private, no-store")
	filterBytes, _ := json.Marshal([]any{actor, query, catQuery, areaQuery, kind, businessQuery, city, hours, c.Query("mine"), sort})
	digest := sha256.Sum256(filterBytes)
	filter := hex.EncodeToString(digest[:])
	cursor := hallSearchCursor{}
	if raw := c.Query("cursor"); raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if e != nil || json.Unmarshal(b, &cursor) != nil || cursor.Filter != filter || uuid.Validate(cursor.ID) != nil || cursor.CreatedAt.IsZero() {
			return fiber.NewError(400, "Search changed. Refresh the request list.")
		}
	}
	relevance := `CASE WHEN $10 THEN 0 ELSE
 CASE WHEN $2='' THEN 0 ELSE round(ts_rank_cd(` + hallSearchVector + `,to_tsquery('simple',$2))*100000)::int END+
 CASE WHEN $6='' THEN 0 ELSE round(ts_rank_cd(` + hallSearchVector + `,to_tsquery('simple',$6))*10000)::int END+
 CASE WHEN $7<>'' AND lower(h.location) LIKE '%'||lower($7)||'%' THEN 20 ELSE 0 END END`
	// Optional predicates are server-selected literals, so prepared statements
	// can use the GIN/partial indexes instead of a generic "empty OR match" scan.
	where := `r.kind='town_hall' AND r.state='open' AND h.engagement_deadline>clock_timestamp()`
	if c.Query("mine") == "true" {
		where += ` AND r.owner_user_id=$1::uuid`
	}
	if query != "" {
		where += ` AND ` + hallSearchVector + `@@to_tsquery('simple',$2)`
	}
	if category != "" {
		where += ` AND (to_tsvector('simple'::regconfig,coalesce(h.category,''))@@to_tsquery('simple',$4) OR (h.category='' AND ` + hallSearchVector + `@@to_tsquery('simple',$4)))`
	}
	if kind != "" {
		where += ` AND r.context->>'request_kind'=$5`
	}
	if businessQuery != "" {
		where += ` AND ` + hallSearchVector + `@@to_tsquery('simple',$6)`
	}
	if hours > 0 {
		where += ` AND r.created_at>clock_timestamp()-($9::text||' hours')::interval`
	}
	if areaQuery != "" {
		where += ` AND to_tsvector('simple'::regconfig,coalesce(h.location,''))@@to_tsquery('simple',$11)`
	}
	preview := strings.Replace(hallPreviewSQL, "'id',r.id,", "'id',r.id,'discovery_rank',x.relevance,", 1)
	sql := `WITH request_parameters AS (SELECT $1::uuid,$2::text,$3::text,$4::text,$5::text,$6::text,$7::text,$8::boolean,$9::integer,$10::boolean,$11::text,$12::timestamptz,$13::integer,$14::uuid,$15::integer),ranked AS MATERIALIZED(SELECT r.id,r.created_at,` + relevance + ` AS relevance FROM chat_rooms r JOIN town_hall_requests h ON h.room_id=r.id WHERE ` + where + `) ` + preview + ` JOIN ranked x ON x.id=r.id WHERE ($12::timestamptz IS NULL OR (x.relevance,r.created_at,r.id)<($13,$12,$14::uuid)) ORDER BY x.relevance DESC,r.created_at DESC,r.id DESC LIMIT $15`
	rows, err := s.db.Query(ctx, sql, actor, query, category, catQuery, kind, businessQuery, city, c.Query("mine") == "true", hours, sort == "latest", areaQuery, page.CursorTime, cursor.Rank, nullID(page.CursorID), page.Limit+1)
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
				ID   string    `json:"id"`
				At   time.Time `json:"created_at"`
				Rank int       `json:"discovery_rank"`
			}
			_ = json.Unmarshal(items[len(items)-1], &last)
			b, _ := json.Marshal(hallSearchCursor{last.At, last.ID, last.Rank, filter})
			next = base64.RawURLEncoding.EncodeToString(b)
			break
		}
		items = append(items, json.RawMessage(raw))
	}
	if err = rows.Err(); err != nil {
		return communityError(err)
	}
	rows.Close()
	_, _ = s.db.Exec(ctx, `UPDATE social_profiles SET town_hall_seen_at=now() WHERE user_id=$1::uuid`, actor)
	payload, _ := json.Marshal(fiber.Map{"items": items, "next_cursor": next, "can_create": true})
	etag := fmt.Sprintf(`"hall-search-%x"`, sha256.Sum256(payload))
	c.Set("ETag", etag)
	c.Set("Cache-Control", "private, no-store")
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(304)
	}
	c.Type("json")
	return c.Send(payload)
}
