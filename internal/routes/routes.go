package routes

import (
	"strings"

	"across/backend/internal/config"
	"across/backend/internal/controllers"
	"across/backend/internal/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Register(app *fiber.App, db *pgxpool.Pool, cfg config.Config) {
	payments := controllers.NewPaymentController(db, cfg)
	admin := controllers.NewAdminController(db, cfg)
	orders := controllers.NewOrderController(db)
	catalog := controllers.NewCatalogController(db, cfg)
	uploads := controllers.NewUploadController(cfg)
	reviews := controllers.NewReviewController(db)
	notifications := controllers.NewNotificationsController(db)
	ops := controllers.NewOpsController(db)
	dev := controllers.NewDevController(db, cfg)
	authController := controllers.NewAuthController(db, cfg)
	xpController := controllers.NewXPController(db)
	supportController := controllers.NewSupportController(db)
	analyticsController := controllers.NewAnalyticsController(db)
	profileController := controllers.NewProfileController(db)
	countryGuard := middleware.RequireAllowedCountry(cfg)

	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("OK") })

	v1 := app.Group("/api/v1")
	v1.Get("/health", func(c *fiber.Ctx) error {
		databaseReady := db.Ping(c.Context()) == nil
		status := fiber.StatusOK
		if !databaseReady {
			status = fiber.StatusServiceUnavailable
		}
		return c.Status(status).JSON(fiber.Map{"ok": databaseReady})
	})
	v1.Get("/ready", func(c *fiber.Ctx) error {
		databaseReady := db.Ping(c.Context()) == nil
		privyReady := strings.TrimSpace(cfg.PrivyAppID) != "" && strings.TrimSpace(cfg.PrivyAppSecret) != ""
		storageReady := strings.TrimSpace(cfg.AWSRegion) != "" && strings.TrimSpace(cfg.S3BucketName) != "" && strings.TrimSpace(cfg.AWSAccessKeyID) != "" && strings.TrimSpace(cfg.AWSSecretAccessKey) != ""
		ready := databaseReady && privyReady && storageReady
		status := fiber.StatusOK
		if !ready {
			status = fiber.StatusServiceUnavailable
		}
		return c.Status(status).JSON(fiber.Map{
			"ok": ready,
			"checks": fiber.Map{
				"database":        databaseReady,
				"google_auth":     privyReady,
				"profile_uploads": storageReady,
			},
		})
	})
	v1.Get("/products", catalog.ListProducts)
	v1.Get("/products/:product_id", catalog.GetProduct)
	v1.Get("/products/:product_id/reviews", reviews.ListProductReviews)
	v1.Get("/public/images/view/*", uploads.PublicImageView)
	v1.Post("/dev/login", dev.Login)
	v1.Post("/auth/signup", countryGuard, authController.Signup)
	v1.Post("/auth/login", countryGuard, authController.Login)
	v1.Post("/auth/gmail", countryGuard, authController.Gmail)
	v1.Post("/auth/privy/verify", countryGuard, authController.VerifyPrivy)
	v1.Post("/auth/resend-verification", countryGuard, authController.ResendVerification)
	v1.Get("/auth/verify-email", authController.VerifyEmail)
	v1.Post("/payments/flutterwave/webhook", payments.FlutterwaveWebhook)

	v1.Post("/admin/login", admin.Login)
	v1.Post("/admin/pricing/calculate", controllers.PriceBreakdown)

	superAdminGroup := v1.Group("/admin", middleware.RequireAdminRoles(cfg, db, "super_admin"))
	catalogGroup := v1.Group("/admin", middleware.RequireAdminRoles(cfg, db, "super_admin", "catalog_admin"))
	opsGroup := v1.Group("/admin", middleware.RequireAdminRoles(cfg, db, "super_admin", "procurement_admin", "courier_admin"))

	superAdminGroup.Post("/admins", admin.CreateAdmin)
	superAdminGroup.Patch("/admins/:admin_id/password", admin.ResetAdminPassword)
	superAdminGroup.Delete("/admins/:admin_id", admin.DeleteAdmin)
	superAdminGroup.Delete("/users/:user_id", admin.DeleteUser)
	catalogGroup.Get("/admins", admin.ListAdmins)
	catalogGroup.Get("/users", admin.ListUsers)
	catalogGroup.Get("/orders", admin.ListOrders)
	catalogGroup.Get("/transactions", admin.ListTransactions)
	catalogGroup.Get("/products", admin.ListProducts)
	catalogGroup.Post("/products", admin.CreateProduct)
	catalogGroup.Patch("/products/:product_id", admin.UpdateProduct)
	catalogGroup.Delete("/products/:product_id", admin.DeleteProduct)
	catalogGroup.Post("/uploads/presign", uploads.AdminPresign)
	opsGroup.Get("/batches", admin.ListBatches)
	opsGroup.Patch("/batches/:batch_id", admin.UpdateBatch)
	opsGroup.Get("/batches/:batch_id/orders", admin.ListBatchOrders)
	opsGroup.Get("/manifest/pending", admin.PendingManifest)
	opsGroup.Post("/tracking/batch-scan", admin.BatchScanTracking)
	// Admin II: Purchase management
	opsGroup.Get("/batches/:batch_id/purchase-manifest", ops.GetPurchaseManifest)
	opsGroup.Post("/batches/:batch_id/purchase-confirm", ops.ConfirmPurchase)
	// Admin III: Delivery management
	opsGroup.Post("/batches/:batch_id/confirm-arrival", ops.ConfirmArrival)
	opsGroup.Post("/batches/confirm-delivered", ops.ConfirmDelivered)
	// Auto-confirm expired deliveries (can be called by cron)
	opsGroup.Post("/deliveries/auto-confirm", ops.AutoConfirmDeliveries)

	authed := v1.Group("", middleware.RequireAuth(cfg))
	authed.Get("/auth/session", authController.Session)
	authed.Get("/profile/bootstrap", orders.BootstrapProfile)
	authed.Post("/checkout/quote", countryGuard, orders.QuoteCheckout)
	authed.Get("/orders", orders.ListOrders)
	authed.Get("/orders/:order_id/tracking", orders.Tracking)
	authed.Get("/orders/:order_id/payment-status", orders.PaymentStatus)
	authed.Post("/payments/flutterwave/checkout", countryGuard, payments.FlutterwaveCheckout)
	authed.Post("/payments/tokenized-charge", countryGuard, payments.TokenizedCharge)
	authed.Post("/uploads/presign", uploads.UserPresign)
	authed.Get("/products/:product_id/reviews/mine", reviews.MyProductReview)
	authed.Put("/products/:product_id/reviews", reviews.UpsertProductReview)
	authed.Get("/notifications", notifications.List)
	authed.Get("/notifications/unread-count", notifications.UnreadCount)
	authed.Patch("/notifications/:notification_id/read", notifications.MarkRead)
	authed.Patch("/notifications/read-all", notifications.MarkAllRead)

	// XP System
	authed.Post("/xp/daily-login", xpController.ClaimDailyLogin)
	authed.Get("/xp/balance", xpController.GetBalance)
	authed.Get("/xp/history", xpController.GetHistory)
	authed.Post("/orders/:order_id/xp-award", xpController.AwardPurchaseXP)

	// Support Tickets
	authed.Post("/support/tickets", supportController.CreateTicket)
	authed.Get("/support/tickets", supportController.ListMyTickets)
	authed.Get("/support/tickets/:ticket_id/messages", supportController.GetTicketMessages)

	// Admin Support Tickets
	catalogGroup.Get("/support/tickets", supportController.AdminListTickets)
	catalogGroup.Post("/support/tickets/:ticket_id/reply", supportController.AdminReply)
	catalogGroup.Post("/support/tickets/:ticket_id/close", supportController.AdminCloseTicket)

	// Analytics (Admin I and Super Admin)
	catalogGroup.Get("/analytics/daily-sales", analyticsController.GetDailySales)
	catalogGroup.Get("/analytics/complaints", analyticsController.ListComplaints)
	catalogGroup.Post("/analytics/complaints", analyticsController.CreateComplaint)
	catalogGroup.Post("/analytics/complaints/:complaint_id/resolve", analyticsController.ResolveComplaint)

	// Profit/Loss (Super Admin only)
	superAdminGroup.Get("/analytics/profit-loss", analyticsController.GetProfitLoss)

	// Profile
	authed.Get("/profile", profileController.GetProfile)
	authed.Put("/profile", profileController.UpdateProfile)
}
