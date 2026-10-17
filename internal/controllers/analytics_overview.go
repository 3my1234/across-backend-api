package controllers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
)

// A single bounded query releases its connection before the response is rendered.
// Money is gross verified receipts, never marketplace GMV or calculated profit.
const analyticsOverviewSQL = `WITH days AS (
 SELECT generate_series((now() AT TIME ZONE 'Africa/Lagos')::date-29,
 (now() AT TIME ZONE 'Africa/Lagos')::date,'1 day')::date AS day
), paid AS (
 SELECT (paid_at AT TIME ZONE 'Africa/Lagos')::date AS day,
 count(*) AS receipts, sum(amount) AS gross,
 count(*) FILTER (WHERE refund_status IN ('pending','partial','refunded') OR chargeback_status IN ('open','lost','accepted')) AS flagged
 FROM payments WHERE purpose='provider_subscription' AND payment_status='succeeded'
 AND currency_code='NGN' AND paid_at >= (((now() AT TIME ZONE 'Africa/Lagos')::date-29)::timestamp AT TIME ZONE 'Africa/Lagos')
 AND paid_at <= now() GROUP BY 1
), daily AS (
 SELECT d.day,coalesce(p.receipts,0) AS receipts,coalesce(p.gross,0) AS gross,coalesce(p.flagged,0) AS flagged
 FROM days d LEFT JOIN paid p ON p.day=d.day
), issues AS (
 SELECT count(*) AS complaints,count(*) FILTER(WHERE status='unresolved') AS unresolved,
 coalesce(sum(refund_amount) FILTER(WHERE status='resolved'),0) AS recorded_refunds
 FROM complaints WHERE created_at >= (((now() AT TIME ZONE 'Africa/Lagos')::date-29)::timestamp AT TIME ZONE 'Africa/Lagos') AND created_at<=now()
)
SELECT jsonb_build_object('period_days',30,'timezone','Africa/Lagos','currency','NGN',
 'from',min(day),'through',max(day),'receipts',sum(receipts),
 'gross_receipts',CASE WHEN $1 THEN sum(gross) ELSE NULL END,
 'flagged_receipts',sum(flagged),
 'complaints',(SELECT complaints FROM issues),'unresolved_complaints',(SELECT unresolved FROM issues),
 'recorded_refunds',CASE WHEN $1 THEN (SELECT recorded_refunds FROM issues) ELSE NULL END,
 'daily',jsonb_agg(jsonb_build_object('date',day,'receipts',receipts,'gross_receipts',CASE WHEN $1 THEN gross ELSE NULL END) ORDER BY day)) FROM daily`

func (a *AnalyticsController) GetOverview(c *fiber.Ctx) error {
	role, _ := c.Locals("admin_role").(string)
	ctx, cancel := context.WithTimeout(c.UserContext(), 5*time.Second)
	defer cancel()
	var raw []byte
	if err := a.db.QueryRow(ctx, analyticsOverviewSQL, role == "super_admin").Scan(&raw); err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "Analytics could not finish. Please retry.")
	}
	var result json.RawMessage = raw
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(result)
}
