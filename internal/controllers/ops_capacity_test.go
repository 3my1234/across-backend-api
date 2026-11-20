package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"net/http/httptest"
	"testing"
)

func TestAdminCapacityHistoryBoundsAndLatestDetail(t *testing.T) {
	db := settlementTestDB(t)
	_, err := db.Exec(context.Background(), `CREATE TABLE ops_capacity_samples(host text,observed_at timestamptz,report jsonb);
 INSERT INTO ops_capacity_samples VALUES
 ('main',clock_timestamp(),'{"host":{"cpu_percent":40},"containers":[{"name":"api"}]}'),
 ('main',clock_timestamp()-interval '10 minutes','{"host":{"cpu_percent":20},"containers":[{"name":"old"}]}'),
 ('database',clock_timestamp(),'{"host":{"cpu_percent":10}}'),
 ('main',clock_timestamp()-interval '8 days','{"host":{"cpu_percent":99}}');`)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/capacity", NewOpsController(db).Capacity)
	for _, path := range []string{"/capacity?hours=999999", "/capacity?hours=6&full=1"} {
		res, e := app.Test(httptest.NewRequest("GET", path, nil))
		if e != nil {
			t.Fatal(e)
		}
		var data struct {
			Hours int `json:"hours"`
			Items []struct {
				Host   string         `json:"host"`
				Report map[string]any `json:"report"`
			} `json:"items"`
		}
		if e = json.NewDecoder(res.Body).Decode(&data); e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 200 || data.Hours != 6 || len(data.Items) != 3 {
			t.Fatal(res.StatusCode, data)
		}
		if path == "/capacity?hours=999999" && data.Items[2].Report["containers"] != nil {
			t.Fatal("historical detail should be compact by default")
		}
		if path == "/capacity?hours=6&full=1" && data.Items[2].Report["containers"] == nil {
			t.Fatal("explicit download lost historical detail")
		}
	}
}
