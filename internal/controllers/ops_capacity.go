package controllers

import (
	"context"
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"strconv"
	"time"
)

// History is protected by super-admin middleware at registration. No raw SQL,
// tokens, recipients, query strings or request paths enter these reports.
func (o *OpsController) Capacity(c *fiber.Ctx) error {
	c.Set("Cache-Control", "private, no-store")
	hours, _ := strconv.Atoi(c.Query("hours", "6"))
	if hours < 1 || hours > 168 {
		hours = 6
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 5*time.Second)
	defer cancel()
	// At most 720 points per host; use the most recent sample in each time bin.
	step := max(60, hours*3600/720)
	rows, err := o.db.Query(ctx, `SELECT host,observed_at,CASE WHEN observed_at=(SELECT max(x.observed_at) FROM ops_capacity_samples x WHERE x.host=s.host) OR $3 THEN report ELSE jsonb_build_object('host',report->'host','alerts',report->'alerts','database',report->'database') END FROM (SELECT DISTINCT ON(host,floor(extract(epoch FROM observed_at)/$2)) host,observed_at,report FROM ops_capacity_samples WHERE observed_at>clock_timestamp()-make_interval(hours=>$1) ORDER BY host,floor(extract(epoch FROM observed_at)/$2),observed_at DESC) s ORDER BY observed_at DESC LIMIT 1440`, hours, step, c.Query("full") == "1")
	if err != nil {
		return fiber.NewError(503, "Capacity history unavailable")
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var host string
		var at time.Time
		var report json.RawMessage
		if err = rows.Scan(&host, &at, &report); err != nil {
			return fiber.ErrServiceUnavailable
		}
		items = append(items, fiber.Map{"host": host, "observed_at": at, "report": report})
	}
	if rows.Err() != nil {
		return fiber.ErrServiceUnavailable
	}
	return c.JSON(fiber.Map{"items": items, "server_time": time.Now().UTC(), "retention_days": 7, "sample_interval_seconds": 60, "hours": hours, "bucket_seconds": step})
}
