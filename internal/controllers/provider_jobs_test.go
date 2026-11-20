package controllers

import (
	"across/backend/internal/auth"
	"across/backend/internal/config"
	appdb "across/backend/internal/db"
	"across/backend/internal/middleware"
	"across/backend/internal/storage"
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Synthetic, randomly isolated schema. No workers, mailers, storage or gateway clients run.
func jobsDB117(t *testing.T, before ...func(*pgxpool.Pool)) *pgxpool.Pool {
	t.Helper()
	db := settlementTestDB(t)
	if appdb.VerifyJobsSchema(context.Background(), db) == nil {
		t.Fatal("missing Jobs schema allowed startup")
	}
	safetyExec(t, db, `CREATE TABLE users(id uuid PRIMARY KEY,full_name text DEFAULT 'Candidate',email text DEFAULT 'candidate@example.invalid',avatar_url text DEFAULT '',is_active bool DEFAULT true);
 CREATE TABLE chat_messages(id uuid PRIMARY KEY);
 CREATE TABLE admins(id uuid PRIMARY KEY,role text,is_active bool DEFAULT true);
 CREATE TABLE provider_organizations(id uuid PRIMARY KEY,owner_user_id uuid,business_name text DEFAULT 'Synthetic employer',is_active bool DEFAULT true,verification_status text DEFAULT 'approved',tier text DEFAULT 'free');
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,is_active bool DEFAULT true,role text DEFAULT 'owner');
 CREATE TABLE provider_listings(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),provider_id uuid,title text DEFAULT 'Service',status text DEFAULT 'draft',plan_slot int,published_at timestamptz,media_urls text[] DEFAULT '{}',attributes jsonb DEFAULT '{}');
 CREATE TABLE products(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),provider_id uuid,title text DEFAULT 'Product',moderation_status text DEFAULT 'draft',plan_slot int,published_at timestamptz,image_urls text[] DEFAULT '{}',factory_details jsonb DEFAULT '{}');
 CREATE TABLE provider_subscriptions(provider_id uuid,status text,current_period_end timestamptz);
 CREATE VIEW provider_public_identity AS SELECT id AS provider_id,tier AS badge_tier FROM provider_organizations;
 CREATE TABLE provider_post_video_assets(provider_id uuid,url text,duration_seconds int);
 CREATE TABLE provider_marketplace_events(id uuid DEFAULT gen_random_uuid(),provider_id uuid,listing_id uuid,event_type text,metadata jsonb);`)
	raw, e := os.ReadFile("../../migrations/107_provider_tier_entitlements.sql")
	if e != nil {
		t.Fatal(e)
	}
	// The exact existing tier function; no paid subscriptions are needed by this fixture.
	safetyExec(t, db, strings.Split(string(raw), "UPDATE provider_subscription_plans")[0])
	safetyExec(t, db, `CREATE FUNCTION provider_current_benefits(provider uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$SELECT provider_tier_benefits(tier) FROM provider_organizations WHERE id=provider$$;`)
	for _, name := range []string{"041_provider_conversations.sql", "064_product_payment_options_and_chat_images.sql", "074_chat_history_revision.sql", "088_chat_location_pins.sql", "099_provider_conversation_threads.sql", "117_provider_jobs.sql", "123_job_photos.sql"} {
		if name == "117_provider_jobs.sql" {
			for _, seed := range before {
				seed(db)
			}
		}
		raw, e := os.ReadFile("../../migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		safetyExec(t, db, string(raw))
	}
	safetyExec(t, db, `CREATE TRIGGER capacity BEFORE INSERT OR UPDATE ON products FOR EACH ROW EXECUTE FUNCTION enforce_provider_listing_capacity();
 CREATE TRIGGER slot BEFORE INSERT OR UPDATE ON products FOR EACH ROW EXECUTE FUNCTION assign_provider_post_slot();
 CREATE TRIGGER capacity BEFORE INSERT OR UPDATE ON provider_listings FOR EACH ROW EXECUTE FUNCTION enforce_provider_listing_capacity();
 CREATE TRIGGER slot BEFORE INSERT OR UPDATE ON provider_listings FOR EACH ROW EXECUTE FUNCTION assign_provider_post_slot();`)
	if e := appdb.VerifyJobsSchema(context.Background(), db); e == nil {
		t.Fatal("old shared Jobs schema allowed new API startup")
	}
	return db
}
func jobsDB(t *testing.T, before ...func(*pgxpool.Pool)) *pgxpool.Pool {
	t.Helper()
	db := jobsDB117(t, before...)
	jobsIndependentMigration(t, db)
	return db
}
func jobsIndependentMigration(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	for _, name := range []string{"120_independent_provider_job_allowance.sql", "121_provider_job_tier_limits.sql"} {
		raw, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		safetyExec(t, db, string(raw))
	}

	if err := appdb.VerifyJobsSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}
func jobsApp(db *pgxpool.Pool) *fiber.App {
	m := &ProviderMarketplaceController{db: db, s3: storage.NewS3(config.Config{AssetsCDNBase: "https://media.example.test"})}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Get("/jobs", m.ListJobs)
	app.Get("/jobs/:job_id", m.Job)
	app.Get("/owner/allowance", m.JobAllowance)
	app.Get("/owner", m.ProviderJobs)
	app.Post("/owner", m.SaveJob)
	app.Put("/owner/:job_id", m.SaveJob)
	app.Get("/owner/:job_id", m.ProviderJob)
	app.Post("/owner/:job_id/status", m.TransitionJob)
	app.Post("/jobs/:job_id/clicks", m.JobClick)
	app.Post("/jobs/:job_id/applications", m.ApplyJob)
	app.Get("/jobs/:job_id/application", m.JobApplication)
	app.Post("/jobs/:job_id/reports", m.ReportJob)
	// Exercise real admin authentication/role middleware, not an actor header shortcut.
	group := app.Group("/admin", middleware.RequireAdminRoles(config.Config{JWTSecret: "jobs-test-only"}, db, "super_admin", "catalog_admin", "moderation_admin"))
	group.Get("/jobs", m.AdminJobs)
	group.Get("/jobs/:job_id", m.AdminJob)
	group.Post("/jobs/:job_id/moderation", m.ModerateJob)
	return app
}
func jobsActor(t *testing.T, db *pgxpool.Pool, tier string) (string, string) {
	t.Helper()
	owner, p := uuid.NewString(), uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id)VALUES($1)`, owner)
	safetyExec(t, db, `INSERT INTO provider_organizations(id,owner_user_id,tier)VALUES($1,$2,$3)`, p, owner, tier)
	safetyExec(t, db, `INSERT INTO provider_members(provider_id,user_id)VALUES($1,$2)`, p, owner)
	return owner, p
}
func jobsPayload(id string, revision int64, method string) map[string]any {
	return map[string]any{"id": id, "revision": revision, "title": "Dispatch rider", "city": "Abuja", "work_type": "one_day", "work_duration": "One day", "description": "Deliver parcels safely.", "requirements": "Valid driving licence.", "deadline": time.Now().Add(14 * 24 * time.Hour).UTC(), "application_method": method, "application_url": "https://forms.gle/synthetic-only", "application_email": "careers@example.invalid"}
}
func jobsCall(t *testing.T, app *fiber.App, method, path, actor string, body any, want int) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(actor, "Bearer ") {
		req.Header.Set("Authorization", actor)
	} else {
		req.Header.Set("Actor", actor)
	}
	res, e := app.Test(req, 15000)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	data, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s got%d want%d: %s", method, path, res.StatusCode, want, data)
	}
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}
func jobsCreate(t *testing.T, app *fiber.App, actor, method string) map[string]any {
	t.Helper()
	return jobsCall(t, app, "POST", "/owner", actor, jobsPayload(uuid.NewString(), 0, method), 201)
}
func jobsStatus(t *testing.T, app *fiber.App, actor string, job map[string]any, status string, want int) map[string]any {
	t.Helper()
	return jobsCall(t, app, "POST", "/owner/"+job["id"].(string)+"/status", actor, map[string]any{"status": status, "revision": job["revision"]}, want)
}
func TestJobValidation(t *testing.T) {
	for _, raw := range []string{"http://example.com/apply", "https://user:pass@example.com", "https://127.0.0.1/x", "https://example.local", "https://example.com:444/x", "javascript:alert(1)", "https://example.com\n"} {
		if validJobURL(raw) {
			t.Fatal("unsafe URL accepted", raw)
		}
	}
	for _, raw := range []string{"https://forms.gle/abc", "https://careers.example.com/jobs?a=1#apply"} {
		if !validJobURL(raw) {
			t.Fatal("valid public URL rejected", raw)
		}
	}
	var p jobPayload
	raw, _ := json.Marshal(jobsPayload(uuid.NewString(), 0, "chat"))
	_ = json.Unmarshal(raw, &p)
	if e := validateJob(&p, true, time.Now()); e != nil {
		t.Fatal(e)
	}
	if p.Duration != "One day" || p.Deadline.Sub(time.Now()) < 13*24*time.Hour {
		t.Fatal("work duration conflated with application deadline")
	}
	p.Method = "email"
	p.Email = "Recruiter <careers@example.com>"
	if validateJob(&p, false, time.Now()) == nil {
		t.Fatal("display-name email accepted")
	}
	p.Email = "careers@example.com"
	if validateJob(&p, true, time.Now()) != nil {
		t.Fatal("email rejected")
	}
	p.Deadline = nil
	if validateJob(&p, true, time.Now()) == nil {
		t.Fatal("missing publish deadline accepted")
	}
	if validateJob(&p, false, time.Now()) != nil {
		t.Fatal("incomplete draft rejected")
	}
}
func TestJobIndependentAllowanceExpiryAndHistory(t *testing.T) {
	db := jobsDB(t)
	app := jobsApp(db)
	owner, p := jobsActor(t, db, "free")
	ctx := context.Background()
	first, second := jobsCreate(t, app, owner, "chat"), jobsCreate(t, app, owner, "email")
	if jobsCall(t, app, "GET", "/owner/allowance", owner, nil, 200)["remaining"] != float64(1) {
		t.Fatal("draft used slot")
	}
	// A Free provider can keep their product place and publish one free job.
	safetyExec(t, db, `INSERT INTO products(provider_id)VALUES($1)`, p)
	first = jobsStatus(t, app, owner, first, "published", 200)
	jobsStatus(t, app, owner, second, "published", 409)
	if _, e := db.Exec(ctx, `INSERT INTO provider_listings(provider_id)VALUES($1)`, p); e == nil {
		t.Fatal("service draft bypassed the existing product allowance")
	}
	// No background cleanup, status changes or slot clearing after deadline passage.
	safetyExec(t, db, `UPDATE provider_jobs SET deadline=clock_timestamp()+interval '150 milliseconds' WHERE id=$1`, first["id"])
	time.Sleep(200 * time.Millisecond)
	var status string
	var slot int
	if e := db.QueryRow(ctx, `SELECT status,plan_slot FROM provider_jobs WHERE id=$1`, first["id"]).Scan(&status, &slot); e != nil || status != "published" || slot != 1 {
		t.Fatal("expiry fixture invalid", e, status, slot)
	}
	if jobsCall(t, app, "GET", "/owner/allowance", owner, nil, 200)["remaining"] != float64(1) {
		t.Fatal("expiry did not immediately release slot")
	}
	if len(jobsCall(t, app, "GET", "/jobs", owner, nil, 200)["items"].([]any)) != 0 {
		t.Fatal("expired job still public")
	}
	second = jobsStatus(t, app, owner, second, "published", 200)
	if len(jobsCall(t, app, "GET", "/owner?status=history", owner, nil, 200)["items"].([]any)) != 1 {
		t.Fatal("expired job missing from history")
	}
	for _, state := range []string{"closed", "filled", "draft"} {
		second = jobsStatus(t, app, owner, second, state, 200)
		if jobsCall(t, app, "GET", "/owner/allowance", owner, nil, 200)["remaining"] != float64(1) {
			t.Fatal("state did not free slot", state)
		}
		second = jobsStatus(t, app, owner, second, "published", 200)
	}
	// Old links explain expiry and cannot receive applications/clicks.
	expired := jobsCall(t, app, "GET", "/jobs/"+first["id"].(string), owner, nil, 200)
	if expired["effective_status"] != "expired" || expired["available"] != false {
		t.Fatal("old link incorrect")
	}
	outsider, _ := jobsActor(t, db, "free")
	jobsCall(t, app, "GET", "/owner/"+first["id"].(string), outsider, nil, 404)
	jobsStatus(t, app, outsider, second, "closed", 404)
}
func TestJobSimultaneousPublishAndLegacyAllocation(t *testing.T) {
	db := jobsDB(t)
	owner, p := jobsActor(t, db, "free")
	app := jobsApp(db)
	a, b := jobsCreate(t, app, owner, "chat"), jobsCreate(t, app, owner, "chat")
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, job := range []map[string]any{a, b} {
		wg.Add(1)
		go func(job map[string]any) {
			defer wg.Done()
			<-start
			raw, _ := json.Marshal(map[string]any{"status": "published", "revision": job["revision"]})
			req := httptest.NewRequest("POST", "/owner/"+job["id"].(string)+"/status", strings.NewReader(string(raw)))
			req.Header.Set("Actor", owner)
			req.Header.Set("Content-Type", "application/json")
			res, e := app.Test(req, 15000)
			if e != nil {
				results <- 0
				return
			}
			res.Body.Close()
			results <- res.StatusCode
		}(job)
	}
	close(start)
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for code := range results {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal("simultaneous publish exceeded allowance", counts)
	}
	var used int
	if e := db.QueryRow(context.Background(), `SELECT count(*) FROM provider_jobs WHERE provider_id=$1 AND status='published'`, p).Scan(&used); e != nil || used != 1 {
		t.Fatal("wrong active job count", used, e)
	}
	// A product and a job can publish concurrently: their allowances are independent.
	safetyExec(t, db, `UPDATE provider_jobs SET status='closed' WHERE provider_id=$1`, p)
	start = make(chan struct{})
	errs := make(chan error, 2)
	for _, q := range []string{`INSERT INTO products(provider_id)VALUES($1)`, `UPDATE provider_jobs SET status='published' WHERE id=$1`} {
		wg.Add(1)
		go func(q string) {
			defer wg.Done()
			<-start
			var e error
			if strings.HasPrefix(q, "UPDATE") {
				_, e = db.Exec(context.Background(), q, a["id"])
			} else {
				_, e = db.Exec(context.Background(), q, p)
			}
			errs <- e
		}(q)
	}
	close(start)
	wg.Wait()
	close(errs)
	ok := 0
	for e := range errs {
		if e == nil {
			ok++
		}
	}
	if ok != 2 {
		t.Fatal("independent product/job allocations interfered", ok)
	}
}
func TestJobApplicationsClicksOwnershipAndModeration(t *testing.T) {
	db := jobsDB(t)
	app := jobsApp(db)
	owner, p := jobsActor(t, db, "bronze")
	buyer := uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id)VALUES($1)`, buyer)
	for _, method := range []string{"external", "email", "chat"} {
		job := jobsStatus(t, app, owner, jobsCreate(t, app, owner, method), "published", 200)
		id := job["id"].(string)
		rev := job["revision"]
		if method != "chat" {
			event := map[string]any{"id": uuid.NewString(), "method": method, "revision": rev}
			out := jobsCall(t, app, "POST", "/jobs/"+id+"/clicks", buyer, event, 200)
			if out["application_received"] != false || out["status"] != "click_recorded" {
				t.Fatal("click falsely treated as application")
			}
			jobsCall(t, app, "POST", "/jobs/"+id+"/clicks", buyer, event, 200)
			event["revision"] = 0
			jobsCall(t, app, "POST", "/jobs/"+id+"/clicks", buyer, event, 409)
			jobsCall(t, app, "POST", "/jobs/"+id+"/applications", buyer, map[string]any{"id": uuid.NewString(), "message": "Apply", "revision": rev}, 409)
		} else {
			body := map[string]any{"id": uuid.NewString(), "message": "I would like to apply.", "revision": rev}
			jobsCall(t, app, "POST", "/jobs/"+id+"/applications", owner, body, 409)
			out := jobsCall(t, app, "POST", "/jobs/"+id+"/applications", buyer, body, 201)
			if out["status"] != "received" || out["conversation_id"] == nil {
				t.Fatal("missing committed chat receipt")
			}
			jobsCall(t, app, "POST", "/jobs/"+id+"/applications", buyer, body, 200)
			body["id"] = uuid.NewString()
			jobsCall(t, app, "POST", "/jobs/"+id+"/applications", buyer, body, 200)
			recovered := jobsCall(t, app, "GET", "/jobs/"+id+"/application", buyer, nil, 200)
			if recovered["conversation_id"] != out["conversation_id"] {
				t.Fatal("lost response recovery failed")
			}
			var count int
			var contextID string
			if e := db.QueryRow(context.Background(), `SELECT count(*),min(context->>'job_id') FROM provider_conversation_messages`).Scan(&count, &contextID); e != nil || count != 1 || contextID != id {
				t.Fatal("duplicate or missing job context", count, contextID, e)
			}
		}
		report := jobsCall(t, app, "POST", "/jobs/"+id+"/reports", buyer, map[string]any{"reason": "Suspicious recruitment request"}, 201)
		for _, role := range []string{"support_admin", "finance_admin", "ads_admin", "moderation_admin", "catalog_admin", "super_admin"} {
			admin := uuid.NewString()
			safetyExec(t, db, `INSERT INTO admins(id,role)VALUES($1,$2)`, admin, role)
			token, _, e := auth.Sign(admin, "jobs-test-only", time.Hour)
			if e != nil {
				t.Fatal(e)
			}
			bearer := "Bearer " + token
			want := 403
			if role == "moderation_admin" || role == "catalog_admin" || role == "super_admin" {
				want = 200
			}
			jobsCall(t, app, "GET", "/admin/jobs", bearer, nil, want)
			if want == 403 {
				jobsCall(t, app, "POST", "/admin/jobs/"+id+"/moderation", bearer, map[string]any{"action": "remove", "reason": "Reason", "revision": rev}, 403)
				continue
			}
			if role != "moderation_admin" {
				continue
			}
			jobsCall(t, app, "POST", "/admin/jobs/"+id+"/moderation", bearer, map[string]any{"action": "dismiss_report", "report_id": report["id"], "reason": "Reviewed evidence", "revision": rev}, 200)
			detail := jobsCall(t, app, "GET", "/admin/jobs/"+id, bearer, nil, 200)
			if detail["status"] != "published" || len(detail["history"].([]any)) != 1 {
				t.Fatal("report dismissal changed listing or audit missing")
			}
			jobsCall(t, app, "POST", "/admin/jobs/"+id+"/moderation", bearer, map[string]any{"action": "remove", "reason": "Unsafe recruitment listing", "revision": rev}, 200)
			jobsStatus(t, app, owner, job, "published", 409)
			old := jobsCall(t, app, "GET", "/jobs/"+id, buyer, nil, 200)
			if old["available"] != false || old["effective_status"] != "removed" {
				t.Fatal("moderated old link active")
			}
		}
	}
	var events, applications int
	e := db.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM provider_job_events),(SELECT count(*) FROM provider_job_applications)`).Scan(&events, &applications)
	if e != nil || events != 2 || applications != 1 {
		t.Fatal("wrong accounting", events, applications, e)
	}
	if jobsCall(t, app, "GET", "/owner/allowance", owner, nil, 200)["remaining"] != float64(1) {
		t.Fatal("removal did not free slots", p)
	}
	draft := jobsCreate(t, app, owner, "chat")
	jobsCall(t, app, "GET", "/jobs/"+draft["id"].(string), buyer, nil, 404)
	safetyExec(t, db, `UPDATE provider_organizations SET verification_status='pending' WHERE id=$1`, p)
	jobsStatus(t, app, owner, draft, "published", 409)

}

func TestJobMigrationPreservesExistingConversationAndLegacyBenefits(t *testing.T) {
	var owner, provider, listing, buyer, thread, message string
	db := jobsDB(t, func(db *pgxpool.Pool) {
		owner, provider = jobsActor(t, db, "free")
		buyer = uuid.NewString()
		listing = uuid.NewString()
		thread = uuid.NewString()
		message = uuid.NewString()
		safetyExec(t, db, `INSERT INTO users(id)VALUES($1)`, buyer)
		safetyExec(t, db, `INSERT INTO provider_listings(id,provider_id,title,status,plan_slot)VALUES($1,$2,'Existing service','approved',1)`, listing, provider)
		safetyExec(t, db, `INSERT INTO provider_conversations(id,provider_id,user_id,listing_id)VALUES($1,$2,$3,$4)`, thread, provider, buyer, listing)
		safetyExec(t, db, `INSERT INTO provider_conversation_messages(id,conversation_id,sender_type,sender_user_id,body)VALUES($1,$2,'buyer',$3,'Original message')`, message, thread, buyer)
	})
	var text, contextTitle string
	if e := db.QueryRow(context.Background(), `SELECT body,context->>'title' FROM provider_conversation_messages WHERE id=$1`, message).Scan(&text, &contextTitle); e != nil || text != "Original message" || contextTitle != "Existing service" {
		t.Fatal("migration changed existing history", text, contextTitle, e)
	}
	var limit int
	if e := db.QueryRow(context.Background(), `SELECT (provider_current_benefits($1)->>'post_limit')::int`, provider).Scan(&limit); e != nil || limit != 1 {
		t.Fatal("legacy plan changed", limit, e)
	}
	app := jobsApp(db)
	job := jobsCreate(t, app, owner, "chat")
	jobsStatus(t, app, owner, job, "published", 200)
	job = jobsCreate(t, app, owner, "chat")
	// Never-published drafts remain private even when removed by an administrator.
	safetyExec(t, db, `UPDATE provider_jobs SET status='removed' WHERE id=$1`, job["id"])
	jobsCall(t, app, "GET", "/jobs/"+job["id"].(string), buyer, nil, 404)
}

func TestJobExpiredAndChangedDestinationsRejectNewApplications(t *testing.T) {
	db := jobsDB(t)
	app := jobsApp(db)
	owner, _ := jobsActor(t, db, "free")
	buyer := uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id)VALUES($1)`, buyer)
	job := jobsStatus(t, app, owner, jobsCreate(t, app, owner, "external"), "published", 200)
	id := job["id"].(string)
	payload := jobsPayload(id, int64(job["revision"].(float64)), "external")
	payload["application_url"] = "https://careers.example.com/new"
	updated := jobsCall(t, app, "PUT", "/owner/"+id, owner, payload, 200)
	jobsCall(t, app, "POST", "/jobs/"+id+"/clicks", buyer, map[string]any{"id": uuid.NewString(), "revision": job["revision"], "method": "external"}, 409)
	jobsCall(t, app, "POST", "/jobs/"+id+"/clicks", buyer, map[string]any{"id": uuid.NewString(), "revision": updated["revision"], "method": "external"}, 200)
	safetyExec(t, db, `UPDATE provider_jobs SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, id)
	jobsCall(t, app, "POST", "/jobs/"+id+"/clicks", buyer, map[string]any{"id": uuid.NewString(), "revision": updated["revision"], "method": "external"}, 409)
	jobsCall(t, app, "POST", "/jobs/"+id+"/applications", buyer, map[string]any{"id": uuid.NewString(), "revision": updated["revision"], "message": "Apply"}, 409)
}

func TestJobConcurrentApplicationsProduceOneMessage(t *testing.T) {
	db := jobsDB(t)
	app := jobsApp(db)
	owner, _ := jobsActor(t, db, "free")
	buyer := uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id)VALUES($1)`, buyer)
	job := jobsStatus(t, app, owner, jobsCreate(t, app, owner, "chat"), "published", 200)
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			raw, _ := json.Marshal(map[string]any{"id": uuid.NewString(), "revision": job["revision"], "message": "Same candidate application"})
			req := httptest.NewRequest("POST", "/jobs/"+job["id"].(string)+"/applications", strings.NewReader(string(raw)))
			req.Header.Set("Actor", buyer)
			req.Header.Set("Content-Type", "application/json")
			res, e := app.Test(req, 15000)
			if e != nil {
				codes <- 0
				return
			}
			res.Body.Close()
			codes <- res.StatusCode
		}()
	}
	close(start)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 && code != 201 {
			t.Fatal("application race failed", code)
		}
	}
	var count int
	if e := db.QueryRow(context.Background(), `SELECT count(*) FROM provider_conversation_messages`).Scan(&count); e != nil || count != 1 {
		t.Fatal("race duplicated first message", count, e)
	}
}

func TestJobApplicationReusesClosedThreadButNeverUnblocks(t *testing.T) {
	db := jobsDB(t)
	app := jobsApp(db)
	owner, provider := jobsActor(t, db, "bronze")
	buyer, service, thread := uuid.NewString(), uuid.NewString(), uuid.NewString()
	safetyExec(t, db, `INSERT INTO users(id)VALUES($1)`, buyer)
	safetyExec(t, db, `INSERT INTO provider_listings(id,provider_id)VALUES($1,$2)`, service, provider)
	safetyExec(t, db, `INSERT INTO provider_conversations(id,provider_id,user_id,listing_id,status)VALUES($1,$2,$3,$4,'blocked')`, thread, provider, buyer, service)
	job := jobsStatus(t, app, owner, jobsCreate(t, app, owner, "chat"), "published", 200)
	payload := map[string]any{"id": uuid.NewString(), "revision": job["revision"], "message": "Please consider my application"}
	jobsCall(t, app, "POST", "/jobs/"+job["id"].(string)+"/applications", buyer, payload, 409)
	var count int
	if e := db.QueryRow(context.Background(), `SELECT count(*) FROM provider_job_applications`).Scan(&count); e != nil || count != 0 {
		t.Fatal("blocked conversation accepted application", count, e)
	}
	safetyExec(t, db, `UPDATE provider_conversations SET status='closed' WHERE id=$1`, thread)
	receipt := jobsCall(t, app, "POST", "/jobs/"+job["id"].(string)+"/applications", buyer, payload, 201)
	if receipt["conversation_id"] != thread {
		t.Fatal("existing inbox identity replaced")
	}
	var status string
	if e := db.QueryRow(context.Background(), `SELECT status FROM provider_conversations WHERE id=$1`, thread).Scan(&status); e != nil || status != "open" {
		t.Fatal("application conversation remained unusable", status, e)
	}
}

func TestJobTierPlacesAndLegacyOverage(t *testing.T) {
	db := jobsDB117(t)
	app := jobsApp(db)
	owner, provider := jobsActor(t, db, "bronze")
	a, b := jobsCreate(t, app, owner, "chat"), jobsCreate(t, app, owner, "email")
	// Seed real pre-120 published jobs through the old guard, not by disabling constraints.
	safetyExec(t, db, `UPDATE provider_jobs SET status='published',published_at=clock_timestamp() WHERE id IN($1,$2)`, a["id"], b["id"])
	jobsIndependentMigration(t, db)
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM provider_jobs WHERE provider_id=$1 AND status='published'`, provider).Scan(&count); err != nil || count != 2 {
		t.Fatal("migration lost existing jobs", count, err)
	}
	if len(jobsCall(t, app, "GET", "/jobs", owner, nil, 200)["items"].([]any)) != 2 {
		t.Fatal("historical jobs hidden")
	}
	limits := jobsCall(t, app, "GET", "/owner/allowance", owner, nil, 200)
	if limits["limit"] != float64(1) || limits["used"] != float64(2) || limits["remaining"] != float64(0) {
		t.Fatal("legacy overage allowance", limits)
	}
	fresh := jobsCreate(t, app, owner, "chat")
	jobsStatus(t, app, owner, fresh, "published", 409)
	// Existing active jobs remain editable, but overages cannot extend their old deadlines.
	safetyExec(t, db, `UPDATE provider_jobs SET title='Updated title' WHERE id=$1`, a["id"])
	if _, err := db.Exec(context.Background(), `UPDATE provider_jobs SET deadline=deadline+interval '1 day' WHERE id=$1`, a["id"]); err == nil {
		t.Fatal("overage extended its deadline")
	}
	safetyExec(t, db, `UPDATE provider_jobs SET status='closed' WHERE id=$1`, a["id"])
	jobsStatus(t, app, owner, fresh, "published", 409)
	safetyExec(t, db, `UPDATE provider_jobs SET status='filled' WHERE id=$1`, b["id"])
	jobsStatus(t, app, owner, fresh, "published", 200)
	// Tier-based jobs are independent even when the product/service allowance is full.
	for _, tier := range []string{"free", "bronze", "silver", "diamond", "gold"} {
		t.Run(tier, func(t *testing.T) {
			actor, p := jobsActor(t, db, tier)
			var cap int
			if err := db.QueryRow(context.Background(), `SELECT (provider_current_benefits($1)->>'post_limit')::int`, p).Scan(&cap); err != nil {
				t.Fatal(err)
			}
			for n := 0; n < cap; n++ {
				safetyExec(t, db, `INSERT INTO products(provider_id)VALUES($1)`, p)
			}
			allowance := jobsCall(t, app, "GET", "/owner/allowance", actor, nil, 200)
			jobLimit := 1
			if tier == "diamond" {
				jobLimit = 5
			}
			if tier == "gold" {
				jobLimit = 10
			}
			if allowance["limit"] != float64(jobLimit) || allowance["remaining"] != float64(jobLimit) {
				t.Fatal("wrong independent tier limit", allowance)
			}
			first := jobsStatus(t, app, actor, jobsCreate(t, app, actor, "chat"), "published", 200)
			for n := 1; n < jobLimit; n++ {
				jobsStatus(t, app, actor, jobsCreate(t, app, actor, "chat"), "published", 200)
			}
			jobsStatus(t, app, actor, jobsCreate(t, app, actor, "chat"), "published", 409)
			safetyExec(t, db, `UPDATE products SET moderation_status='archived' WHERE provider_id=$1`, p)
			safetyExec(t, db, `INSERT INTO provider_listings(provider_id)VALUES($1)`, p)
			// Downgrade never hides the independent job (or consumes product/service capacity).
			safetyExec(t, db, `UPDATE provider_organizations SET tier='free' WHERE id=$1`, p)
			if jobsCall(t, app, "GET", "/jobs/"+first["id"].(string), actor, nil, 200)["available"] != true {
				t.Fatal("tier downgrade hid free job")
			}
		})
	}
}

func TestJobPlanComparisonOffersSeparateFreePlace(t *testing.T) {
	for _, code := range []string{"provider-free", "provider-monthly", "provider-growth", "provider-business", "provider-diamond", "legacy-custom"} {
		p := mergedPlanPolicy(code, 7, []byte(`{"post_limit":25,"max_images":8,"max_videos":0}`))
		want := 1
		if code == "provider-business" {
			want = 5
		}
		if code == "provider-diamond" {
			want = 10
		}
		if p.JobLimit != want {
			t.Fatal("plan comparison omitted free job", code, p)
		}
	}
}

func TestJobPaidTierConcurrentPublishesAndExpiry(t *testing.T) {
	db := jobsDB(t)
	app := jobsApp(db)
	for _, tc := range []struct {
		tier string
		cap  int
	}{{"diamond", 5}, {"gold", 10}} {
		t.Run(tc.tier, func(t *testing.T) {
			owner, provider := jobsActor(t, db, tc.tier)
			jobs := []map[string]any{}
			for n := 0; n < tc.cap+1; n++ {
				jobs = append(jobs, jobsCreate(t, app, owner, "chat"))
			}
			start := make(chan struct{})
			codes := make(chan int, len(jobs))
			var wg sync.WaitGroup
			for _, job := range jobs {
				wg.Add(1)
				go func(job map[string]any) {
					defer wg.Done()
					<-start
					raw, _ := json.Marshal(map[string]any{"status": "published", "revision": job["revision"]})
					req := httptest.NewRequest("POST", "/owner/"+job["id"].(string)+"/status", strings.NewReader(string(raw)))
					req.Header.Set("Actor", owner)
					req.Header.Set("Content-Type", "application/json")
					res, err := app.Test(req, 15000)
					if err != nil {
						codes <- 0
						return
					}
					defer res.Body.Close()
					codes <- res.StatusCode
				}(job)
			}
			close(start)
			wg.Wait()
			close(codes)
			counts := map[int]int{}
			for code := range codes {
				counts[code]++
			}
			if counts[200] != tc.cap || counts[409] != 1 {
				t.Fatal("tier publish race", counts)
			}
			safetyExec(t, db, `UPDATE provider_jobs SET deadline=clock_timestamp()-interval '1 second' WHERE id=(SELECT id FROM provider_jobs WHERE provider_id=$1 AND status='published' LIMIT 1)`, provider)
			if jobsCall(t, app, "GET", "/owner/allowance", owner, nil, 200)["remaining"] != float64(1) {
				t.Fatal("expired tier place not released")
			}
			fresh := jobsCreate(t, app, owner, "chat")
			jobsStatus(t, app, owner, fresh, "published", 200)
			safetyExec(t, db, `UPDATE provider_organizations SET tier='free' WHERE id=$1`, provider)
			limits := jobsCall(t, app, "GET", "/owner/allowance", owner, nil, 200)
			if limits["limit"] != float64(1) || limits["remaining"] != float64(0) {
				t.Fatal("downgrade allowance", limits)
			}
			jobsStatus(t, app, owner, jobsCreate(t, app, owner, "chat"), "published", 409)
			var count int
			if err := db.QueryRow(context.Background(), `SELECT count(*) FROM provider_jobs WHERE provider_id=$1 AND status='published' AND deadline>clock_timestamp()`, provider).Scan(&count); err != nil || count != tc.cap {
				t.Fatal("downgrade removed jobs", count, err)
			}
		})
	}
}
