package controllers

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestJobPhotosOwnershipRetentionAndRemoval(t *testing.T) {
	db := jobsDB(t)
	app := jobsApp(db)
	owner, provider := jobsActor(t, db, "free")
	other, otherProvider := jobsActor(t, db, "free")
	image := "https://media.example.test/user-uploads/providers/" + provider + "/listing/123_workplace.jpg"
	payload := jobsPayload(uuid.NewString(), 0, "chat")
	payload["image_urls"] = []string{image}
	saved := jobsCall(t, app, "POST", "/owner", owner, payload, 201)
	if len(saved["image_urls"].([]any)) != 1 {
		t.Fatal(saved)
	}
	id := saved["id"].(string)
	published := jobsStatus(t, app, owner, saved, "published", 200)
	public := jobsCall(t, app, "GET", "/jobs/"+id, "", nil, 200)
	if public["image_urls"].([]any)[0] != image {
		t.Fatal("public cover missing", public)
	}
	// An older client's PUT lacks image_urls. It must preserve photos already saved.
	update := jobsPayload(id, int64(published["revision"].(float64)), "chat")
	retained := jobsCall(t, app, "PUT", "/owner/"+id, owner, update, 200)
	if retained["image_urls"].([]any)[0] != image {
		t.Fatal("older editor erased photos")
	}
	for _, images := range [][]string{{image, image}, {"https://evil.example.test/user-uploads/providers/" + provider + "/listing/photo.jpg"}, {"https://media.example.test/private/providers/" + provider + "/verification/id.jpg"}, {"https://media.example.test/user-uploads/providers/" + otherProvider + "/listing/photo.jpg"}, {image, image, image, image, image}} {
		update["revision"] = retained["revision"]
		update["image_urls"] = images
		jobsCall(t, app, "PUT", "/owner/"+id, owner, update, 422)
	}
	jobsCall(t, app, "PUT", "/owner/"+id, other, jobsPayload(id, int64(retained["revision"].(float64)), "chat"), 404)
	update["revision"] = retained["revision"]
	update["image_urls"] = []string{}
	removed := jobsCall(t, app, "PUT", "/owner/"+id, owner, update, 200)
	if len(removed["image_urls"].([]any)) != 0 {
		t.Fatal("explicit photo removal failed")
	}
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM provider_jobs WHERE provider_id=$1 AND status='published'`, provider).Scan(&count); err != nil || count != 1 {
		t.Fatal("photo edits changed job allowance", count, err)
	}
}
