package controllers

import (
	"across/backend/internal/services"
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestCommunityPromotionsValidateCachedPaths(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	users := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for _, user := range users {
		if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name) VALUES($1,'Friend')`, user); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO social_profiles(user_id,handle,connection_promotions) VALUES($1::uuid,'atl_'||replace($1::text,'-',''),true)`, user); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < len(users); i++ {
		if _, err := db.Exec(ctx, `INSERT INTO social_connections(user_low,user_high,requested_by,state) VALUES(LEAST($1::uuid,$2::uuid),GREATEST($1::uuid,$2::uuid),$1,'accepted')`, users[i-1], users[i]); err != nil {
			t.Fatal(err)
		}
	}
	provider, plan, sub := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, command := range []struct {
		SQL  string
		Args []any
	}{
		{`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status) VALUES($1,$2,'Paid seller','approved')`, []any{provider, users[3]}},
		{`INSERT INTO provider_subscription_plans(id,code,listing_limit,badge_tier) VALUES($1,'provider-monthly',5,'bronze')`, []any{plan}},
		{`INSERT INTO provider_subscriptions(id,provider_id,plan_id,status,current_period_end) VALUES($1,$2,$3,'active',now()+interval '1 month')`, []any{sub, provider, plan}},
		{`INSERT INTO provider_subscription_payments(subscription_id,amount,currency_code) VALUES($1,500,'NGN')`, []any{sub}},
		{`INSERT INTO provider_listings(id,provider_id,title,status) VALUES($1,$2,'Rice delivery','approved')`, []any{uuid.NewString(), provider}},
	} {
		if _, err := db.Exec(ctx, command.SQL, command.Args...); err != nil {
			t.Fatal(err)
		}
	}
	s := &CommunityController{db: db}
	paths := [][]string{users}
	check := func(want int) {
		t.Helper()
		items, err := s.promotionCandidates(ctx, users[0], "NG", "Abuja", paths)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != want {
			t.Fatalf("got%d want%d", len(items), want)
		}
	}
	check(1)
	filtered, filterErr := s.promotionCandidates(ctx, users[0], "NG", "Abuja", paths, "electronics")
	if filterErr != nil {
		t.Fatal(filterErr)
	}
	if len(filtered) != 0 {
		t.Fatal("unrelated category promoted")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := services.PromotionPaths(ctx, tx, users[0])
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(generated) != 1 || len(generated[0]) != 4 {
		t.Fatalf("wrong three-step audience: %v", generated)
	}
	if _, err := db.Exec(ctx, `UPDATE social_profiles SET connection_promotions=false WHERE user_id=$1`, users[1]); err != nil {
		t.Fatal(err)
	}
	check(0)
	if _, err := db.Exec(ctx, `UPDATE social_profiles SET connection_promotions=true WHERE user_id=$1`, users[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO social_blocks(blocker_id,blocked_id) VALUES($1,$2)`, users[0], users[3]); err != nil {
		t.Fatal(err)
	}
	check(0)
	if _, err := db.Exec(ctx, `DELETE FROM social_blocks`); err != nil {
		t.Fatal(err)
	}
	check(1)
	if _, err := db.Exec(ctx, `UPDATE social_connections SET state='declined' WHERE user_low=LEAST($1::uuid,$2::uuid) AND user_high=GREATEST($1::uuid,$2::uuid)`, users[1], users[2]); err != nil {
		t.Fatal(err)
	}
	check(0)
	if _, err := db.Exec(ctx, `UPDATE social_connections SET state='accepted'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE provider_subscriptions SET current_period_end=now()-interval '1 second' WHERE id=$1`, sub); err != nil {
		t.Fatal(err)
	}
	check(0)
	paths = [][]string{append(append([]string{}, users...), uuid.NewString())}
	check(0)
}
