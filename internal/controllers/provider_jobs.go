package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type jobPayload struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	City         string     `json:"city"`
	State        string     `json:"state"`
	Country      string     `json:"country_code"`
	WorkType     string     `json:"work_type"`
	Duration     string     `json:"work_duration"`
	Description  string     `json:"description"`
	Requirements string     `json:"requirements"`
	PayMin       *float64   `json:"pay_min"`
	PayMax       *float64   `json:"pay_max"`
	Currency     string     `json:"currency_code"`
	PayPeriod    string     `json:"pay_period"`
	Method       string     `json:"application_method"`
	URL          string     `json:"application_url"`
	Email        string     `json:"application_email"`
	Instructions string     `json:"application_instructions"`
	Deadline     *time.Time `json:"deadline"`
	Revision     int64      `json:"revision"`
	Status       string     `json:"status"`
}

var jobDomain = regexp.MustCompile(`^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,63}$`)

func jobChoice(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
func validJobURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.Port() != "" && u.Port() != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return jobDomain.MatchString(host) && net.ParseIP(host) == nil && !strings.HasSuffix(host, ".localhost") && !strings.HasSuffix(host, ".local") && !strings.HasSuffix(host, ".internal") && !strings.HasSuffix(host, ".test") && !strings.HasSuffix(host, ".invalid") && !strings.ContainsAny(raw, "\r\n\x00")
}
func validateJob(p *jobPayload, publish bool, now time.Time) error {
	p.Title = strings.TrimSpace(p.Title)
	p.City = strings.TrimSpace(p.City)
	p.State = strings.TrimSpace(p.State)
	p.URL = strings.TrimSpace(p.URL)
	p.Email = strings.TrimSpace(p.Email)
	if p.Country == "" {
		p.Country = "NG"
	}
	p.Country = strings.ToUpper(p.Country)
	if p.Currency == "" {
		p.Currency = "NGN"
	}
	p.Currency = strings.ToUpper(p.Currency)
	if p.Method == "" {
		p.Method = "chat"
	}
	if p.PayPeriod == "" {
		p.PayPeriod = "month"
	}
	for _, field := range []struct {
		value string
		max   int
	}{{p.Title, 160}, {p.City, 100}, {p.State, 100}, {p.Duration, 200}, {p.Description, 10000}, {p.Requirements, 6000}, {p.Instructions, 2000}, {p.URL, 2048}, {p.Email, 254}} {
		if utf8.RuneCountInString(field.value) > field.max || strings.ContainsRune(field.value, 0) {
			return fiber.NewError(422, "A job field exceeds its permitted length")
		}
	}
	if len(p.Country) != 2 || !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(p.Currency) {
		return fiber.NewError(422, "Choose a valid country and pay currency")
	}
	if p.WorkType != "" && !jobChoice(p.WorkType, "full_time", "part_time", "contract", "temporary", "internship", "one_day", "volunteer") {
		return fiber.NewError(422, "Choose a work type")
	}
	if !jobChoice(p.PayPeriod, "hour", "day", "week", "month", "year", "project") {
		return fiber.NewError(422, "Choose a pay period")
	}
	for _, amount := range []*float64{p.PayMin, p.PayMax} {
		if amount != nil && (math.IsNaN(*amount) || math.IsInf(*amount, 0) || *amount < 0 || *amount > 999999999999) {
			return fiber.NewError(422, "Enter a valid non-negative pay amount")
		}
	}
	if p.PayMax != nil && (p.PayMin == nil || *p.PayMax < *p.PayMin) {
		return fiber.NewError(422, "Maximum pay must be at least minimum pay")
	}
	switch p.Method {
	case "external":
		if p.URL != "" && !validJobURL(p.URL) {
			return fiber.NewError(422, "Use a public HTTPS application website without embedded credentials")
		}
		p.Email = ""
	case "email":
		if p.Email != "" {
			address, err := mail.ParseAddress(p.Email)
			if err != nil || address.Address != p.Email || strings.ContainsAny(p.Email, "\r\n") || !jobDomain.MatchString(strings.Split(p.Email, "@")[len(strings.Split(p.Email, "@"))-1]) {
				return fiber.NewError(422, "Enter a recruitment email address without a display name")
			}
		}
		p.URL = ""
	case "chat":
		p.URL = ""
		p.Email = ""
	default:
		return fiber.NewError(422, "Choose one application method")
	}
	if publish && (p.Title == "" || p.City == "" || p.WorkType == "" || strings.TrimSpace(p.Description) == "" || strings.TrimSpace(p.Requirements) == "" || p.Deadline == nil || !p.Deadline.After(now) || p.Method == "external" && p.URL == "" || p.Method == "email" && p.Email == "") {
		return fiber.NewError(422, "Publishing needs a title, location, work type, description, requirements, application destination and future deadline")
	}
	return nil
}
func jobError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fiber.ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code == "23514" {
			return fiber.NewError(409, pg.Message)
		}
		if pg.Code == "23505" {
			return fiber.NewError(409, "This operation already exists. Reload before retrying.")
		}
	}
	return fiber.ErrInternalServerError
}

const jobAvailable = `j.status='published' AND j.deadline>clock_timestamp() AND p.is_active AND p.verification_status='approved'`
const jobSelect = `SELECT to_jsonb(j)||jsonb_build_object('provider_name',p.business_name,'available',(` + jobAvailable + `),'effective_status',CASE WHEN j.status='published' AND j.deadline<=clock_timestamp() THEN 'expired' ELSE j.status END,'availability_reason',CASE WHEN j.status<>'published' THEN j.status WHEN j.deadline<=clock_timestamp() THEN 'expired' WHEN NOT p.is_active OR p.verification_status<>'approved' THEN 'provider_unavailable' ELSE '' END,'open_reports',(SELECT count(*) FROM provider_job_reports r WHERE r.job_id=j.id AND r.status='open')) FROM provider_jobs j JOIN provider_organizations p ON p.id=j.provider_id `

func (m *ProviderMarketplaceController) jobsOwner(c *fiber.Ctx) (string, error) {
	actor, _ := c.Locals("user_id").(string)
	id, _, err := m.providerForUser(c.Context(), actor)
	if err != nil {
		return "", fiber.ErrForbidden
	}
	return id, nil
}
func (m *ProviderMarketplaceController) ListJobs(c *fiber.Ctx) error { return m.listJobs(c, "public") }
func (m *ProviderMarketplaceController) ProviderJobs(c *fiber.Ctx) error {
	return m.listJobs(c, "provider")
}
func (m *ProviderMarketplaceController) AdminJobs(c *fiber.Ctx) error { return m.listJobs(c, "admin") }
func (m *ProviderMarketplaceController) listJobs(c *fiber.Ctx, scope string) error {
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	owner := ""
	if scope == "provider" {
		owner, err = m.jobsOwner(c)
		if err != nil {
			return err
		}
	}
	search, location, work, status, reports := strings.TrimSpace(c.Query("search")), strings.TrimSpace(c.Query("location")), c.Query("work_type"), c.Query("status"), c.Query("reports")
	if len(search) > 160 || len(location) > 160 {
		return fiber.ErrBadRequest
	}
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	rows, err := m.db.Query(ctx, jobSelect+`WHERE ($1='' OR j.provider_id=NULLIF($1,'')::uuid) AND ($2<>'public' OR (`+jobAvailable+`))
 AND ($3='' OR j.title||' '||j.description||' '||p.business_name ILIKE '%'||$3||'%') AND($4='' OR j.city||' '||j.state ILIKE '%'||$4||'%') AND($5='' OR j.work_type=$5)
 AND($6='' OR $6='history' AND (j.status IN ('closed','filled','removed') OR j.status='published' AND j.deadline<=clock_timestamp()) OR CASE WHEN j.status='published' AND j.deadline<=clock_timestamp() THEN 'expired' ELSE j.status END=$6)
 AND($7='' OR $7='all' OR $7='open' AND EXISTS(SELECT 1 FROM provider_job_reports r WHERE r.job_id=j.id AND r.status='open') OR $7='dismissed' AND EXISTS(SELECT 1 FROM provider_job_reports r WHERE r.job_id=j.id AND r.status='dismissed'))
 AND($8::timestamptz IS NULL OR(j.created_at,j.id)<($8,NULLIF($9,'')::uuid)) ORDER BY j.created_at DESC,j.id DESC LIMIT $10`, owner, scope, search, location, work, status, reports, page.CursorTime, page.CursorID, page.Limit+1)
	if err != nil {
		return jobError(err)
	}
	defer rows.Close()
	items := []json.RawMessage{}
	var last struct {
		ID      string    `json:"id"`
		Created time.Time `json:"created_at"`
	}
	next := ""
	for rows.Next() {
		var raw json.RawMessage
		if err = rows.Scan(&raw); err != nil {
			return jobError(err)
		}
		if len(items) == page.Limit {
			next = encodeAdminCursor(last.Created, last.ID)
			break
		}
		if err = json.Unmarshal(raw, &last); err != nil {
			return jobError(err)
		}
		if scope == "public" {
			var item map[string]any
			if json.Unmarshal(raw, &item) == nil {
				delete(item, "open_reports")
				delete(item, "plan_slot")
				raw, _ = json.Marshal(item)
			}
		}
		items = append(items, raw)
	}
	if rows.Err() != nil {
		return jobError(rows.Err())
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"items": items, "next_cursor": next, "has_more": next != ""})
}
func (m *ProviderMarketplaceController) Job(c *fiber.Ctx) error { return m.getJob(c, "public") }
func (m *ProviderMarketplaceController) ProviderJob(c *fiber.Ctx) error {
	return m.getJob(c, "provider")
}
func (m *ProviderMarketplaceController) AdminJob(c *fiber.Ctx) error { return m.getJob(c, "admin") }
func (m *ProviderMarketplaceController) getJob(c *fiber.Ctx, scope string) error {
	if _, err := uuid.Parse(c.Params("job_id")); err != nil {
		return fiber.ErrBadRequest
	}
	owner := ""
	var err error
	if scope == "provider" {
		owner, err = m.jobsOwner(c)
		if err != nil {
			return err
		}
	}
	var raw json.RawMessage
	err = m.db.QueryRow(c.Context(), jobSelect+`WHERE j.id=$1::uuid AND($2='' OR j.provider_id=NULLIF($2,'')::uuid) AND($3<>'public' OR j.status<>'draft' AND j.published_at IS NOT NULL)`, c.Params("job_id"), owner, scope).Scan(&raw)
	if err != nil {
		return jobError(err)
	}
	var item fiber.Map
	if err = json.Unmarshal(raw, &item); err != nil {
		return jobError(err)
	}
	if scope == "public" {
		delete(item, "open_reports")
		if item["available"] != true {
			item["application_url"] = ""
			item["application_email"] = ""
		}
	}
	if scope == "admin" {
		for _, kind := range []string{"reports", "history"} {
			table := "provider_job_reports"
			if kind == "history" {
				table = "provider_job_moderation"
			}
			var entries json.RawMessage
			err = m.db.QueryRow(c.Context(), `SELECT COALESCE(jsonb_agg(x ORDER BY x.created_at DESC),'[]'::jsonb) FROM(SELECT * FROM `+table+` WHERE job_id=$1::uuid ORDER BY created_at DESC LIMIT 100)x`, c.Params("job_id")).Scan(&entries)
			if err != nil {
				return jobError(err)
			}
			item[kind] = entries
		}
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(item)
}
func (m *ProviderMarketplaceController) JobAllowance(c *fiber.Ctx) error {
	owner, err := m.jobsOwner(c)
	if err != nil {
		return err
	}
	var limit, used int
	var verified bool
	err = m.db.QueryRow(c.Context(), `SELECT provider_active_job_limit(),p.is_active AND p.verification_status='approved',
 (SELECT count(*) FROM provider_jobs WHERE provider_id=p.id AND status='published' AND deadline>clock_timestamp()) FROM provider_organizations p WHERE p.id=$1::uuid`, owner).Scan(&limit, &verified, &used)
	if err != nil {
		return jobError(err)
	}
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"limit": limit, "used": used, "remaining": remaining, "verified": verified, "note": "Every verified provider has one free active job place, separate from product and service places. Drafts use no job place; closing, filling, expiry or removal releases it. Existing jobs stay in your history."})
}
func (m *ProviderMarketplaceController) SaveJob(c *fiber.Ctx) error {
	owner, err := m.jobsOwner(c)
	if err != nil {
		return err
	}
	var p jobPayload
	if c.BodyParser(&p) != nil {
		return fiber.ErrBadRequest
	}
	if err = validateJob(&p, false, time.Now()); err != nil {
		return err
	}
	create := c.Method() == "POST"
	id := c.Params("job_id")
	if create {
		id = p.ID
	}
	if _, err = uuid.Parse(id); err != nil {
		return fiber.NewError(422, "A stable job reference is required")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
	defer cancel()
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return jobError(err)
	}
	defer tx.Rollback(ctx)
	status := "draft"
	if create {
		if _, err = tx.Exec(ctx, `SELECT 1 FROM provider_organizations WHERE id=$1::uuid FOR UPDATE`, owner); err != nil {
			return jobError(err)
		}
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM provider_jobs WHERE provider_id=$1::uuid AND status='draft'`, owner).Scan(&count); err != nil {
			return jobError(err)
		}
		if count >= 100 {
			return fiber.NewError(409, "Remove unused drafts before creating another job")
		}
	} else {
		var revision int64
		err = tx.QueryRow(ctx, `SELECT status,revision FROM provider_jobs WHERE id=$1::uuid AND provider_id=$2::uuid FOR UPDATE`, id, owner).Scan(&status, &revision)
		if err != nil {
			return jobError(err)
		}
		if revision != p.Revision {
			return fiber.NewError(409, "This job changed. Reload before saving.")
		}
		if status == "removed" {
			return fiber.NewError(409, "A removed job cannot be edited or republished")
		}
		if err = validateJob(&p, status == "published", time.Now()); err != nil {
			return err
		}
	}
	if create {
		_, err = tx.Exec(ctx, `INSERT INTO provider_jobs(id,provider_id) VALUES($1,$2)`, id, owner)
		if err != nil {
			return jobError(err)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE provider_jobs SET title=$3,city=$4,state=$5,country_code=$6,work_type=$7,work_duration=$8,description=$9,requirements=$10,pay_min=$11,pay_max=$12,currency_code=$13,pay_period=$14,application_method=$15,application_url=$16,application_email=$17,application_instructions=$18,deadline=$19,revision=revision+1,updated_at=now() WHERE id=$1::uuid AND provider_id=$2::uuid`, id, owner, p.Title, p.City, p.State, p.Country, p.WorkType, p.Duration, p.Description, p.Requirements, p.PayMin, p.PayMax, p.Currency, p.PayPeriod, p.Method, p.URL, p.Email, p.Instructions, p.Deadline)
	if err != nil {
		return jobError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return jobError(err)
	}
	var raw json.RawMessage
	err = m.db.QueryRow(ctx, `SELECT to_jsonb(j) FROM provider_jobs j WHERE id=$1`, id).Scan(&raw)
	if err != nil {
		return jobError(err)
	}
	if create {
		c.Status(201)
	}
	return c.JSON(raw)
}
func (m *ProviderMarketplaceController) TransitionJob(c *fiber.Ctx) error {
	if _, err := uuid.Parse(c.Params("job_id")); err != nil {
		return fiber.ErrBadRequest
	}
	owner, err := m.jobsOwner(c)
	if err != nil {
		return err
	}
	var input struct {
		Status   string `json:"status"`
		Revision int64  `json:"revision"`
	}
	if c.BodyParser(&input) != nil || !jobChoice(input.Status, "published", "closed", "filled", "draft") {
		return fiber.ErrBadRequest
	}
	ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
	defer cancel()
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return jobError(err)
	}
	defer tx.Rollback(ctx)
	var raw json.RawMessage
	err = tx.QueryRow(ctx, `SELECT to_jsonb(j) FROM provider_jobs j WHERE id=$1::uuid AND provider_id=$2::uuid FOR UPDATE`, c.Params("job_id"), owner).Scan(&raw)
	if err != nil {
		return jobError(err)
	}
	var job jobPayload
	if json.Unmarshal(raw, &job) != nil {
		return fiber.ErrInternalServerError
	}
	if job.Revision != input.Revision {
		return fiber.NewError(409, "This job changed. Reload before changing status.")
	}
	if job.Status == "removed" {
		return fiber.NewError(409, "A removed job cannot be republished")
	}
	if input.Status == "published" {
		if err = validateJob(&job, true, time.Now()); err != nil {
			return err
		}
	}
	err = tx.QueryRow(ctx, `UPDATE provider_jobs SET status=$3,revision=revision+1,updated_at=now() WHERE id=$1::uuid AND provider_id=$2::uuid RETURNING to_jsonb(provider_jobs)`, c.Params("job_id"), owner, input.Status).Scan(&raw)
	if err != nil {
		return jobError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return jobError(err)
	}
	return c.JSON(raw)
}
func (m *ProviderMarketplaceController) ApplyJob(c *fiber.Ctx) error {
	if _, err := uuid.Parse(c.Params("job_id")); err != nil {
		return fiber.ErrBadRequest
	}
	actor, _ := c.Locals("user_id").(string)
	var input struct {
		ID       string `json:"id"`
		Message  string `json:"message"`
		Revision int64  `json:"revision"`
	}
	if c.BodyParser(&input) != nil {
		return fiber.ErrBadRequest
	}
	if _, err := uuid.Parse(input.ID); err != nil {
		return fiber.NewError(422, "A stable application reference is required")
	}
	if len(strings.TrimSpace(input.Message)) < 1 || utf8.RuneCountInString(input.Message) > 1500 {
		return fiber.NewError(422, "Enter an application message of up to 1500 characters")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
	defer cancel()
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return jobError(err)
	}
	defer tx.Rollback(ctx)
	var conversation string
	err = tx.QueryRow(ctx, `SELECT c.thread_id::text FROM provider_job_applications a JOIN provider_conversations c ON c.id=a.conversation_id WHERE a.job_id=$1::uuid AND a.user_id=$2::uuid`, c.Params("job_id"), actor).Scan(&conversation)
	if err == nil {
		return c.JSON(fiber.Map{"status": "already_received", "conversation_id": conversation})
	}
	if err != pgx.ErrNoRows {
		return jobError(err)
	}
	var raw json.RawMessage
	err = tx.QueryRow(ctx, jobSelect+`WHERE j.id=$1::uuid FOR SHARE OF j`, c.Params("job_id")).Scan(&raw)
	if err != nil {
		return jobError(err)
	}
	var job struct {
		jobPayload
		Provider  string `json:"provider_id"`
		Available bool   `json:"available"`
	}
	if json.Unmarshal(raw, &job) != nil {
		return fiber.ErrInternalServerError
	}
	if job.Revision != input.Revision {
		return fiber.NewError(409, "This job changed. Reload before applying.")
	}
	if !job.Available {
		return fiber.NewError(409, "Applications are unavailable for this job")
	}
	if job.Method != "chat" {
		return fiber.NewError(409, "Use this job's selected application method")
	}
	var own bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_members WHERE provider_id=$1::uuid AND user_id=$2::uuid AND is_active) OR EXISTS(SELECT 1 FROM provider_organizations WHERE id=$1::uuid AND owner_user_id=$2::uuid)`, job.Provider, actor).Scan(&own); err != nil {
		return jobError(err)
	}
	if own {
		return fiber.NewError(409, "You cannot apply to your own provider's job")
	}
	var physical, thread string
	err = tx.QueryRow(ctx, `INSERT INTO provider_conversations(job_id,provider_id,user_id) VALUES($1,$2,$3) ON CONFLICT(job_id,user_id) WHERE job_id IS NOT NULL DO UPDATE SET status='open',updated_at=now() WHERE provider_conversations.status<>'blocked' RETURNING id::text,thread_id::text`, c.Params("job_id"), job.Provider, actor).Scan(&physical, &thread)
	if err != nil {
		return jobError(err)
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT c.thread_id::text FROM provider_job_applications a JOIN provider_conversations c ON c.id=a.conversation_id WHERE a.job_id=$1::uuid AND a.user_id=$2::uuid`, c.Params("job_id"), actor).Scan(&existing)
	if err == nil {
		return c.JSON(fiber.Map{"status": "already_received", "conversation_id": existing})
	}
	if err != pgx.ErrNoRows {
		return jobError(err)
	}
	var blocked bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_conversations c WHERE c.id IN (`+businessThreadMembers("$1::uuid")+`) AND c.status='blocked')`, physical).Scan(&blocked); err != nil {
		return jobError(err)
	}
	if blocked {
		return fiber.NewError(409, "This conversation is blocked")
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_conversations SET status='open' WHERE id=$1::uuid AND status<>'blocked'`, thread); err != nil {
		return jobError(err)
	}
	if m.cfg.AsyncMessageNotifications {
		if _, err = tx.Exec(ctx, `SELECT set_config('app.async_message_fanout','on',true)`); err != nil {
			return jobError(err)
		}
	}
	message := conversationMessageInput{Body: "Job application: " + job.Title + "\n" + input.Message, ClientID: input.ID, MediaKeys: []string{}}
	messageID, _, fresh, err := insertConversationMessage(ctx, tx, physical, "buyer", actor, message)
	if err != nil {
		return jobError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO provider_job_applications(id,job_id,user_id,conversation_id,message_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(job_id,user_id) DO NOTHING`, input.ID, c.Params("job_id"), actor, physical, messageID); err != nil {
		return jobError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_conversations SET last_message_at=now(),updated_at=now() WHERE id=$1`, physical); err != nil {
		return jobError(err)
	}
	if fresh {
		if err = queueProviderActivityTx(ctx, tx, job.Provider, "", "conversation_message", map[string]any{"conversation_id": thread, "job_id": c.Params("job_id"), "listing_title": job.Title, "message": message.Body, "sender_user_id": actor}); err != nil {
			return jobError(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return jobError(err)
	}
	return c.Status(201).JSON(fiber.Map{"status": "received", "conversation_id": thread, "message_id": messageID})
}
func (m *ProviderMarketplaceController) JobClick(c *fiber.Ctx) error {
	if _, err := uuid.Parse(c.Params("job_id")); err != nil {
		return fiber.ErrBadRequest
	}
	actor, _ := c.Locals("user_id").(string)
	var input struct {
		ID       string `json:"id"`
		Method   string `json:"method"`
		Revision int64  `json:"revision"`
	}
	if c.BodyParser(&input) != nil {
		return fiber.ErrBadRequest
	}
	if _, err := uuid.Parse(input.ID); err != nil {
		return fiber.ErrBadRequest
	}
	var raw json.RawMessage
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return jobError(err)
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, jobSelect+`WHERE j.id=$1::uuid FOR SHARE OF j`, c.Params("job_id")).Scan(&raw)
	if err != nil {
		return jobError(err)
	}
	var job struct {
		jobPayload
		Available bool `json:"available"`
	}
	if json.Unmarshal(raw, &job) != nil {
		return fiber.ErrInternalServerError
	}
	if job.Revision != input.Revision {
		return fiber.NewError(409, "This job changed. Reload and check the destination again.")
	}
	if !job.Available || job.Method != input.Method || input.Method != "external" && input.Method != "email" {
		return fiber.NewError(409, "This application destination is unavailable")
	}
	changed, err := tx.Exec(ctx, `INSERT INTO provider_job_events(id,job_id,user_id,kind) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET id=provider_job_events.id WHERE provider_job_events.job_id=EXCLUDED.job_id AND provider_job_events.user_id=EXCLUDED.user_id AND provider_job_events.kind=EXCLUDED.kind`, input.ID, c.Params("job_id"), actor, input.Method+"_click")
	if err != nil {
		return jobError(err)
	}
	if changed.RowsAffected() != 1 {
		return fiber.NewError(409, "This click reference belongs to another operation")
	}
	if err = tx.Commit(ctx); err != nil {
		return jobError(err)
	}
	return c.JSON(fiber.Map{"status": "click_recorded", "application_received": false, "application_url": job.URL, "application_email": job.Email, "title": job.Title, "instructions": job.Instructions})
}
func (m *ProviderMarketplaceController) ReportJob(c *fiber.Ctx) error {
	if _, err := uuid.Parse(c.Params("job_id")); err != nil {
		return fiber.ErrBadRequest
	}
	actor, _ := c.Locals("user_id").(string)
	var input struct {
		Reason string `json:"reason"`
	}
	if c.BodyParser(&input) != nil || len(strings.TrimSpace(input.Reason)) < 3 || utf8.RuneCountInString(input.Reason) > 1000 {
		return fiber.NewError(422, "Describe the report in 3–1000 characters")
	}
	var id string
	err := m.db.QueryRow(c.Context(), `INSERT INTO provider_job_reports(job_id,user_id,reason) SELECT id,$2,$3 FROM provider_jobs WHERE id=$1::uuid AND status<>'draft' AND published_at IS NOT NULL ON CONFLICT(job_id,user_id) WHERE status='open' DO UPDATE SET reason=provider_job_reports.reason RETURNING id::text`, c.Params("job_id"), actor, input.Reason).Scan(&id)
	if err != nil {
		return jobError(err)
	}
	return c.Status(201).JSON(fiber.Map{"id": id, "status": "open"})
}
func (m *ProviderMarketplaceController) ModerateJob(c *fiber.Ctx) error {
	if _, err := uuid.Parse(c.Params("job_id")); err != nil {
		return fiber.ErrBadRequest
	}
	actor, _ := c.Locals("admin_id").(string)
	var input struct {
		Action   string `json:"action"`
		Reason   string `json:"reason"`
		Report   string `json:"report_id"`
		Revision int64  `json:"revision"`
	}
	if c.BodyParser(&input) != nil || len(strings.TrimSpace(input.Reason)) < 3 || utf8.RuneCountInString(input.Reason) > 1000 || input.Action != "remove" && input.Action != "dismiss_report" {
		return fiber.NewError(422, "Choose an action and give a moderation reason")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
	defer cancel()
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return jobError(err)
	}
	defer tx.Rollback(ctx)
	var revision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM provider_jobs WHERE id=$1::uuid FOR UPDATE`, c.Params("job_id")).Scan(&revision)
	if err != nil {
		return jobError(err)
	}
	if revision != input.Revision {
		return fiber.NewError(409, "The job changed. Reload before moderating.")
	}
	if input.Action == "remove" {
		_, err = tx.Exec(ctx, `UPDATE provider_jobs SET status='removed',revision=revision+1,updated_at=now() WHERE id=$1::uuid`, c.Params("job_id"))
	} else {
		if _, e := uuid.Parse(input.Report); e != nil {
			return fiber.ErrBadRequest
		}
		var changed pgconn.CommandTag
		changed, err = tx.Exec(ctx, `UPDATE provider_job_reports SET status='dismissed',reviewed_by=$3,review_notes=$4,reviewed_at=now() WHERE id=$1::uuid AND job_id=$2::uuid AND status='open'`, input.Report, c.Params("job_id"), actor, input.Reason)
		if err == nil && changed.RowsAffected() != 1 {
			return fiber.NewError(409, "This report was already reviewed")
		}
	}
	if err != nil {
		return jobError(err)
	}
	var report any
	if input.Action == "dismiss_report" {
		report = input.Report
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_job_moderation(job_id,admin_id,action,reason,report_id) VALUES($1,$2,$3,$4,$5)`, c.Params("job_id"), actor, input.Action, input.Reason, report)
	if err != nil {
		return jobError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return jobError(err)
	}
	return c.JSON(fiber.Map{"id": c.Params("job_id"), "action": input.Action, "confirmed": true})
}

// Recover a committed first application after a disconnected client, without replaying it.
func (m *ProviderMarketplaceController) JobApplication(c *fiber.Ctx) error {
	if _, err := uuid.Parse(c.Params("job_id")); err != nil {
		return fiber.ErrBadRequest
	}
	actor, _ := c.Locals("user_id").(string)
	var thread string
	err := m.db.QueryRow(c.Context(), `SELECT c.thread_id::text FROM provider_job_applications a JOIN provider_conversations c ON c.id=a.conversation_id WHERE a.job_id=$1::uuid AND a.user_id=$2::uuid`, c.Params("job_id"), actor).Scan(&thread)
	c.Set("Cache-Control", "private, no-store")
	if errors.Is(err, pgx.ErrNoRows) {
		return c.JSON(fiber.Map{"status": "not_received"})
	}
	if err != nil {
		return jobError(err)
	}
	return c.JSON(fiber.Map{"status": "received", "conversation_id": thread})
}
