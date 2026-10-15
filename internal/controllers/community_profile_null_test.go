package controllers

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestCommunityProfileAllowsMissingNameAndPhoto(t *testing.T) {
	db := communityTestDB(t)
	actor := uuid.NewString()
	if _, err := db.Exec(context.Background(), `INSERT INTO users(id,full_name,avatar_url) VALUES($1,NULL,NULL)`, actor); err != nil {
		t.Fatal(err)
	}
	profile := communityCall(t, communityApp(db), "GET", "/profile", actor, "", 200)
	if profile["name"] != "" || profile["avatar_url"] != "" {
		t.Fatal("missing optional identity must be empty", profile)
	}
}
