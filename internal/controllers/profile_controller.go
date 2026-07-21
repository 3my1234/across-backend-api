package controllers

import (
	"database/sql"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProfileController struct {
	db *pgxpool.Pool
}

type profileResponse struct {
	FullName    string `json:"full_name"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	AvatarURL   string `json:"avatar_url"`
	Region      string `json:"region"`
	Address     string `json:"address"`
	City        string `json:"city"`
	State       string `json:"state"`
	PostalCode  string `json:"postal_code"`
	DateOfBirth string `json:"date_of_birth"`
}

func NewProfileController(db *pgxpool.Pool) *ProfileController {
	return &ProfileController{db: db}
}

func (p *ProfileController) GetProfile(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	profile, err := p.readProfile(c, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "user profile not found")
	}
	return c.JSON(profile)
}

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
		DateOfBirth string `json:"date_of_birth"`
		AvatarURL   string `json:"avatar_url"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid profile payload")
	}

	req.FullName = strings.TrimSpace(req.FullName)
	req.Phone = strings.TrimSpace(req.Phone)
	req.Region = strings.TrimSpace(req.Region)
	req.Address = strings.TrimSpace(req.Address)
	req.City = strings.TrimSpace(req.City)
	req.State = strings.TrimSpace(req.State)
	req.PostalCode = strings.TrimSpace(req.PostalCode)
	req.DateOfBirth = strings.TrimSpace(req.DateOfBirth)
	req.AvatarURL = strings.TrimSpace(req.AvatarURL)
	if len(req.FullName) > 150 || len(req.Phone) > 32 || len(req.Region) > 100 || len(req.Address) > 300 || len(req.City) > 100 || len(req.State) > 100 || len(req.PostalCode) > 24 {
		return fiber.NewError(fiber.StatusBadRequest, "one or more profile fields are too long")
	}
	if req.AvatarURL != "" {
		parsed, err := url.ParseRequestURI(req.AvatarURL)
		if err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
			return fiber.NewError(fiber.StatusBadRequest, "avatar must be uploaded before saving")
		}
	}

	var dob *time.Time
	if req.DateOfBirth != "" {
		parsed, err := time.Parse("2006-01-02", req.DateOfBirth)
		if err != nil || parsed.After(time.Now()) {
			return fiber.NewError(fiber.StatusBadRequest, "invalid date of birth; use YYYY-MM-DD")
		}
		dob = &parsed
	}

	tag, err := p.db.Exec(c.Context(), `
		UPDATE users SET
			full_name = COALESCE(NULLIF($2, ''), full_name),
			phone = COALESCE(NULLIF($3, ''), phone),
			region = COALESCE(NULLIF($4, ''), region),
			avatar_url = COALESCE(NULLIF($5, ''), avatar_url),
			address = COALESCE(NULLIF($6, ''), address),
			city = COALESCE(NULLIF($7, ''), city),
			state = COALESCE(NULLIF($8, ''), state),
			postal_code = COALESCE(NULLIF($9, ''), postal_code),
			date_of_birth = COALESCE($10::date, date_of_birth),
			updated_at = now()
		WHERE id = $1
	`, userID, req.FullName, req.Phone, req.Region, req.AvatarURL, req.Address, req.City, req.State, req.PostalCode, dob)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fiber.NewError(fiber.StatusConflict, "phone number is already linked to another account")
		}
		log.Printf("profile update failed user_id=%s: %v", userID, err)
		return fiber.NewError(fiber.StatusInternalServerError, "profile could not be saved")
	}
	if tag.RowsAffected() == 0 {
		return fiber.NewError(fiber.StatusNotFound, "user profile not found")
	}

	profile, err := p.readProfile(c, userID)
	if err != nil {
		log.Printf("profile reload failed user_id=%s: %v", userID, err)
		return fiber.NewError(fiber.StatusInternalServerError, "profile saved but could not be reloaded")
	}
	return c.JSON(fiber.Map{"message": "Profile updated", "profile": profile})
}

func (p *ProfileController) readProfile(c *fiber.Ctx, userID string) (profileResponse, error) {
	var profile profileResponse
	var dateOfBirth sql.NullTime
	err := p.db.QueryRow(c.Context(), `
		SELECT full_name, COALESCE(email, ''), COALESCE(phone, ''), COALESCE(avatar_url, ''),
			COALESCE(region, ''), COALESCE(address, ''), COALESCE(city, ''), COALESCE(state, ''),
			COALESCE(postal_code, ''), date_of_birth
		FROM users
		WHERE id = $1
	`, userID).Scan(&profile.FullName, &profile.Email, &profile.Phone, &profile.AvatarURL, &profile.Region, &profile.Address, &profile.City, &profile.State, &profile.PostalCode, &dateOfBirth)
	if dateOfBirth.Valid {
		profile.DateOfBirth = dateOfBirth.Time.Format("2006-01-02")
	}
	return profile, err
}
