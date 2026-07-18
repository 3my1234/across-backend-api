package controllers

import (
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
	if err := c.BodyParser(&req); err != nil || req.Subject == "" || req.Message == "" {
		return fiber.NewError(fiber.StatusBadRequest, "subject and message required")
	}

	var ticketID string
	err := s.db.QueryRow(c.Context(), `
		INSERT INTO support_tickets(user_id, subject, message)
		VALUES ($1, $2, $3)
		RETURNING id
	`, userID, req.Subject, req.Message).Scan(&ticketID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to create ticket")
	}

	// Add initial message
	_, err = s.db.Exec(c.Context(), `
		INSERT INTO support_messages(ticket_id, sender_type, sender_id, message)
		VALUES ($1, 'user', $2, $3)
	`, ticketID, userID, req.Message)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to save message")
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"ticket_id": ticketID,
		"message":   "Support ticket created",
	})
}

// ListMyTickets - User lists their own tickets
func (s *SupportController) ListMyTickets(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	rows, err := s.db.Query(c.Context(), `
		SELECT id, subject, message, status, created_at, updated_at
		FROM support_tickets
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 20
	`, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "query failed")
	}
	defer rows.Close()

	tickets := make([]fiber.Map, 0)
	for rows.Next() {
		var id, subject, message, status string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &subject, &message, &status, &createdAt, &updatedAt); err != nil {
			continue
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
	return c.JSON(fiber.Map{"tickets": tickets})
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

	rows, err := s.db.Query(c.Context(), `
		SELECT sender_type, sender_id, message, created_at
		FROM support_messages
		WHERE ticket_id = $1
		ORDER BY created_at ASC
	`, ticketID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "query failed")
	}
	defer rows.Close()

	messages := make([]fiber.Map, 0)
	for rows.Next() {
		var senderType, senderID, message string
		var createdAt time.Time
		if err := rows.Scan(&senderType, &senderID, &message, &createdAt); err != nil {
			continue
		}
		messages = append(messages, fiber.Map{
			"sender_type": senderType,
			"sender_id":   senderID,
			"message":     message,
			"created_at":  createdAt,
		})
	}
	return c.JSON(fiber.Map{"messages": messages})
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
	return c.JSON(fiber.Map{"tickets": tickets})
}

// AdminReply - Admin replies to a ticket
func (s *SupportController) AdminReply(c *fiber.Ctx) error {
	adminID, _ := c.Locals("admin_id").(string)
	ticketID := c.Params("ticket_id")

	var req struct {
		Message string `json:"message"`
	}
	if err := c.BodyParser(&req); err != nil || req.Message == "" {
		return fiber.NewError(fiber.StatusBadRequest, "message required")
	}

	// Get the user_id for this ticket
	var userID string
	err := s.db.QueryRow(c.Context(), `
		SELECT user_id FROM support_tickets WHERE id = $1
	`, ticketID).Scan(&userID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "ticket not found")
	}

	// Add admin message
	_, err = s.db.Exec(c.Context(), `
		INSERT INTO support_messages(ticket_id, sender_type, sender_id, message)
		VALUES ($1, 'admin', $2, $3)
	`, ticketID, adminID, req.Message)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to save reply")
	}

	// Update ticket status
	_, err = s.db.Exec(c.Context(), `
		UPDATE support_tickets SET status = 'responded', updated_at = now()
		WHERE id = $1 AND status = 'open'
	`, ticketID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to update ticket")
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
