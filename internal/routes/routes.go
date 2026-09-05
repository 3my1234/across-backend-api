package routes

import (
	_ "embed"
	"strings"

	"across/backend/internal/config"
	"across/backend/internal/controllers"
	"across/backend/internal/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed atlantic-express-logo.png
var atlanticExpressLogo []byte

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
	sesController := controllers.NewSESController(db, cfg)
	xpController := controllers.NewXPController(db)
	supportController := controllers.NewSupportController(db)
	analyticsController := controllers.NewAnalyticsController(db)
	profileController := controllers.NewProfileController(db)
	marketplaceController := controllers.NewProviderMarketplaceController(db, cfg)
	countryGuard := middleware.RequireAllowedCountry(cfg)

	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("OK") })

	v1 := app.Group("/api/v1")
	v1.Use(func(c *fiber.Ctx) error {
		err := c.Next()
		if c.GetRespHeader("Cache-Control") != "" {
			return err
		}
		path := c.Path()
		if c.Method() == fiber.MethodGet &&
			(path == "/api/v1/products" ||
				strings.HasPrefix(path, "/api/v1/products/") &&
					!strings.Contains(path, "/reviews") && !strings.Contains(path, "/mine")) {
			// Product prices and inventory are operational data. Keep a very short
			// shared cache window; explicit app refreshes use a cache-busting query.
			c.Set(fiber.HeaderCacheControl, "public, max-age=5, stale-while-revalidate=10")
			c.Vary(fiber.HeaderAcceptEncoding)
			return err
		}
		c.Set(fiber.HeaderCacheControl, "private, no-store")
		return err
	})
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
		privyReady := authController.PrivyReady(c.Context()) == nil
		storageReady := strings.TrimSpace(cfg.AWSRegion) != "" && strings.TrimSpace(cfg.S3BucketName) != "" && strings.TrimSpace(cfg.AWSAccessKeyID) != "" && strings.TrimSpace(cfg.AWSSecretAccessKey) != ""
		emailReady := strings.TrimSpace(cfg.SMTPHost) != "" && strings.TrimSpace(cfg.SMTPUsername) != "" && strings.TrimSpace(cfg.SMTPPassword) != "" && strings.TrimSpace(cfg.SMTPFromEmail) != ""
		ready := databaseReady && privyReady && storageReady && emailReady
		status := fiber.StatusOK
		if !ready {
			status = fiber.StatusServiceUnavailable
		}
		return c.Status(status).JSON(fiber.Map{
			"ok": ready,
			"checks": fiber.Map{
				"database":        databaseReady,
				"email_delivery":  emailReady,
				"google_auth":     privyReady,
				"profile_uploads": storageReady,
			},
		})
	})
	v1.Get("/products", catalog.ListProducts)
	v1.Get("/products/flash-sale", catalog.ListFlashSales)
	v1.Get("/public/brand/logo.png", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "public, max-age=604800, immutable")
		c.Type("png")
		return c.Send(atlanticExpressLogo)
	})
	v1.Get("/products/:product_id/recommendations", catalog.ListRecommendations)
	v1.Get("/products/:product_id", catalog.GetProduct)
	v1.Get("/products/:product_id/reviews", reviews.ListProductReviews)
	v1.Get("/marketplace/listings", marketplaceController.ListPublicListings)
	v1.Get("/marketplace/nearby", marketplaceController.ListNearbyListings)
	v1.Get("/marketplace/listings/:listing_id", marketplaceController.GetPublicListing)
	v1.Get("/marketplace/listings/:listing_id/availability", marketplaceController.ListAvailability)
	v1.Get("/marketplace/subscription-plans", marketplaceController.ListPlans)
	v1.Get("/public/images/view/*", uploads.PublicImageView)
	v1.Post("/dev/login", dev.Login)
	v1.Post("/auth/signup", countryGuard, authController.Signup)
	v1.Post("/auth/login", countryGuard, authController.Login)
	v1.Post("/auth/gmail", countryGuard, authController.Gmail)
	v1.Post("/auth/privy/verify", countryGuard, authController.VerifyPrivy)
	v1.Post("/auth/resend-verification", countryGuard, authController.ResendVerification)
	v1.Get("/auth/verify-email", authController.VerifyEmail)
	v1.Post("/auth/forgot-password", authController.ForgotPassword)
	v1.Get("/auth/reset-password", authController.ResetPasswordPage)
	v1.Post("/auth/reset-password", authController.ResetPassword)
	v1.Post("/webhooks/ses", sesController.Webhook)
	v1.Post("/payments/flutterwave/webhook", payments.FlutterwaveWebhook)

	v1.Post("/admin/login", admin.Login)
	v1.Post("/admin/pricing/calculate", controllers.PriceBreakdown)
	adminRoutes := v1.Group("/admin")
	allAdmins := middleware.RequireAdminRoles(cfg, db, "super_admin", "catalog_admin", "procurement_admin", "courier_admin")
	superOnly := middleware.RequireAdminRoles(cfg, db, "super_admin")
	catalogOnly := middleware.RequireAdminRoles(cfg, db, "super_admin", "catalog_admin")
	opsOnly := middleware.RequireAdminRoles(cfg, db, "super_admin", "procurement_admin", "courier_admin")
	courierOnly := middleware.RequireAdminRoles(cfg, db, "super_admin", "courier_admin")
	procurementActions := middleware.RequireAdminRoles(cfg, db, "procurement_admin")
	courierActions := middleware.RequireAdminRoles(cfg, db, "courier_admin")

	adminRoutes.Get("/session", allAdmins, admin.Session)
	adminRoutes.Get("/activity", allAdmins, admin.Activity)
	adminRoutes.Patch("/activity/read-all", allAdmins, admin.MarkAllActivityRead)
	adminRoutes.Patch("/activity/:event_id/read", allAdmins, admin.MarkActivityRead)
	adminRoutes.Get("/overview", allAdmins, admin.Overview)
	adminRoutes.Post("/admins", superOnly, admin.CreateAdmin)
	adminRoutes.Patch("/admins/:admin_id/password", superOnly, admin.ResetAdminPassword)
	adminRoutes.Delete("/admins/:admin_id", superOnly, admin.DeleteAdmin)
	adminRoutes.Delete("/users/:user_id", superOnly, admin.DeleteUser)
	adminRoutes.Get("/admins", catalogOnly, admin.ListAdmins)
	adminRoutes.Get("/users", catalogOnly, admin.ListUsers)
	adminRoutes.Get("/orders", catalogOnly, admin.ListOrders)
	adminRoutes.Get("/transactions", catalogOnly, admin.ListTransactions)
	adminRoutes.Post("/payments/flutterwave/reconcile", superOnly, payments.AdminReconcileFlutterwavePayment)
	adminRoutes.Get("/products", catalogOnly, admin.ListProducts)
	adminRoutes.Post("/products", catalogOnly, admin.CreateProduct)
	adminRoutes.Patch("/products/:product_id", catalogOnly, admin.UpdateProduct)
	adminRoutes.Delete("/products/:product_id", catalogOnly, admin.DeleteProduct)
	adminRoutes.Get("/providers", catalogOnly, marketplaceController.AdminListProviders)
	adminRoutes.Get("/providers/:provider_id/verification-documents", catalogOnly, marketplaceController.AdminListVerificationDocuments)
	adminRoutes.Patch("/providers/:provider_id/verification-documents/:document_id", catalogOnly, marketplaceController.AdminReviewVerificationDocument)
	adminRoutes.Patch("/providers/:provider_id/verification", catalogOnly, marketplaceController.AdminVerifyProvider)
	adminRoutes.Get("/provider-listings", catalogOnly, marketplaceController.AdminListListings)
	adminRoutes.Patch("/provider-listings/:listing_id/moderation", catalogOnly, marketplaceController.AdminModerateListing)
	adminRoutes.Get("/merchant-products", catalogOnly, marketplaceController.AdminListMerchantProducts)
	adminRoutes.Patch("/merchant-products/:product_id/moderation", catalogOnly, marketplaceController.AdminModerateMerchantProduct)
	adminRoutes.Post("/provider-subscription-plans", superOnly, marketplaceController.AdminUpsertPlan)
	adminRoutes.Post("/provider-subscriptions/reconcile", superOnly, payments.AdminReconcileProviderSubscription)
	adminRoutes.Post("/uploads/presign", catalogOnly, uploads.AdminPresign)
	adminRoutes.Get("/batches", allAdmins, admin.ListBatches)
	adminRoutes.Patch("/batches/:batch_id", superOnly, admin.UpdateBatch)
	adminRoutes.Get("/batches/:batch_id/orders", allAdmins, admin.ListBatchOrders)
	adminRoutes.Get("/manifest/pending", opsOnly, admin.PendingManifest)
	adminRoutes.Post("/tracking/batch-scan", opsOnly, admin.BatchScanTracking)
	adminRoutes.Post("/batches/:batch_id/transitions", allAdmins, ops.TransitionBatch)
	// Admin II: Purchase management
	adminRoutes.Get("/batches/:batch_id/purchase-manifest", allAdmins, ops.GetPurchaseManifest)
	adminRoutes.Post("/batches/:batch_id/purchase-confirm", procurementActions, ops.ConfirmPurchase)
	// Admin III: Delivery management
	adminRoutes.Post("/batches/confirm-delivered", courierActions, ops.ConfirmDelivered)
	adminRoutes.Get("/merchant-fulfillments", allAdmins, marketplaceController.AdminListMerchantFulfillments)
	adminRoutes.Patch("/merchant-orders/:order_id/last-mile", courierActions, marketplaceController.TransitionAtlanticLastMile)
	// Auto-confirm expired deliveries (can be called by cron)
	adminRoutes.Post("/deliveries/auto-confirm", courierOnly, ops.AutoConfirmDeliveries)

	authed := v1.Group("", middleware.RequireAuth(cfg, db))
	authed.Get("/auth/session", authController.Session)
	authed.Get("/profile/bootstrap", orders.BootstrapProfile)
	authed.Post("/checkout/quote", countryGuard, orders.QuoteCheckout)
	authed.Get("/orders", orders.ListOrders)
	authed.Get("/orders/:order_id/tracking", orders.Tracking)
	authed.Get("/orders/:order_id/payment-status", orders.PaymentStatus)
	authed.Post("/orders/:order_id/confirm-receipt", ops.ConfirmReceipt)
	authed.Post("/orders/:order_id/review-reward/claim", ops.ClaimReviewReward)
	authed.Post("/payments/flutterwave/checkout", countryGuard, payments.FlutterwaveCheckout)
	authed.Post("/payments/flutterwave/verify", payments.VerifyFlutterwavePayment)
	authed.Post("/payments/tokenized-charge", countryGuard, payments.TokenizedCharge)
	authed.Post("/uploads/presign", uploads.UserPresign)
	authed.Get("/products/:product_id/reviews/mine", reviews.MyProductReview)
	authed.Put("/products/:product_id/reviews", reviews.UpsertProductReview)
	authed.Get("/notifications", notifications.List)
	authed.Get("/notifications/unread-count", notifications.UnreadCount)
	authed.Get("/notifications/activity", notifications.Activity)
	authed.Patch("/notifications/:notification_id/read", notifications.MarkRead)
	authed.Patch("/notifications/read-all", notifications.MarkAllRead)
	authed.Post("/notifications/push-token", notifications.RegisterPushToken)
	authed.Delete("/notifications/push-token", notifications.UnregisterPushToken)

	// Provider marketplace. Providers authenticate through the verified buyer
	// identity system but operate through a separate organization membership;
	// none of these routes grant administrator privileges.
	authed.Post("/providers/onboarding", marketplaceController.Onboard)
	authed.Get("/providers/me", marketplaceController.MyProvider)
	authed.Patch("/providers/me", marketplaceController.UpdateMyProvider)
	authed.Post("/providers/me/uploads/presign", marketplaceController.PresignProviderUpload)
	authed.Get("/providers/me/verification-documents", marketplaceController.ListVerificationDocuments)
	authed.Post("/providers/me/verification-documents", marketplaceController.AddVerificationDocument)
	authed.Get("/providers/me/listings", marketplaceController.ListMyListings)
	authed.Post("/providers/me/listings", marketplaceController.CreateListing)
	authed.Patch("/providers/me/listings/:listing_id", marketplaceController.UpdateListing)
	authed.Delete("/providers/me/listings/:listing_id", marketplaceController.ArchiveListing)
	authed.Post("/providers/me/listings/:listing_id/submit", marketplaceController.SubmitListing)
	authed.Get("/providers/me/products", marketplaceController.ListMyMerchantProducts)
	authed.Post("/providers/me/products", marketplaceController.CreateMerchantProduct)
	authed.Patch("/providers/me/products/:product_id", marketplaceController.UpdateMerchantProduct)
	authed.Delete("/providers/me/products/:product_id", marketplaceController.ArchiveMerchantProduct)
	authed.Post("/providers/me/products/:product_id/submit", marketplaceController.SubmitMerchantProduct)
	authed.Get("/providers/me/merchant-orders", marketplaceController.ListMyMerchantOrders)
	authed.Patch("/providers/me/merchant-orders/:order_id/fulfillment", marketplaceController.TransitionMerchantOrder)
	authed.Get("/providers/me/manifests", marketplaceController.ListMerchantManifests)
	authed.Post("/providers/me/manifests", marketplaceController.CreateMerchantManifest)
	authed.Get("/providers/me/manifests/:manifest_id", marketplaceController.GetMerchantManifest)
	authed.Patch("/providers/me/manifests/:manifest_id", marketplaceController.TransitionMerchantManifest)
	authed.Post("/providers/me/listings/:listing_id/availability", marketplaceController.UpsertAvailability)
	authed.Get("/providers/me/requests", marketplaceController.ListProviderRequests)
	authed.Patch("/providers/me/requests/:request_id", marketplaceController.UpdateProviderRequest)
	authed.Post("/providers/me/subscription-checkout", countryGuard, marketplaceController.SubscriptionCheckout)
	authed.Get("/marketplace/requests", marketplaceController.ListMyRequests)
	authed.Post("/marketplace/listings/:listing_id/contact", marketplaceController.RevealContact)
	authed.Post("/marketplace/listings/:listing_id/requests", marketplaceController.CreateRequest)
	authed.Post("/marketplace/listings/:listing_id/reports", marketplaceController.ReportListing)

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
	adminRoutes.Get("/support/tickets", catalogOnly, supportController.AdminListTickets)
	adminRoutes.Get("/support/tickets/:ticket_id/messages", catalogOnly, supportController.AdminGetTicketMessages)
	adminRoutes.Post("/support/tickets/:ticket_id/reply", catalogOnly, supportController.AdminReply)
	adminRoutes.Post("/support/tickets/:ticket_id/close", catalogOnly, supportController.AdminCloseTicket)

	// Analytics (Admin I and Super Admin)
	adminRoutes.Get("/analytics/daily-sales", catalogOnly, analyticsController.GetDailySales)
	adminRoutes.Get("/analytics/complaints", catalogOnly, analyticsController.ListComplaints)
	adminRoutes.Post("/analytics/complaints", catalogOnly, analyticsController.CreateComplaint)
	adminRoutes.Post("/analytics/complaints/:complaint_id/resolve", catalogOnly, analyticsController.ResolveComplaint)

	// Profit/Loss (Super Admin only)
	adminRoutes.Get("/analytics/profit-loss", superOnly, analyticsController.GetProfitLoss)

	// Profile
	authed.Get("/profile", profileController.GetProfile)
	authed.Put("/profile", profileController.UpdateProfile)
}
