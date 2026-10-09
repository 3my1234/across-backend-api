package controllers

import (
	"bytes"
	"encoding/csv"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/mail"
	"strings"
	"time"
)

type WaitlistController struct{ db *pgxpool.Pool }

func NewWaitlistController(db *pgxpool.Pool) *WaitlistController { return &WaitlistController{db: db} }

func (w *WaitlistController) Join(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Phone    string `json:"phone"`
		Interest string `json:"interest"`
		Source   string `json:"source"`
		Consent  bool   `json:"consent"`
		Website  string `json:"website"`
	}
	if len(c.Body()) > 4096 || c.BodyParser(&req) != nil {
		return fiber.NewError(400, "Please check your details")
	}
	if req.Website != "" {
		return c.JSON(fiber.Map{"message": "You're on the list. We'll email you when Atlantic Express launches."})
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Name = strings.TrimSpace(req.Name)
	req.Phone = strings.TrimSpace(req.Phone)
	req.Source = strings.TrimSpace(req.Source)
	parsed, err := mail.ParseAddress(req.Email)
	if err != nil || parsed.Address != req.Email || len(req.Email) > 254 || !strings.Contains(req.Email, ".") || !req.Consent {
		return fiber.NewError(400, "Enter a valid email and agree to receive launch updates")
	}
	if len(req.Name) > 120 || len(req.Phone) > 30 || len(req.Source) > 100 {
		return fiber.NewError(400, "Please shorten your name, phone or source")
	}
	if req.Interest == "" {
		req.Interest = "exploring"
	}
	if req.Source == "" {
		req.Source = "website"
	}
	if req.Interest != "exploring" && req.Interest != "buyer" && req.Interest != "seller" && req.Interest != "artisan" {
		return fiber.NewError(400, "Choose what interests you")
	}
	_, err = w.db.Exec(c.Context(), `INSERT INTO launch_waitlist(email,full_name,phone,interest,source) VALUES($1,$2,$3,$4,$5) ON CONFLICT(email) DO NOTHING`, req.Email, req.Name, req.Phone, req.Interest, req.Source)
	if err != nil {
		return fiber.NewError(503, "We couldn't save your signup. Please try again shortly.")
	}
	// Same response for new and existing emails. No contact information is public.
	return c.JSON(fiber.Map{"message": "You're on the list. We'll email you when Atlantic Express launches."})
}

func csvSafe(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + s
	}
	return s
}

func (w *WaitlistController) AdminList(c *fiber.Ctx) error {
	var total int
	if err := w.db.QueryRow(c.Context(), `SELECT COUNT(*) FROM launch_waitlist`).Scan(&total); err != nil {
		return err
	}
	rows, err := w.db.Query(c.Context(), `SELECT email,full_name,phone,interest,source,created_at FROM launch_waitlist ORDER BY created_at DESC,id DESC LIMIT 100`)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []fiber.Map{}
	for rows.Next() {
		var email, name, phone, interest, source string
		var created time.Time
		if err = rows.Scan(&email, &name, &phone, &interest, &source, &created); err != nil {
			return err
		}
		items = append(items, fiber.Map{"email": email, "name": name, "phone": phone, "interest": interest, "source": source, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	c.Set("Cache-Control", "private, no-store")
	return c.JSON(fiber.Map{"items": items, "total": total})
}

func (w *WaitlistController) AdminExport(c *fiber.Ctx) error {
	rows, err := w.db.Query(c.Context(), `SELECT email,full_name,phone,interest,source,consent_version,consented_at FROM launch_waitlist ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err = writer.Write([]string{"Email", "Name", "Phone", "Interest", "Source", "Consent version", "Consent date (UTC)"}); err != nil {
		return err
	}
	for rows.Next() {
		var email, name, phone, interest, source, consent string
		var created time.Time
		if err = rows.Scan(&email, &name, &phone, &interest, &source, &consent, &created); err != nil {
			return err
		}
		if err = writer.Write([]string{csvSafe(email), csvSafe(name), csvSafe(phone), interest, csvSafe(source), consent, created.UTC().Format(time.RFC3339)}); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return err
	}
	c.Set("Cache-Control", "private, no-store")
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="atlantic-express-waitlist.csv"`)
	return c.Send(buffer.Bytes())
}
