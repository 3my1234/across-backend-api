package controllers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLaunchVideoThreeMinuteMigrationPreservesQuotas(t *testing.T) {
	db := tierEntitlementDB(t, nil)
	ctx := context.Background()
	run := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	_, gold, _ := tierProvider(t, db, "provider-diamond")
	old := "https://fixture.test/retained.mp4"
	run(`INSERT INTO provider_post_video_assets VALUES($1,$2,60,100,now())`, old, gold)
	run(`INSERT INTO products(id,provider_id,moderation_status,factory_details) VALUES(gen_random_uuid(),$1,'approved',jsonb_build_object('video_urls',jsonb_build_array($2::text)))`, gold, old)
	run(`CREATE TABLE admins(id uuid PRIMARY KEY);ALTER TABLE provider_conversations ADD CONSTRAINT conversation_subject_check CHECK(num_nonnulls(listing_id,product_id)=1)`)
	for _, name := range []string{"111_launch_video_allowances.sql", "117_provider_jobs.sql", "119_three_minute_provider_videos.sql"} {
		sql, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		run(string(sql))
	}
	var seconds, perListing, total int
	if err := db.QueryRow(ctx, `SELECT max_seconds,videos_per_listing,videos_per_provider FROM provider_video_launch_policy`).Scan(&seconds, &perListing, &total); err != nil || seconds != 180 || perListing != 1 || total != 5 {
		t.Fatal("unexpected quota change", seconds, perListing, total, err)
	}
	asset := "https://fixture.test/three-minutes.mp4"
	run(`INSERT INTO provider_post_video_assets VALUES($1,$2,180,100,now())`, asset, gold)
	run(`INSERT INTO provider_listings(id,provider_id,status,attributes) VALUES(gen_random_uuid(),$1,'draft',jsonb_build_object('video_urls',jsonb_build_array($2::text)))`, gold, asset)
	if _, err := db.Exec(ctx, `INSERT INTO provider_post_video_assets VALUES('https://fixture.test/too-long',$1,181,100,now())`, gold); err == nil {
		t.Fatal("181s asset accepted")
	}
	var retained int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM products WHERE provider_id=$1 AND factory_details->'video_urls' @> jsonb_build_array($2::text)`, gold, old).Scan(&retained); err != nil || retained != 1 {
		t.Fatal("published media lost", err)
	}
	_, bronze, _ := tierProvider(t, db, "provider-monthly")
	run(`INSERT INTO provider_post_video_assets VALUES('https://fixture.test/bronze',$1,180,100,now())`, bronze)
	if _, err := db.Exec(ctx, `INSERT INTO products(id,provider_id,factory_details) VALUES(gen_random_uuid(),$1,'{"video_urls":["https://fixture.test/bronze"]}')`, bronze); err == nil {
		t.Fatal("non-Gold new video accepted")
	}
	// Migration119 must preserve the Jobs-aware shared-capacity function from117.
	run(`INSERT INTO products(id,provider_id,moderation_status) VALUES(gen_random_uuid(),$1,'draft'),(gen_random_uuid(),$1,'draft')`, bronze)
	job := uuid.NewString()
	run(`INSERT INTO provider_jobs(id,provider_id,title,city,work_type,description,requirements,status,deadline) VALUES($1,$2,'Helper','Abuja','full_time','Help at the shop','Experience','published',now()+interval '1 day')`, job, bronze)
	if _, err := db.Exec(ctx, `INSERT INTO products(id,provider_id,moderation_status) VALUES(gen_random_uuid(),$1,'draft')`, bronze); err == nil {
		t.Fatal("active job no longer consumes shared slot")
	}
	run(`UPDATE provider_jobs SET deadline=now()-interval '1 second' WHERE id=$1`, job)
	run(`INSERT INTO products(id,provider_id,moderation_status) VALUES(gen_random_uuid(),$1,'draft')`, bronze)
	run(`UPDATE provider_video_launch_policy SET max_seconds=120`)
	if _, err := db.Exec(ctx, `INSERT INTO products(id,provider_id,factory_details) VALUES(gen_random_uuid(),$1,jsonb_build_object('video_urls',jsonb_build_array($2::text)))`, gold, asset); err == nil {
		t.Fatal("current lower video policy bypassed")
	}
}

func TestPostVideoThreeMinuteProcessingBoundary(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg acceptance runs in CI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	directory := t.TempDir()
	input := filepath.Join(directory, "input.mp4")
	output := filepath.Join(directory, "output.mp4")
	poster := filepath.Join(directory, "poster.jpg")
	if data, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=size=320x180:rate=1", "-t", "180", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-an", input).CombinedOutput(); err != nil {
		t.Fatal(err, string(data))
	}
	if err := processLaunchVideo(ctx, input, output, poster, 180); err != nil {
		t.Fatal("three-minute processing rejected", err)
	}
	if duration, err := probePostVideo(ctx, output); err != nil || duration != 180 {
		t.Fatal("wrong output duration", duration, err)
	}
	if info, err := os.Stat(output); err != nil || info.Size() > maxPostVideoBytes {
		t.Fatal("output upload limit", err)
	}
	if err := processLaunchVideo(ctx, input, output, poster, 30); err == nil {
		t.Fatal("old runtime policy was bypassed")
	}
}
