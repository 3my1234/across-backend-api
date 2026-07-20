package controllers

import (
	"database/sql"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProfileController struct {
	db *pgxpool.Pool
}

func NewProfileController(db *pgxpool.Pool) *ProfileController {
	return &ProfileController{db: db}
}

// GetProfile returns user profile with all fields
func (p *ProfileController) GetProfile(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	var fullName, email, phone, avatarURL, region, address, city, state, postalCode string
	var dateOfBirth sql.NullTime
	err := p.db.QueryRow(c.Context(), `
		SELECT full_name, COALESCE(email, ''), COALESCE(phone, ''), COALESCE(avatar_url, ''), COALESCE(region, ''), COALESCE(address, ''), COALESCE(city, ''), COALESCE(state, ''), COALESCE(postal_code, ''), date_of_birth
		FROM users WHERE id = $1
	`, userID).Scan(&fullName, &email, &phone, &avatarURL, &region, &address, &city, &state, &postalCode, &dateOfBirth)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "user not found")
	}

	dob := ""
	if dateOfBirth.Valid {
		dob = dateOfBirth.Time.Format("2006-01-02")
	}

	return c.JSON(fiber.Map{
		"full_name":     fullName,
		"email":         email,
		"phone":         phone,
		"avatar_url":    avatarURL,
		"region":        region,
		"address":       address,
		"city":          city,
		"state":         state,
		"postal_code":   postalCode,
		"date_of_birth": dob,
	})
}

// UpdateProfile updates user profile fields
func (p *ProfileController) UpdateProfile(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)

	var req struct {
		FullName    string `json:"full_name"`
		Phone       string `json:"phone"`
		Region      string `json:"region"`
		Address     string `json:"address"`
		City        string `json:"city"`
		State       string `json:"state"`
		PostalCode  string `json:"postal_code"`
		DateOfBirth string `json:"date_of_birth"` // "2006-01-02" format
		AvatarURL   string `json:"avatar_url"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}

	var dob *time.Time
	if req.DateOfBirth != "" {
		parsed, err := time.Parse("2006-01-02", req.DateOfBirth)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid date format, use YYYY-MM-DD")
		}
		dob = &parsed
	}

	_, err := p.db.Exec(c.Context(), `
		UPDATE users SET
			full_name = COALESCE(NULLIF($2, ''), full_name),
			phone = COALESCE(NULLIF($3, ''), phone),
			region = COALESCE(NULLIF($4, ''), region),
			avatar_url = COALESCE(NULLIF($5, ''), avatar_url),
			address = COALESCE(NULLIF($6, ''), address),
			city = COALESCE(NULLIF($7, ''), city),
			state = COALESCE(NULLIF($8, ''), state),
			postal_code = COALESCE(NULLIF($9, ''), postal_code),
			date_of_birth = CASE WHEN $10::date IS NULL THEN date_of_birth ELSE $10::date END,
			updated_at = now()
		WHERE id = $1
	`, userID, req.FullName, req.Phone, req.Region, req.AvatarURL, req.Address, req.City, req.State, req.PostalCode, dob)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "update failed")
	}

	return c.JSON(fiber.Map{"message": "Profile updated"})
}
