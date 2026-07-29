package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type batchActionSpec struct {
	From  []string
	To    string
	Roles []string
}

var batchActionSpecs = map[string]batchActionSpec{
	"close_collection":              {From: []string{"collecting_funds"}, To: "closed", Roles: []string{"catalog_admin"}},
	"reconcile":                     {From: []string{"closed"}, To: "settled", Roles: []string{"catalog_admin"}},
	"send_procurement_funds":        {From: []string{"settled"}, To: "funds_sent_to_procurement", Roles: []string{"super_admin"}},
	"acknowledge_procurement_funds": {From: []string{"funds_sent_to_procurement"}, To: "procurement_acknowledged", Roles: []string{"procurement_admin"}},
	"start_procurement":             {From: []string{"procurement_acknowledged"}, To: "purchasing", Roles: []string{"procurement_admin"}},
	"complete_procurement":          {From: []string{"purchasing"}, To: "procurement_complete", Roles: []string{"procurement_admin"}},
	"dispatch":                      {From: []string{"procurement_complete"}, To: "enroute_nigeria", Roles: []string{"procurement_admin"}},
	"confirm_arrival":               {From: []string{"enroute_nigeria"}, To: "arrived_local", Roles: []string{"courier_admin"}},
	"ready_for_pickup":              {From: []string{"arrived_local"}, To: "ready_for_pickup", Roles: []string{"courier_admin"}},
}

type batchTransitionRequest struct {
	Action          string  `json:"action"`
	ExpectedVersion int64   `json:"expected_version"`
	Amount          float64 `json:"amount"`
	Currency        string  `json:"currency"`
	Reference       string  `json:"reference"`
	Location        string  `json:"location"`
	PickupLocation  string  `json:"pickup_location"`
	PickupPhone     string  `json:"pickup_phone"`
	Notes           string  `json:"notes"`
}

func validateBatchAction(role, currentStatus, action string) (batchActionSpec, error) {
	spec, ok := batchActionSpecs[action]
	if !ok {
		return batchActionSpec{}, fiber.NewError(fiber.StatusBadRequest, "unsupported batch action")
	}
	role = normalizeAdminRole(role)
	if role != "super_admin" && !containsString(spec.Roles, role) {
		return batchActionSpec{}, fiber.NewError(fiber.StatusForbidden, "this action belongs to another admin role")
	}
	if !containsString(spec.From, currentStatus) {
		return batchActionSpec{}, fiber.NewError(
			fiber.StatusConflict,
			fmt.Sprintf("cannot %s while batch is %s", strings.ReplaceAll(action, "_", " "), currentStatus),
		)
	}
	return spec, nil
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func nullableAdminID(adminID string) any {
	if strings.TrimSpace(adminID) == "" || adminID == "bootstrap" {
		return nil
	}
	return adminID
}

// TransitionBatch is the only interactive endpoint that advances a batch.
// The backend validates role ownership, current state, preconditions, and version.
func (o *OpsController) TransitionBatch(c *fiber.Ctx) error {
	batchID := c.Params("batch_id")
	adminID, _ := c.Locals("admin_id").(string)
	role, _ := c.Locals("admin_role").(string)
	var req batchTransitionRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.Currency = strings.ToUpper(strings.TrimSpace(req.Currency))
	req.Reference = strings.TrimSpace(req.Reference)
	req.Location = strings.TrimSpace(req.Location)
	req.PickupLocation = strings.TrimSpace(req.PickupLocation)
	req.PickupPhone = strings.TrimSpace(req.PickupPhone)
	req.Notes = strings.TrimSpace(req.Notes)

	tx, err := o.db.Begin(c.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(c.Context())

	var currentStatus string
	var currentVersion int64
	if err := tx.QueryRow(c.Context(), `
		SELECT status::text, version
		FROM order_batches
		WHERE id = $1
		FOR UPDATE
	`, batchID).Scan(&currentStatus, &currentVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "batch not found")
		}
		return err
	}
	if req.ExpectedVersion > 0 && req.ExpectedVersion != currentVersion {
		return fiber.NewError(fiber.StatusConflict, "batch changed; reload before trying again")
	}

	spec, err := validateBatchAction(role, currentStatus, req.Action)
	if err != nil {
		return err
	}
	if err := validateBatchActionPayload(c.Context(), tx, batchID, req); err != nil {
		return err
	}

	metadata, err := json.Marshal(map[string]any{
		"action":          req.Action,
		"actor_role":      normalizeAdminRole(role),
		"amount":          req.Amount,
		"currency":        req.Currency,
		"reference":       req.Reference,
		"pickup_location": req.PickupLocation,
		"pickup_phone":    req.PickupPhone,
	})
	if err != nil {
		return err
	}

	location := req.Location
	if req.PickupLocation != "" {
		location = req.PickupLocation
	}
	actorID := nullableAdminID(adminID)
	var newVersion int64
	if err := tx.QueryRow(c.Context(), `
		UPDATE order_batches
		SET status = $2::batch_status,
			current_location = COALESCE(NULLIF($3, ''), current_location),
			notes = CASE WHEN $4 <> '' THEN $4 ELSE notes END,
			membership_locked = CASE WHEN $5 = 'close_collection' THEN true ELSE membership_locked END,
			closed_at = CASE WHEN $5 = 'close_collection' THEN COALESCE(closed_at, now()) ELSE closed_at END,
			reconciled_at = CASE WHEN $5 = 'reconcile' THEN now() ELSE reconciled_at END,
			reconciled_by = CASE WHEN $5 = 'reconcile' THEN $6::uuid ELSE reconciled_by END,
			procurement_funds_amount = CASE WHEN $5 = 'send_procurement_funds' THEN $7 ELSE procurement_funds_amount END,
			procurement_funds_currency = CASE WHEN $5 = 'send_procurement_funds' THEN $8 ELSE procurement_funds_currency END,
			procurement_funds_reference = CASE WHEN $5 = 'send_procurement_funds' THEN $9 ELSE procurement_funds_reference END,
			procurement_funds_sent_at = CASE WHEN $5 = 'send_procurement_funds' THEN now() ELSE procurement_funds_sent_at END,
			procurement_funds_sent_by = CASE WHEN $5 = 'send_procurement_funds' THEN $6::uuid ELSE procurement_funds_sent_by END,
			procurement_funds_acknowledged_at = CASE WHEN $5 = 'acknowledge_procurement_funds' THEN now() ELSE procurement_funds_acknowledged_at END,
			procurement_funds_acknowledged_by = CASE WHEN $5 = 'acknowledge_procurement_funds' THEN $6::uuid ELSE procurement_funds_acknowledged_by END,
			procurement_completed_at = CASE WHEN $5 = 'complete_procurement' THEN now() ELSE procurement_completed_at END,
			procurement_completed_by = CASE WHEN $5 = 'complete_procurement' THEN $6::uuid ELSE procurement_completed_by END,
			version = version + 1,
			updated_at = now()
		WHERE id = $1 AND version = $10
		RETURNING version
	`, batchID, spec.To, location, req.Notes, req.Action, actorID, req.Amount,
		defaultCurrency(req.Currency), req.Reference, currentVersion).Scan(&newVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusConflict, "batch changed; reload before trying again")
		}
		return err
	}

	if err := applyTransitionOrderEffects(c.Context(), tx, batchID, req, spec.To); err != nil {
		return err
	}
	if _, err := tx.Exec(c.Context(), `
		INSERT INTO batch_events(
			batch_id, actor_id, event_type, previous_status, status, location, notes, metadata
		)
		VALUES ($1, $2, $3, $4::batch_status, $5::batch_status, $6, $7, $8::jsonb)
	`, batchID, actorID, req.Action, currentStatus, spec.To, location, req.Notes, metadata); err != nil {
		return err
	}
	if err := insertTransitionNotifications(c.Context(), tx, batchID, req.Action, spec.To, location); err != nil {
		return err
	}
	if err := tx.Commit(c.Context()); err != nil {
		return err
	}

	return c.JSON(fiber.Map{
		"batch_id": batchID,
		"status":   spec.To,
		"version":  newVersion,
		"updated":  true,
	})
}

func defaultCurrency(value string) string {
	if value == "" {
		return "NGN"
	}
	return value
}

func isISO4217Code(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func validateBatchActionPayload(ctx context.Context, tx pgx.Tx, batchID string, req batchTransitionRequest) error {
	switch req.Action {
	case "send_procurement_funds":
		if req.Amount <= 0 || req.Reference == "" {
			return fiber.NewError(fiber.StatusBadRequest, "positive amount and transfer reference are required")
		}
		if !isISO4217Code(defaultCurrency(req.Currency)) {
			return fiber.NewError(fiber.StatusBadRequest, "currency must be a three-letter ISO 4217 code")
		}
	case "complete_procurement":
		var unresolved int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*)::int
			FROM order_items oi
			JOIN orders o ON o.id = oi.order_id
			WHERE o.batch_id = $1
			  AND (
				oi.purchase_status = 'pending'
				OR (
					oi.purchase_status = 'failed'
					AND oi.exception_resolution IN ('none', 'pending')
				)
			  )
		`, batchID).Scan(&unresolved); err != nil {
			return err
		}
		if unresolved > 0 {
			return fiber.NewError(fiber.StatusConflict, "resolve all pending and failed manifest items before completing procurement")
		}
	case "confirm_arrival":
		if req.Location == "" {
			return fiber.NewError(fiber.StatusBadRequest, "arrival location is required")
		}
	case "ready_for_pickup":
		if req.PickupLocation == "" || req.PickupPhone == "" {
			return fiber.NewError(fiber.StatusBadRequest, "pickup location and phone are required")
		}
	}
	return nil
}

func applyTransitionOrderEffects(ctx context.Context, tx pgx.Tx, batchID string, req batchTransitionRequest, targetStatus string) error {
	var trackingStage, orderStatus, trackingNote string
	switch targetStatus {
	case "purchasing":
		trackingStage = "Arrived at China Hub"
		trackingNote = "Procurement started at the China operations hub"
	case "enroute_nigeria":
		trackingStage = "In Transit Internationally"
		orderStatus = "Shipped"
		trackingNote = "Batch dispatched from China"
	case "arrived_local":
		trackingStage = "Arrived at Local Hub"
		trackingNote = "Batch received at the local hub"
	case "ready_for_pickup":
		trackingStage = "Arrived at Local Hub"
		trackingNote = "Order sorted and ready for pickup or delivery"
	default:
		return nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE orders
		SET current_tracking_stage = $2::tracking_stage,
			order_status = CASE WHEN $3 = '' THEN order_status ELSE $3::order_status END,
			pickup_location = CASE WHEN $4 = '' THEN pickup_location ELSE $4 END,
			pickup_phone = CASE WHEN $5 = '' THEN pickup_phone ELSE $5 END,
			delivery_notes = CASE WHEN $6 = '' THEN delivery_notes ELSE $6 END,
			updated_at = now()
		WHERE batch_id = $1
	`, batchID, trackingStage, orderStatus, req.PickupLocation, req.PickupPhone, req.Notes); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO tracking_events(order_id, stage, notes)
		SELECT id, $2::tracking_stage, $3
		FROM orders
		WHERE batch_id = $1
	`, batchID, trackingStage, trackingNote)
	return err
}

func insertTransitionNotifications(ctx context.Context, tx pgx.Tx, batchID, action, status, location string) error {
	var notificationType, title, body string
	switch action {
	case "start_procurement":
		notificationType = "product_purchased"
		title = "Procurement has started"
		body = "Your order is being secured from the supplier."
	case "complete_procurement":
		notificationType = "product_purchased"
		title = "Procurement completed"
		body = "Your order has been procured and is being prepared for dispatch."
	case "dispatch":
		notificationType = "enroute_international"
		title = "In transit to Nigeria"
		body = "Your package has left China and is heading to Nigeria."
	case "confirm_arrival":
		notificationType = "arrived_local"
		title = "Package arrived in Nigeria"
		body = "Your package has arrived at " + location + " and is being sorted."
	case "ready_for_pickup":
		notificationType = "ready_for_pickup"
		title = "Package ready for pickup"
		body = "Your package is ready at " + location + "."
	default:
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO notifications(
			user_id, order_id, batch_id, type, title, body, data, event_key
		)
		SELECT DISTINCT o.user_id, o.id, $1, $2, $3, $4,
			jsonb_build_object('batch_status', $5, 'location', $6),
			'batch-transition:' || $1::text || ':' || $7 || ':' || o.id::text
		FROM orders o
		WHERE o.batch_id = $1
		ON CONFLICT DO NOTHING
	`, batchID, notificationType, title, body, status, location, action)
	return err
}

// CloseExpiredBatches atomically locks every open batch whose local business day
// has ended. Multiple API replicas can safely run it concurrently.
func CloseExpiredBatches(ctx context.Context, db *pgxpool.Pool) (int64, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		UPDATE order_batches b
		SET status = 'closed'::batch_status,
			membership_locked = true,
			closed_at = COALESCE(closed_at, now()),
			version = version + 1,
			updated_at = now()
		FROM countries_config c
		WHERE c.id = b.country_id
		  AND b.status = 'collecting_funds'::batch_status
		  AND b.membership_locked = false
		  AND b.batch_date < (
			now() AT TIME ZONE CASE
				WHEN c.country_code = 'NG' AND c.operational_timezone = 'UTC'
					THEN 'Africa/Lagos'
				ELSE c.operational_timezone
			END
		  )::date
		RETURNING b.id
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `
			INSERT INTO batch_events(
				batch_id, event_type, previous_status, status, notes, metadata
			)
			VALUES (
				$1, 'automatic_daily_close', 'collecting_funds', 'closed',
				'Operational day ended',
				jsonb_build_object('source', 'lagos_batch_closure_worker')
			)
		`, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}
