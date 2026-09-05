package controllers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type fulfillmentTransitionPayload struct {
	Status            string `json:"status"`
	ExpectedVersion   int64  `json:"expected_version"`
	IdempotencyKey    string `json:"idempotency_key"`
	Notes             string `json:"notes"`
	Location          string `json:"location"`
	Carrier           string `json:"carrier"`
	TrackingNumber    string `json:"tracking_number"`
	TrackingURL       string `json:"tracking_url"`
	EstimatedDelivery string `json:"estimated_delivery_at"`
}

var merchantLocalTransitions = map[string]map[string]bool{
	"pending":          {"accepted": true},
	"accepted":         {"packed": true},
	"packed":           {"ready_for_pickup": true, "out_for_delivery": true, "handed_to_atlantic": true},
	"ready_for_pickup": {"delivered": true},
	"out_for_delivery": {"delivered": true},
}

var merchantCrossBorderTransitions = map[string]map[string]bool{
	"pending":                {"accepted": true},
	"accepted":               {"processing": true},
	"processing":             {"dispatched_from_origin": true},
	"dispatched_from_origin": {"international_transit": true},
	"international_transit":  {"customs_clearance": true, "local_hub": true},
	"customs_clearance":      {"local_hub": true},
	"local_hub":              {"ready_for_pickup": true, "out_for_delivery": true, "handed_to_atlantic": true},
	"ready_for_pickup":       {"delivered": true},
	"out_for_delivery":       {"delivered": true},
}

var atlanticLastMileTransitions = map[string]map[string]bool{
	"handed_to_atlantic": {"local_hub": true},
	"local_hub":          {"ready_for_pickup": true, "out_for_delivery": true},
	"ready_for_pickup":   {"delivered": true},
	"out_for_delivery":   {"delivered": true},
}

func validFulfillmentTransition(route, owner, from, to string) bool {
	var transitions map[string]map[string]bool
	if owner == "atlantic_last_mile" {
		transitions = atlanticLastMileTransitions
	} else if route == "merchant_local" {
		transitions = merchantLocalTransitions
	} else if route == "merchant_cross_border" {
		transitions = merchantCrossBorderTransitions
	}
	return transitions != nil && transitions[from][to]
}

func legacyStageForFulfillment(route, status string) (string, string) {
	switch status {
	case "international_transit", "customs_clearance":
		return "In Transit Internationally", "Shipped"
	case "local_hub", "handed_to_atlantic", "ready_for_pickup":
		return "Arrived at Local Hub", "Shipped"
	case "out_for_delivery":
		return "Out for Delivery", "Shipped"
	case "delivered":
		return "Delivered", "Delivered"
	case "accepted", "packed":
		if route == "merchant_local" {
			return "Order Placed", "Paid"
		}
		return "Arrived at China Hub", "Paid"
	case "processing", "dispatched_from_origin":
		return "Arrived at China Hub", "Paid"
	default:
		return "Order Placed", "Paid"
	}
}

func fulfillmentNotification(status string) (string, string) {
	switch status {
	case "accepted":
		return "Order accepted", "The seller has accepted your paid order."
	case "packed":
		return "Order packed", "The seller has packed your order."
	case "processing":
		return "Order processing", "The seller is preparing your international order."
	case "dispatched_from_origin":
		return "Dispatched from origin", "Your order has left the seller's origin facility."
	case "international_transit":
		return "In transit internationally", "Your order is travelling to Nigeria."
	case "customs_clearance":
		return "Customs clearance", "Your order is being processed by customs."
	case "local_hub":
		return "Arrived at local hub", "Your order has arrived at the local hub."
	case "handed_to_atlantic":
		return "Handed to Atlantic Express", "Atlantic Express is handling the last mile of your order."
	case "ready_for_pickup":
		return "Ready for pickup", "Your order is ready for pickup. Check the tracking page for the location."
	case "out_for_delivery":
		return "Out for delivery", "Your order is on its way to you."
	case "delivered":
		return "Package delivered", "Confirm receipt from your tracking page after you receive the package."
	default:
		return "Order updated", "Your order fulfilment status has changed."
	}
}

func parseEstimatedDelivery(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("estimated_delivery_at must be RFC3339")
	}
	return &parsed, nil
}

func parseFulfillmentTransition(c *fiber.Ctx) (fulfillmentTransitionPayload, *time.Time, error) {
	var req fulfillmentTransitionPayload
	if err := c.BodyParser(&req); err != nil {
		return req, nil, fiber.ErrBadRequest
	}
	req.Status = strings.ToLower(strings.TrimSpace(req.Status))
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = strings.TrimSpace(c.Get("Idempotency-Key"))
	}
	if req.Status == "" || req.ExpectedVersion < 1 || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 160 {
		return req, nil, fiber.NewError(fiber.StatusUnprocessableEntity, "status, expected_version, and idempotency_key are required")
	}
	estimated, err := parseEstimatedDelivery(req.EstimatedDelivery)
	if err != nil {
		return req, nil, fiber.NewError(fiber.StatusUnprocessableEntity, err.Error())
	}
	return req, estimated, nil
}

func (m *ProviderMarketplaceController) TransitionMerchantOrder(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, role, err := m.providerForUser(c.Context(), userID)
	if err != nil || (role != "owner" && role != "manager" && role != "staff") {
		return fiber.ErrForbidden
	}
	req, estimated, err := parseFulfillmentTransition(c)
	if err != nil {
		return err
	}
	return m.transitionFulfillment(c, c.Params("order_id"), providerID, "merchant", userID, req, estimated)
}

func (m *ProviderMarketplaceController) TransitionAtlanticLastMile(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)
	req, estimated, err := parseFulfillmentTransition(c)
	if err != nil {
		return err
	}
	return m.transitionFulfillment(c, c.Params("order_id"), "", "admin", adminID, req, estimated)
}

func (m *ProviderMarketplaceController) transitionFulfillment(c *fiber.Ctx, orderID, providerID, actorType, actorID string, req fulfillmentTransitionPayload, estimated *time.Time) error {
	tx, err := m.db.Begin(c.Context())
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer tx.Rollback(c.Context())

	var fulfillmentID, userID, route, owner, currentStatus string
	var version int64
	query := `SELECT f.id::text,o.user_id::text,f.route,f.owner,f.status,f.version
		FROM order_fulfillments f JOIN orders o ON o.id=f.order_id
		WHERE f.order_id=$1::uuid AND o.paid_at IS NOT NULL`
	args := []any{orderID}
	if actorType == "merchant" {
		query += ` AND f.provider_id=$2::uuid AND f.owner='merchant'`
		args = append(args, providerID)
	} else {
		query += ` AND f.owner='atlantic_last_mile'`
	}
	query += ` FOR UPDATE`
	if err = tx.QueryRow(c.Context(), query, args...).Scan(&fulfillmentID, &userID, &route, &owner, &currentStatus, &version); err != nil {
		if err == pgx.ErrNoRows {
			return fiber.NewError(fiber.StatusNotFound, "paid fulfilment not found or not assigned to you")
		}
		return fiber.ErrInternalServerError
	}

	var existingStatus string
	err = tx.QueryRow(c.Context(), `SELECT status FROM fulfillment_events WHERE fulfillment_id=$1::uuid AND idempotency_key=$2`, fulfillmentID, req.IdempotencyKey).Scan(&existingStatus)
	if err == nil {
		return c.JSON(fiber.Map{"order_id": orderID, "status": existingStatus, "version": version, "idempotent_replay": true})
	}
	if err != pgx.ErrNoRows {
		return fiber.ErrInternalServerError
	}
	if version != req.ExpectedVersion {
		return fiber.NewError(fiber.StatusConflict, "fulfilment changed in another session; refresh and try the current next action")
	}
	if !validFulfillmentTransition(route, owner, currentStatus, req.Status) {
		return fiber.NewError(fiber.StatusConflict, fmt.Sprintf("cannot move %s fulfilment from %s to %s", route, currentStatus, req.Status))
	}

	newOwner := owner
	if req.Status == "handed_to_atlantic" {
		newOwner = "atlantic_last_mile"
	}
	stage, orderStatus := legacyStageForFulfillment(route, req.Status)
	meta, _ := json.Marshal(map[string]any{"carrier": strings.TrimSpace(req.Carrier), "tracking_number": strings.TrimSpace(req.TrackingNumber), "tracking_url": strings.TrimSpace(req.TrackingURL)})
	tag, err := tx.Exec(c.Context(), `UPDATE order_fulfillments SET owner=$3,status=$4,carrier=COALESCE(NULLIF($5,''),carrier),tracking_number=COALESCE(NULLIF($6,''),tracking_number),tracking_url=COALESCE(NULLIF($7,''),tracking_url),current_location=COALESCE(NULLIF($8,''),current_location),estimated_delivery_at=COALESCE($9,estimated_delivery_at),accepted_at=CASE WHEN $4='accepted' THEN COALESCE(accepted_at,now()) ELSE accepted_at END,dispatched_at=CASE WHEN $4 IN ('dispatched_from_origin','international_transit') THEN COALESCE(dispatched_at,now()) ELSE dispatched_at END,handed_to_atlantic_at=CASE WHEN $4='handed_to_atlantic' THEN COALESCE(handed_to_atlantic_at,now()) ELSE handed_to_atlantic_at END,delivered_at=CASE WHEN $4='delivered' THEN COALESCE(delivered_at,now()) ELSE delivered_at END,version=version+1,updated_at=now() WHERE id=$1::uuid AND version=$2`, fulfillmentID, version, newOwner, req.Status, strings.TrimSpace(req.Carrier), strings.TrimSpace(req.TrackingNumber), strings.TrimSpace(req.TrackingURL), strings.TrimSpace(req.Location), estimated)
	if err != nil || tag.RowsAffected() != 1 {
		return fiber.NewError(fiber.StatusConflict, "fulfilment changed; refresh and retry")
	}
	if _, err = tx.Exec(c.Context(), `UPDATE orders SET current_tracking_stage=$2::tracking_stage,order_status=$3::order_status,delivered_at=CASE WHEN $3='Delivered' THEN COALESCE(delivered_at,now()) ELSE delivered_at END,updated_at=now() WHERE id=$1::uuid`, orderID, stage, orderStatus); err != nil {
		return fiber.ErrInternalServerError
	}
	if _, err = tx.Exec(c.Context(), `INSERT INTO fulfillment_events(fulfillment_id,actor_type,actor_id,previous_status,status,notes,location,metadata,idempotency_key) VALUES($1::uuid,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8::jsonb,$9)`, fulfillmentID, actorType, actorID, currentStatus, req.Status, strings.TrimSpace(req.Notes), strings.TrimSpace(req.Location), meta, req.IdempotencyKey); err != nil {
		return fiber.ErrInternalServerError
	}
	if _, err = tx.Exec(c.Context(), `INSERT INTO tracking_events(order_id,stage,notes) VALUES($1::uuid,$2::tracking_stage,$3)`, orderID, stage, strings.TrimSpace(req.Notes)); err != nil {
		return fiber.ErrInternalServerError
	}
	title, body := fulfillmentNotification(req.Status)
	if err = insertNotification(c.Context(), tx, userID, orderID, nil, "fulfillment_update", title, body, map[string]any{"order_id": orderID, "route": route, "status": req.Status, "location": strings.TrimSpace(req.Location)}, "fulfillment:"+fulfillmentID+":"+req.IdempotencyKey); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"order_id": orderID, "route": route, "owner": newOwner, "status": req.Status, "tracking_stage": stage, "version": version + 1})
}

func newMerchantManifestCode() string {
	return "MER-" + time.Now().UTC().Format("20060102") + "-" + strings.ToUpper(uuid.NewString()[:8])
}

type merchantManifestPayload struct {
	OrderIDs      []string  `json:"order_ids"`
	OriginCountry string    `json:"origin_country_code"`
	OriginCity    string    `json:"origin_city"`
	CutoffAt      time.Time `json:"cutoff_at"`
}

func (m *ProviderMarketplaceController) CreateMerchantManifest(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, role, err := m.providerForUser(c.Context(), userID)
	if err != nil || (role != "owner" && role != "manager" && role != "staff") {
		return fiber.ErrForbidden
	}
	var req merchantManifestPayload
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	req.OriginCountry = strings.ToUpper(strings.TrimSpace(req.OriginCountry))
	req.OriginCity = strings.TrimSpace(req.OriginCity)
	if len(req.OrderIDs) == 0 || len(req.OrderIDs) > 500 || len(req.OriginCountry) != 2 || req.CutoffAt.IsZero() {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "order_ids, two-letter origin_country_code, and cutoff_at are required")
	}
	seen := make(map[string]bool, len(req.OrderIDs))
	for _, orderID := range req.OrderIDs {
		if _, err := uuid.Parse(orderID); err != nil || seen[orderID] {
			return fiber.NewError(fiber.StatusUnprocessableEntity, "order_ids must contain unique UUIDs")
		}
		seen[orderID] = true
	}
	tx, err := m.db.Begin(c.Context())
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer tx.Rollback(c.Context())
	manifestID, code := uuid.NewString(), newMerchantManifestCode()
	if _, err = tx.Exec(c.Context(), `INSERT INTO merchant_manifests(id,provider_id,manifest_code,origin_country_code,origin_city,cutoff_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6)`, manifestID, providerID, code, req.OriginCountry, req.OriginCity, req.CutoffAt); err != nil {
		return fiber.ErrInternalServerError
	}
	for _, orderID := range req.OrderIDs {
		tag, err := tx.Exec(c.Context(), `INSERT INTO merchant_manifest_orders(manifest_id,order_id)
			SELECT $1::uuid,o.id FROM orders o JOIN order_fulfillments f ON f.order_id=o.id
			WHERE o.id=$2::uuid AND f.provider_id=$3::uuid AND f.route='merchant_cross_border'
			AND f.owner='merchant' AND o.paid_at IS NOT NULL AND f.status IN ('pending','accepted','processing')
			AND NOT EXISTS(SELECT 1 FROM merchant_manifest_orders existing WHERE existing.order_id=o.id)`, manifestID, orderID, providerID)
		if err != nil || tag.RowsAffected() != 1 {
			return fiber.NewError(fiber.StatusConflict, "one or more orders are unavailable, unpaid, already manifested, or not owned by this provider")
		}
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": manifestID, "manifest_code": code, "status": "open", "version": 1})
}

func (m *ProviderMarketplaceController) ListMerchantManifests(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	limit := marketplaceLimit(c)
	cursorTime, cursorID, err := decodeMarketplaceCursor(c.Query("cursor"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid cursor")
	}
	status, search := strings.ToLower(strings.TrimSpace(c.Query("status"))), strings.TrimSpace(c.Query("search"))
	rows, err := m.db.Query(c.Context(), `SELECT m.id::text,m.manifest_code,m.origin_country_code,m.origin_city,m.status,m.cutoff_at,m.closed_at,m.dispatched_at,m.version,m.created_at,count(mo.order_id)
		FROM merchant_manifests m LEFT JOIN merchant_manifest_orders mo ON mo.manifest_id=m.id
		WHERE m.provider_id=$1::uuid AND ($2='' OR m.status=$2) AND ($3='' OR (m.manifest_code||' '||m.origin_city) ILIKE '%%'||$3||'%%')
		AND ($4::timestamptz IS NULL OR (m.created_at,m.id)<($4,$5::uuid))
		GROUP BY m.id ORDER BY m.created_at DESC,m.id DESC LIMIT $6`, providerID, status, search, cursorTime, cursorID, limit+1)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	items := []fiber.Map{}
	var lastCreated time.Time
	var lastID, next string
	for rows.Next() {
		var id, code, country, city, state string
		var cutoff, created time.Time
		var closed, dispatched *time.Time
		var version, count int64
		if err := rows.Scan(&id, &code, &country, &city, &state, &cutoff, &closed, &dispatched, &version, &created, &count); err != nil {
			return fiber.ErrInternalServerError
		}
		if len(items) == limit {
			next = encodeMarketplaceCursor(lastCreated, lastID)
			break
		}
		items = append(items, fiber.Map{"id": id, "manifest_code": code, "origin_country_code": country, "origin_city": city, "status": state, "cutoff_at": cutoff, "closed_at": closed, "dispatched_at": dispatched, "version": version, "order_count": count, "created_at": created})
		lastCreated, lastID = created, id
	}
	return c.JSON(fiber.Map{"items": items, "next_cursor": next, "has_more": next != ""})
}

func (m *ProviderMarketplaceController) GetMerchantManifest(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, _, err := m.providerForUser(c.Context(), userID)
	if err != nil {
		return fiber.ErrForbidden
	}
	var manifest fiber.Map
	var id, code, country, city, status string
	var cutoff, created time.Time
	var version int64
	if err = m.db.QueryRow(c.Context(), `SELECT id::text,manifest_code,origin_country_code,origin_city,status,cutoff_at,version,created_at FROM merchant_manifests WHERE id=$1::uuid AND provider_id=$2::uuid`, c.Params("manifest_id"), providerID).Scan(&id, &code, &country, &city, &status, &cutoff, &version, &created); err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	} else if err != nil {
		return fiber.ErrInternalServerError
	}
	manifest = fiber.Map{"id": id, "manifest_code": code, "origin_country_code": country, "origin_city": city, "status": status, "cutoff_at": cutoff, "version": version, "created_at": created}
	rows, err := m.db.Query(c.Context(), `SELECT o.id::text,COALESCE(o.package_label,''),o.total_amount,o.currency_code,o.created_at,u.full_name,u.email,COALESCE(u.phone,''),i.id::text,i.quantity,i.product_snapshot FROM merchant_manifest_orders mo JOIN orders o ON o.id=mo.order_id JOIN users u ON u.id=o.user_id JOIN order_items i ON i.order_id=o.id WHERE mo.manifest_id=$1::uuid ORDER BY o.created_at,o.id,i.id`, id)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()
	orders := []fiber.Map{}
	for rows.Next() {
		var oid, pkg, currency, buyer, email, phone, itemID string
		var total float64
		var ordered time.Time
		var qty int
		var snapshot []byte
		if err := rows.Scan(&oid, &pkg, &total, &currency, &ordered, &buyer, &email, &phone, &itemID, &qty, &snapshot); err != nil {
			return fiber.ErrInternalServerError
		}
		var product map[string]any
		_ = json.Unmarshal(snapshot, &product)
		orders = append(orders, fiber.Map{"order_id": oid, "package_code": pkg, "total_amount": total, "currency_code": currency, "ordered_at": ordered, "buyer": fiber.Map{"full_name": buyer, "email": email, "phone": phone}, "item_id": itemID, "quantity": qty, "product": product})
	}
	return c.JSON(fiber.Map{"manifest": manifest, "items": orders})
}

func (m *ProviderMarketplaceController) TransitionMerchantManifest(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	providerID, role, err := m.providerForUser(c.Context(), userID)
	if err != nil || (role != "owner" && role != "manager" && role != "staff") {
		return fiber.ErrForbidden
	}
	var req struct {
		Status          string `json:"status"`
		ExpectedVersion int64  `json:"expected_version"`
		IdempotencyKey  string `json:"idempotency_key"`
		Notes           string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.ErrBadRequest
	}
	req.Status = strings.ToLower(strings.TrimSpace(req.Status))
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.Status == "" || req.ExpectedVersion < 1 || req.IdempotencyKey == "" {
		return fiber.NewError(fiber.StatusUnprocessableEntity, "status, expected_version, and idempotency_key are required")
	}
	tx, err := m.db.Begin(c.Context())
	if err != nil {
		return fiber.ErrServiceUnavailable
	}
	defer tx.Rollback(c.Context())
	var id, current string
	var version int64
	if err = tx.QueryRow(c.Context(), `SELECT id::text,status,version FROM merchant_manifests WHERE id=$1::uuid AND provider_id=$2::uuid FOR UPDATE`, c.Params("manifest_id"), providerID).Scan(&id, &current, &version); err == pgx.ErrNoRows {
		return fiber.ErrNotFound
	} else if err != nil {
		return fiber.ErrInternalServerError
	}
	var replay string
	if err = tx.QueryRow(c.Context(), `SELECT status FROM merchant_manifest_events WHERE manifest_id=$1::uuid AND idempotency_key=$2`, id, req.IdempotencyKey).Scan(&replay); err == nil {
		return c.JSON(fiber.Map{"id": id, "status": replay, "version": version, "idempotent_replay": true})
	} else if err != pgx.ErrNoRows {
		return fiber.ErrInternalServerError
	}
	allowed := map[string]map[string]bool{"open": {"closed": true, "cancelled": true}, "closed": {"dispatched": true, "cancelled": true}, "dispatched": {"completed": true}}
	if version != req.ExpectedVersion || !allowed[current][req.Status] {
		return fiber.NewError(fiber.StatusConflict, "manifest changed or transition is not allowed")
	}
	if req.Status == "dispatched" {
		rows, queryErr := tx.Query(c.Context(), `SELECT f.id::text,f.order_id::text,o.user_id::text,f.status,f.version
			FROM merchant_manifest_orders mo
			JOIN order_fulfillments f ON f.order_id=mo.order_id
			JOIN orders o ON o.id=f.order_id
			WHERE mo.manifest_id=$1::uuid AND f.provider_id=$2::uuid AND f.route='merchant_cross_border' AND f.owner='merchant'
			ORDER BY f.order_id FOR UPDATE OF f`, id, providerID)
		if queryErr != nil {
			return fiber.ErrInternalServerError
		}
		type manifestFulfillment struct {
			id, orderID, buyerID, status string
			version                      int64
		}
		members := []manifestFulfillment{}
		for rows.Next() {
			var member manifestFulfillment
			if queryErr = rows.Scan(&member.id, &member.orderID, &member.buyerID, &member.status, &member.version); queryErr != nil {
				rows.Close()
				return fiber.ErrInternalServerError
			}
			if member.status != "processing" {
				rows.Close()
				return fiber.NewError(fiber.StatusConflict, "every manifest order must be in processing status before dispatch")
			}
			members = append(members, member)
		}
		rows.Close()
		if queryErr = rows.Err(); queryErr != nil || len(members) == 0 {
			return fiber.NewError(fiber.StatusConflict, "manifest has no dispatchable orders")
		}
		for _, member := range members {
			eventKey := "manifest-dispatch:" + id + ":" + member.orderID
			tag, updateErr := tx.Exec(c.Context(), `UPDATE order_fulfillments SET status='dispatched_from_origin',dispatched_at=COALESCE(dispatched_at,now()),version=version+1,updated_at=now() WHERE id=$1::uuid AND version=$2`, member.id, member.version)
			if updateErr != nil || tag.RowsAffected() != 1 {
				return fiber.NewError(fiber.StatusConflict, "a manifest order changed; refresh and retry")
			}
			if _, updateErr = tx.Exec(c.Context(), `UPDATE orders SET current_tracking_stage='Arrived at China Hub'::tracking_stage,order_status='Paid'::order_status,updated_at=now() WHERE id=$1::uuid`, member.orderID); updateErr != nil {
				return fiber.ErrInternalServerError
			}
			if _, updateErr = tx.Exec(c.Context(), `INSERT INTO fulfillment_events(fulfillment_id,actor_type,actor_id,previous_status,status,notes,idempotency_key) VALUES($1::uuid,'merchant',$2::uuid,$3,'dispatched_from_origin',$4,$5)`, member.id, userID, member.status, strings.TrimSpace(req.Notes), eventKey); updateErr != nil {
				return fiber.ErrInternalServerError
			}
			if _, updateErr = tx.Exec(c.Context(), `INSERT INTO tracking_events(order_id,stage,notes) VALUES($1::uuid,'Arrived at China Hub'::tracking_stage,$2)`, member.orderID, strings.TrimSpace(req.Notes)); updateErr != nil {
				return fiber.ErrInternalServerError
			}
			title, body := fulfillmentNotification("dispatched_from_origin")
			if updateErr = insertNotification(c.Context(), tx, member.buyerID, member.orderID, nil, "fulfillment_update", title, body, map[string]any{"order_id": member.orderID, "route": "merchant_cross_border", "status": "dispatched_from_origin", "manifest_id": id}, eventKey); updateErr != nil {
				return fiber.ErrInternalServerError
			}
		}
	}
	tag, err := tx.Exec(c.Context(), `UPDATE merchant_manifests SET status=$3,closed_at=CASE WHEN $3='closed' THEN COALESCE(closed_at,now()) ELSE closed_at END,dispatched_at=CASE WHEN $3='dispatched' THEN COALESCE(dispatched_at,now()) ELSE dispatched_at END,version=version+1,updated_at=now() WHERE id=$1::uuid AND version=$2`, id, version, req.Status)
	if err != nil || tag.RowsAffected() != 1 {
		return fiber.NewError(fiber.StatusConflict, "manifest changed; refresh and retry")
	}
	if _, err = tx.Exec(c.Context(), `INSERT INTO merchant_manifest_events(manifest_id,actor_id,previous_status,status,notes,idempotency_key) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6)`, id, userID, current, req.Status, strings.TrimSpace(req.Notes), req.IdempotencyKey); err != nil {
		return fiber.ErrInternalServerError
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"id": id, "status": req.Status, "version": version + 1})
}

// AdminListMerchantFulfillments gives operations staff one cursor-paginated
// oversight queue without exposing this list to merchants or buyers.
func (m *ProviderMarketplaceController) AdminListMerchantFulfillments(c *fiber.Ctx) error {
	limit := marketplaceLimit(c)
	cursorTime, cursorID, err := decodeMarketplaceCursor(c.Query("cursor"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid cursor")
	}
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	route := strings.ToLower(strings.TrimSpace(c.Query("route_type")))
	if route == "" {
		route = strings.ToLower(strings.TrimSpace(c.Query("route")))
	}
	owner := strings.ToLower(strings.TrimSpace(c.Query("owner")))
	search := strings.TrimSpace(c.Query("search"))

	rows, err := m.db.Query(c.Context(), `
		SELECT f.id::text, f.order_id::text, f.provider_id::text,
		       p.business_name, COALESCE(o.package_label,''), f.route, f.owner,
		       f.status, f.carrier, f.tracking_number, f.tracking_url,
		       f.current_location, f.estimated_delivery_at, f.version,
		       f.created_at, f.updated_at
		FROM order_fulfillments f
		JOIN orders o ON o.id=f.order_id
		JOIN provider_organizations p ON p.id=f.provider_id
		WHERE ($1='' OR f.status=$1)
		  AND ($2='' OR f.route=$2)
		  AND ($3='' OR f.owner=$3)
		  AND ($4='' OR (p.business_name||' '||COALESCE(o.package_label,'')||' '||COALESCE(f.tracking_number,'')) ILIKE '%%'||$4||'%%')
		  AND ($5::timestamptz IS NULL OR (f.created_at,f.id)<($5,$6::uuid))
		ORDER BY f.created_at DESC, f.id DESC
		LIMIT $7`, status, route, owner, search, cursorTime, cursorID, limit+1)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	defer rows.Close()

	items := make([]fiber.Map, 0, limit)
	var lastCreated time.Time
	var lastID, next string
	for rows.Next() {
		var id, orderID, providerID, businessName, packageCode string
		var fulfillmentRoute, fulfillmentOwner, fulfillmentStatus string
		var carrier, trackingNumber, trackingURL, location *string
		var estimated *time.Time
		var version int64
		var created, updated time.Time
		if err := rows.Scan(&id, &orderID, &providerID, &businessName, &packageCode,
			&fulfillmentRoute, &fulfillmentOwner, &fulfillmentStatus, &carrier,
			&trackingNumber, &trackingURL, &location, &estimated, &version,
			&created, &updated); err != nil {
			return fiber.ErrInternalServerError
		}
		if len(items) == limit {
			next = encodeMarketplaceCursor(lastCreated, lastID)
			break
		}
		items = append(items, fiber.Map{
			"id": id, "order_id": orderID, "provider_id": providerID,
			"provider_name": businessName, "package_code": packageCode, "package_label": packageCode,
			"route": fulfillmentRoute, "route_type": fulfillmentRoute, "owner": fulfillmentOwner, "fulfillment_owner": fulfillmentOwner,
			"status": fulfillmentStatus, "carrier": carrier,
			"tracking_number": trackingNumber, "tracking_url": trackingURL,
			"current_location": location, "estimated_delivery_at": estimated,
			"version": version, "created_at": created, "updated_at": updated,
		})
		lastCreated, lastID = created, id
	}
	if err := rows.Err(); err != nil {
		return fiber.ErrInternalServerError
	}
	return c.JSON(fiber.Map{"items": items, "next_cursor": next, "has_more": next != "", "page": fiber.Map{"next_cursor": next, "has_more": next != ""}})
}
