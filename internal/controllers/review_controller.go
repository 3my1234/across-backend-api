package controllers

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReviewController struct {
	db *pgxpool.Pool
}

func NewReviewController(db *pgxpool.Pool) *ReviewController {
	return &ReviewController{db: db}
}

func (rc *ReviewController) ListProductReviews(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	productID := c.Params("product_id")
	userID, _ := c.Locals("user_id").(string)
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}

	rows, err := rc.db.Query(c.Context(), `
		SELECT r.id, r.rating, r.review_text, r.media_urls, r.created_at,
			COALESCE(NULLIF(u.full_name, ''), 'Verified buyer'),
			(r.user_id::text = $2)
		FROM reviews r
		JOIN users u ON u.id = r.user_id
		WHERE r.product_id = $1
		  AND ($3::timestamptz IS NULL OR (r.created_at, r.id) < ($3, $4::uuid))
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $5
	`, productID, userID, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "reviews unavailable")
	}
	defer rows.Close()

	reviews := make([]fiber.Map, 0, page.Limit+1)
	for rows.Next() {
		var id, reviewText, author string
		var mediaURLs []string
		var rating int
		var createdAt time.Time
		var isMine bool
		if err := rows.Scan(&id, &rating, &reviewText, &mediaURLs, &createdAt, &author, &isMine); err != nil {
			return err
		}
		reviews = append(reviews, fiber.Map{
			"id":          id,
			"rating":      rating,
			"review_text": reviewText,
			"media_urls":  mediaURLs,
			"created_at":  createdAt,
			"author":      author,
			"is_mine":     isMine,
		})
	}

	nextCursor := ""
	if len(reviews) > page.Limit {
		reviews = reviews[:page.Limit]
		last := reviews[len(reviews)-1]
		nextCursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	total := int64(0)
	payload := fiber.Map{
		"reviews": reviews,
		"page":    adminPageMeta(page, total, len(reviews), nextCursor),
	}
	// Aggregates are maintained transactionally on products, so the summary is
	// constant-time even when a product has millions of reviews.
	if page.CursorTime == nil {
		var average float64
		if err := rc.db.QueryRow(c.Context(), `
			SELECT review_count,
				CASE WHEN review_count > 0 THEN review_rating_sum::float8 / review_count ELSE 0 END
			FROM products WHERE id = $1 AND is_active = true
		`, productID).Scan(&total, &average); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "review summary unavailable")
		}
		payload["summary"] = fiber.Map{"count": total, "average_rating": average}
		payload["page"] = adminPageMeta(page, total, len(reviews), nextCursor)
	}
	return c.JSON(payload)
}

func (rc *ReviewController) UpsertProductReview(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	productID := c.Params("product_id")
	var req struct {
		Rating     int      `json:"rating"`
		ReviewText string   `json:"review_text"`
		MediaURLs  []string `json:"media_urls"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid payload")
	}
	if req.Rating < 1 || req.Rating > 5 {
		return fiber.NewError(fiber.StatusBadRequest, "rating must be between 1 and 5")
	}
	req.ReviewText = strings.TrimSpace(req.ReviewText)
	if req.MediaURLs == nil {
		req.MediaURLs = []string{}
	}

	var productActive bool
	if err := rc.db.QueryRow(c.Context(), `
		SELECT is_active FROM products WHERE id = $1
	`, productID).Scan(&productActive); err != nil || !productActive {
		return fiber.NewError(fiber.StatusNotFound, "product not found")
	}

	var orderID string
	err := rc.db.QueryRow(c.Context(), `
		SELECT o.id
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		WHERE o.user_id = $1
			AND oi.product_id = $2
			AND o.order_status IN ('Delivered', 'Completed')
		ORDER BY o.created_at DESC
		LIMIT 1
	`, userID, productID).Scan(&orderID)
	if err != nil {
		return fiber.NewError(fiber.StatusForbidden, "you can review this product after delivery")
	}

	var reviewID string
	err = rc.db.QueryRow(c.Context(), `
		SELECT id FROM reviews
		WHERE product_id = $1 AND user_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, productID, userID).Scan(&reviewID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fiber.NewError(fiber.StatusInternalServerError, "review unavailable")
	}
	created := errors.Is(err, pgx.ErrNoRows)
	if created {
		err = rc.db.QueryRow(c.Context(), `
			INSERT INTO reviews(product_id, user_id, order_id, rating, review_text, media_urls)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id
		`, productID, userID, orderID, req.Rating, req.ReviewText, req.MediaURLs).Scan(&reviewID)
		if err != nil {
			return fiber.NewError(fiber.StatusConflict, "review could not be created")
		}
	} else {
		_, err = rc.db.Exec(c.Context(), `
			UPDATE reviews
			SET rating = $3, review_text = $4, media_urls = $5, order_id = COALESCE(order_id, $6)
			WHERE id = $1 AND user_id = $2
		`, reviewID, userID, req.Rating, req.ReviewText, req.MediaURLs, orderID)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "review could not be updated")
		}
	}

	rewardClaimed, rewardErr := creditReviewReward(c.Context(), rc.db, userID, orderID)
	if rewardErr != nil {
		// The review is already durable. A secondary reward failure must never
		// make the client retry or pretend the review was not saved.
		log.Printf("review reward failed review_id=%s user_id=%s: %v", reviewID, userID, rewardErr)
	}
	return rc.sendReviewMutation(c, reviewID, userID, created, rewardClaimed, rewardErr != nil)
}

func (rc *ReviewController) MyProductReview(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "private, no-store")
	userID := c.Locals("user_id").(string)
	productID := c.Params("product_id")
	var id, reviewText string
	var mediaURLs []string
	var rating int
	var createdAt time.Time
	err := rc.db.QueryRow(c.Context(), `
		SELECT id, rating, review_text, media_urls, created_at
		FROM reviews
		WHERE product_id = $1 AND user_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, productID, userID).Scan(&id, &rating, &reviewText, &mediaURLs, &createdAt)
	if err != nil {
		return c.JSON(fiber.Map{"review": nil, "can_review": rc.canReview(c, userID, productID)})
	}
	return c.JSON(fiber.Map{
		"review": fiber.Map{
			"id":          id,
			"rating":      rating,
			"review_text": reviewText,
			"media_urls":  mediaURLs,
			"created_at":  createdAt,
		},
		"can_review": true,
	})
}

func (rc *ReviewController) sendReviewMutation(c *fiber.Ctx, reviewID, userID string, created, rewardClaimed, rewardPending bool) error {
	var reviewText, author string
	var mediaURLs []string
	var rating int
	var createdAt time.Time
	var reviewCount int64
	var averageRating float64
	if err := rc.db.QueryRow(c.Context(), `
		SELECT r.rating, r.review_text, r.media_urls, r.created_at,
			COALESCE(NULLIF(u.full_name, ''), 'Verified buyer'), p.review_count,
			CASE WHEN p.review_count > 0 THEN p.review_rating_sum::float8 / p.review_count ELSE 0 END
		FROM reviews r
		JOIN users u ON u.id = r.user_id
		JOIN products p ON p.id = r.product_id
		WHERE r.id = $1 AND r.user_id = $2
	`, reviewID, userID).Scan(&rating, &reviewText, &mediaURLs, &createdAt, &author, &reviewCount, &averageRating); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "review saved but response could not be loaded")
	}
	status := fiber.StatusOK
	if created {
		status = fiber.StatusCreated
	}
	return c.Status(status).JSON(fiber.Map{
		"created":               created,
		"review_reward_claimed": rewardClaimed,
		"reward_pending":        rewardPending,
		"review": fiber.Map{
			"id": reviewID, "rating": rating, "review_text": reviewText,
			"media_urls": mediaURLs, "created_at": createdAt, "author": author, "is_mine": true,
		},
		"summary": fiber.Map{"count": reviewCount, "average_rating": averageRating},
	})
}

func (rc *ReviewController) canReview(c *fiber.Ctx, userID, productID string) bool {
	var ok bool
	_ = rc.db.QueryRow(c.Context(), `
		SELECT EXISTS (
			SELECT 1
			FROM orders o
			JOIN order_items oi ON oi.order_id = o.id
			WHERE o.user_id = $1
				AND oi.product_id = $2
				AND o.order_status IN ('Delivered', 'Completed')
		)
	`, userID, productID).Scan(&ok)
	return ok
}
