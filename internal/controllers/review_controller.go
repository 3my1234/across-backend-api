package controllers

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReviewController struct {
	db *pgxpool.Pool
}

func NewReviewController(db *pgxpool.Pool) *ReviewController {
	return &ReviewController{db: db}
}

func (rc *ReviewController) ListProductReviews(c *fiber.Ctx) error {
	productID := c.Params("product_id")
	userID, _ := c.Locals("user_id").(string)

	rows, err := rc.db.Query(c.Context(), `
		SELECT r.id, r.rating, r.review_text, r.media_urls, r.created_at,
			COALESCE(NULLIF(u.full_name, ''), 'Verified buyer'),
			(r.user_id = $2)
		FROM reviews r
		JOIN users u ON u.id = r.user_id
		WHERE r.product_id = $1
		ORDER BY r.created_at DESC
		LIMIT 100
	`, productID, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "reviews unavailable")
	}
	defer rows.Close()

	reviews := make([]fiber.Map, 0)
	var totalRating int
	for rows.Next() {
		var id, reviewText, author string
		var mediaURLs []string
		var rating int
		var createdAt time.Time
		var isMine bool
		if err := rows.Scan(&id, &rating, &reviewText, &mediaURLs, &createdAt, &author, &isMine); err != nil {
			return err
		}
		totalRating += rating
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

	summary := fiber.Map{"count": len(reviews), "average_rating": 0.0}
	if len(reviews) > 0 {
		summary["average_rating"] = float64(totalRating) / float64(len(reviews))
	}
	return c.JSON(fiber.Map{"reviews": reviews, "summary": summary})
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
	if err != nil {
		err = rc.db.QueryRow(c.Context(), `
			INSERT INTO reviews(product_id, user_id, order_id, rating, review_text, media_urls)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id
		`, productID, userID, orderID, req.Rating, req.ReviewText, req.MediaURLs).Scan(&reviewID)
		if err != nil {
			return fiber.NewError(fiber.StatusConflict, "review could not be created")
		}
		rewardClaimed, rewardErr := creditReviewReward(c.Context(), rc.db, userID, orderID)
		if rewardErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "review saved but reward could not be credited")
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": reviewID, "created": true, "review_reward_claimed": rewardClaimed})
	}

	_, err = rc.db.Exec(c.Context(), `
		UPDATE reviews
		SET rating = $3, review_text = $4, media_urls = $5, order_id = COALESCE(order_id, $6)
		WHERE id = $1 AND user_id = $2
	`, reviewID, userID, req.Rating, req.ReviewText, req.MediaURLs, orderID)
	if err != nil {
		return err
	}
	rewardClaimed, rewardErr := creditReviewReward(c.Context(), rc.db, userID, orderID)
	if rewardErr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "review saved but reward could not be credited")
	}
	return c.JSON(fiber.Map{"id": reviewID, "updated": true, "review_reward_claimed": rewardClaimed})
}

func (rc *ReviewController) MyProductReview(c *fiber.Ctx) error {
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
