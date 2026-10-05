package controllers

import (
	"github.com/google/uuid"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SupportController struct {
	db *pgxpool.Pool
}

func NewSupportController(db *pgxpool.Pool) *SupportController {
	return &SupportController{db: db}
}

// CreateTicket - User creates a support ticket
func (s *SupportController) CreateTicket(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	var req struct {
		Subject string `json:"subject"`
		Message string `json:"message"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request")
	}
	req.Subject = strings.TrimSpace(req.Subject)
	req.Message = strings.TrimSpace(req.Message)
	if req.Subject == "" || req.Message == "" {
		return fiber.NewError(fiber.StatusBadRequest, "subject and message required")
	}
	if len(req.Subject) > 200 || len(req.Message) > 5000 {
		return fiber.NewError(fiber.StatusBadRequest, "subject or message is too long")
	}

	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to create ticket")
	}
	defer tx.Rollback(c.Context())
	var ticketID string
	err = tx.QueryRow(c.Context(), `
		INSERT INTO support_tickets(user_id, subject, message)
		VALUES ($1, $2, $3)
		RETURNING id
	`, userID, req.Subject, req.Message).Scan(&ticketID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to create ticket")
	}

	// Add initial message
	_, err = tx.Exec(c.Context(), `
		INSERT INTO support_messages(ticket_id, sender_type, sender_id, message)
		VALUES ($1, 'user', $2, $3)
	`, ticketID, userID, req.Message)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to save message")
	}
	if err := tx.Commit(c.Context()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to create ticket")
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"ticket_id": ticketID,
		"message":   "Support ticket created",
	})
}

// ListMyTickets - User lists their own tickets
func (s *SupportController) ListMyTickets(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	limit := c.QueryInt("limit", 20)
	if limit < 1 || limit > 100 {
		return fiber.NewError(400, "limit must be between 1 and 100")
	}
	var before time.Time
	beforeID := ""
	if cursor := c.Query("cursor"); cursor != "" {
		parts := strings.Split(cursor, "|")
		if len(parts) != 2 {
			return fiber.NewError(400, "invalid ticket cursor")
		}
		var err error
		before, err = time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return fiber.NewError(400, "invalid ticket cursor")
		}
		if _, err = uuid.Parse(parts[1]); err != nil {
			return fiber.NewError(400, "invalid ticket cursor")
		}
		beforeID = parts[1]
	}
	rows, err := s.db.Query(c.Context(), `
		SELECT id, subject, message, status, created_at, updated_at
		FROM support_tickets
		WHERE user_id = $1 AND ($2::text='' OR (created_at,id)<($3::timestamptz,NULLIF($2,'')::uuid))
		ORDER BY created_at DESC,id DESC
		LIMIT $4
	`, userID, beforeID, before, limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "query failed")
	}
	defer rows.Close()

	tickets := make([]fiber.Map, 0)
	for rows.Next() {
		var id, subject, message, status string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &subject, &message, &status, &createdAt, &updatedAt); err != nil {
			return fiber.NewError(500, "ticket history unavailable")
		}
		tickets = append(tickets, fiber.Map{
			"id":         id,
			"subject":    subject,
			"message":    message,
			"status":     status,
			"created_at": createdAt,
			"updated_at": updatedAt,
		})
	}
	if rows.Err() != nil {
		return fiber.NewError(500, "ticket history unavailable")
	}
	next := ""
	if len(tickets) > limit {
		tickets = tickets[:limit]
		last := tickets[len(tickets)-1]
		next = last["created_at"].(time.Time).Format(time.RFC3339Nano) + "|" + last["id"].(string)
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"tickets": tickets, "next_cursor": next, "has_more": next != ""})
}

// GetTicketMessages - Get messages for a ticket
func (s *SupportController) GetTicketMessages(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	ticketID := c.Params("ticket_id")

	// Verify ownership
	var ownerID string
	err := s.db.QueryRow(c.Context(), `
		SELECT user_id FROM support_tickets WHERE id = $1
	`, ticketID).Scan(&ownerID)
	if err != nil || ownerID != userID {
		return fiber.NewError(fiber.StatusNotFound, "ticket not found")
	}

	return s.messagePage(c, ticketID)
}

// UserReply adds a buyer reply to an existing open support conversation.
func (s *SupportController) UserReply(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	ticketID := c.Params("ticket_id")
	var req struct {
		Message string `json:"message"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request")
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" || len(req.Message) > 5000 {
		return fiber.NewError(fiber.StatusBadRequest, "message must be between 1 and 5000 characters")
	}
	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to send reply")
	}
	defer tx.Rollback(c.Context())
	var status string
	if err = tx.QueryRow(c.Context(), `SELECT status FROM support_tickets WHERE id=$1::uuid AND user_id=$2::uuid FOR UPDATE`, ticketID, userID).Scan(&status); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "ticket not found")
	}
	if status == "closed" {
		return fiber.NewError(fiber.StatusConflict, "ticket is closed")
	}
	if _, err = tx.Exec(c.Context(), `INSERT INTO support_messages(ticket_id,sender_type,sender_id,message) VALUES($1::uuid,'user',$2::uuid,$3)`, ticketID, userID, req.Message); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to save reply")
	}
	if _, err = tx.Exec(c.Context(), `UPDATE support_tickets SET status='open',updated_at=now() WHERE id=$1::uuid`, ticketID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to update ticket")
	}
	if err = tx.Commit(c.Context()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to send reply")
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"message": "Reply sent"})
}

// AdminGetTicketMessages returns a complete ticket conversation to authorized support admins.
func (s *SupportController) AdminGetTicketMessages(c *fiber.Ctx) error {
	ticketID := c.Params("ticket_id")
	return s.messagePage(c, ticketID)
}

// AdminListTickets - Admin lists all open tickets
func (s *SupportController) AdminListTickets(c *fiber.Ctx) error {
	rows, err := s.db.Query(c.Context(), `
		SELECT st.id, st.subject, st.message, st.status, u.email, st.created_at, st.updated_at
		FROM support_tickets st
		JOIN users u ON u.id = st.user_id
		ORDER BY st.status = 'open' DESC, st.created_at DESC
		LIMIT 50
	`)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "query failed")
	}
	defer rows.Close()

	tickets := make([]fiber.Map, 0)
	for rows.Next() {
		var id, subject, message, status, email string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &subject, &message, &status, &email, &createdAt, &updatedAt); err != nil {
			continue
		}
		tickets = append(tickets, fiber.Map{
			"id":         id,
			"subject":    subject,
			"message":    message,
			"status":     status,
			"user_email": email,
			"created_at": createdAt,
			"updated_at": updatedAt,
		})
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"tickets": tickets})
}

// AdminReply - Admin replies to a ticket
func (s *SupportController) AdminReply(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)
	ticketID := c.Params("ticket_id")

	var req struct {
		Message string `json:"message"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request")
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		return fiber.NewError(fiber.StatusBadRequest, "message required")
	}
	if len(req.Message) > 5000 {
		return fiber.NewError(fiber.StatusBadRequest, "message is too long")
	}

	tx, err := s.db.Begin(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to send reply")
	}
	defer tx.Rollback(c.Context())

	// Lock the ticket so concurrent replies and closure cannot produce a partial state.
	var userID, status string
	err = tx.QueryRow(c.Context(), `
		SELECT user_id, status FROM support_tickets WHERE id = $1 FOR UPDATE
	`, ticketID).Scan(&userID, &status)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "ticket not found")
	}
	if status == "closed" {
		return fiber.NewError(fiber.StatusConflict, "ticket is closed")
	}

	// Add admin message
	_, err = tx.Exec(c.Context(), `
		INSERT INTO support_messages(ticket_id, sender_type, sender_id, message)
		VALUES ($1, 'admin', $2, $3)
	`, ticketID, adminID, req.Message)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to save reply")
	}

	// Update ticket status
	_, err = tx.Exec(c.Context(), `
		UPDATE support_tickets SET status = 'responded', updated_at = now()
		WHERE id = $1 AND status <> 'closed'
	`, ticketID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to update ticket")
	}
	if err := tx.Commit(c.Context()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to send reply")
	}

	// Notify the user
	CreateNotification(c.Context(), s.db, userID, "", nil, "ticket_reply", "Support Ticket Updated", "An admin has replied to your support ticket.", nil)

	return c.JSON(fiber.Map{"message": "Reply sent"})
}

// AdminCloseTicket - Admin closes a ticket
func (s *SupportController) AdminCloseTicket(c *fiber.Ctx) error {
	ticketID := c.Params("ticket_id")

	_, err := s.db.Exec(c.Context(), `
		UPDATE support_tickets SET status = 'closed', updated_at = now()
		WHERE id = $1
	`, ticketID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to close ticket")
	}

	return c.JSON(fiber.Map{"message": "Ticket closed"})
}

// Bounded keyset pages load recent conversation first. IDs break timestamp ties.
func (s *SupportController) messagePage(c *fiber.Ctx, ticketID string) error {
	var ticketStatus string
	if err := s.db.QueryRow(c.Context(), `SELECT status FROM support_tickets WHERE id=$1`, ticketID).Scan(&ticketStatus); err != nil {
		return fiber.NewError(404, "ticket not found")
	}
	limit := c.QueryInt("limit", 50)
	if limit < 1 || limit > 100 {
		return fiber.NewError(400, "limit must be between 1 and 100")
	}
	var before time.Time
	var beforeID string
	if cursor := c.Query("cursor"); cursor != "" {
		parts := strings.Split(cursor, "|")
		if len(parts) != 2 {
			return fiber.NewError(400, "invalid message cursor")
		}
		var err error
		before, err = time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return fiber.NewError(400, "invalid message cursor")
		}
		if _, err = uuid.Parse(parts[1]); err != nil {
			return fiber.NewError(400, "invalid message cursor")
		}
		beforeID = parts[1]
	}
	rows, err := s.db.Query(c.Context(), `SELECT id::text,sender_type,sender_id,message,created_at FROM support_messages
 WHERE ticket_id=$1 AND ($2::text='' OR (created_at,id)<($3::timestamptz,NULLIF($2,'')::uuid))
 ORDER BY created_at DESC,id DESC LIMIT $4`, ticketID, beforeID, before, limit+1)
	if err != nil {
		return fiber.NewError(500, "conversation unavailable")
	}
	defer rows.Close()
	messages := make([]fiber.Map, 0, limit+1)
	for rows.Next() {
		var id, kind, sender, body string
		var created time.Time
		if err = rows.Scan(&id, &kind, &sender, &body, &created); err != nil {
			return fiber.NewError(500, "conversation unavailable")
		}
		messages = append(messages, fiber.Map{"id": id, "sender_type": kind, "sender_id": sender, "message": body, "created_at": created})
	}
	if rows.Err() != nil {
		return fiber.NewError(500, "conversation unavailable")
	}
	next := ""
	if len(messages) > limit {
		messages = messages[:limit]
		last := messages[len(messages)-1]
		next = last["created_at"].(time.Time).Format(time.RFC3339Nano) + "|" + last["id"].(string)
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"messages": messages, "next_cursor": next, "has_more": next != "", "ticket_status": ticketStatus})
}
