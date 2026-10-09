package routes

import (
	_ "embed"
	"strings"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/controllers"
	"across/backend/internal/middleware"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

//go:embed atlantic-express-logo.png
var atlanticExpressLogo []byte

func Register(app *fiber.App, db, readDB *pgxpool.Pool, cache *redis.Client, cfg config.Config) {
	payments := controllers.NewPaymentController(db, cfg)
	admin := controllers.NewAdminController(db, cfg)
	orders := controllers.NewOrderController(db, cfg.FlutterwaveCustomerPaysFees, func() bool { return cfg.ProviderSubscriptionsRequired(time.Now().UTC()) })
	if readDB == nil {
		readDB = db
	}
	// Read operational prices from the database that commits seller edits.
	// Revision-keyed Redis still shares catalogue responses across buyers.
	catalog := controllers.NewCatalogController(db, cfg)
	uploads := controllers.NewUploadController(cfg)
	reviews := controllers.NewReviewController(db)
	notifications := controllers.NewNotificationsController(db)
	ops := controllers.NewOpsController(db)
	dev := controllers.NewDevController(db, cfg)
	authController := controllers.NewAuthController(db, cfg)
	sesController := controllers.NewSESController(db, cfg)
	xpController := controllers.NewXPController(db)
	waitlist := controllers.NewWaitlistController(db)
	supportController := controllers.NewSupportController(db)
	analyticsController := controllers.NewAnalyticsController(db)
	profileController := controllers.NewProfileController(db)
	marketplaceController := controllers.NewProviderMarketplaceController(db, cfg)
	authRateLimit := middleware.DistributedRateLimit(cache, "auth", 20, time.Minute)

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
	v1.Post("/waitlist", middleware.DistributedRateLimit(cache, "waitlist", 20, time.Hour), waitlist.Join)
	v1.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true})
	})
	v1.Get("/ready", func(c *fiber.Ctx) error {
		databaseReady := db.Ping(c.Context()) == nil
		readDatabaseReady := readDB.Ping(c.Context()) == nil
		redisReady := cache != nil
		if redisReady {
			redisReady = cache.Ping(c.Context()).Err() == nil
		} else if cfg.RedisOptional {
			redisReady = true
		}
		// Readiness must never depend on a live call to an external identity
		// provider; that would eject every healthy replica during a Privy outage.
		privyReady := strings.TrimSpace(cfg.PrivyAppID) != "" && strings.TrimSpace(cfg.PrivyAppSecret) != ""
		storageReady := strings.TrimSpace(cfg.AWSRegion) != "" && strings.TrimSpace(cfg.S3BucketName) != "" && strings.TrimSpace(cfg.AWSAccessKeyID) != "" && strings.TrimSpace(cfg.AWSSecretAccessKey) != ""
		emailReady := strings.TrimSpace(cfg.SMTPHost) != "" && strings.TrimSpace(cfg.SMTPUsername) != "" && strings.TrimSpace(cfg.SMTPPassword) != "" && strings.TrimSpace(cfg.SMTPFromEmail) != ""
		ready := databaseReady && readDatabaseReady && redisReady
		pool := db.Stat()
		status := fiber.StatusOK
		if !ready {
			status = fiber.StatusServiceUnavailable
		}
		return c.Status(status).JSON(fiber.Map{
			"ok": ready,
			"checks": fiber.Map{
				"database":        databaseReady,
				"read_database":   readDatabaseReady,
				"redis":           redisReady,
				"email_delivery":  emailReady,
				"google_auth":     privyReady,
				"profile_uploads": storageReady,
			},
			"database_pool": fiber.Map{
				"acquired": pool.AcquiredConns(),
				"idle":     pool.IdleConns(),
				"max":      pool.MaxConns(),
			},
		})
	})
	v1.Get("/products", catalog.ListProducts)
	v1.Get("/catalog/version", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		var revision int64
		if err := db.QueryRow(c.Context(), `SELECT revision FROM catalog_revision WHERE singleton=true`).Scan(&revision); err != nil {
			return fiber.NewError(fiber.StatusServiceUnavailable, "catalog version unavailable")
		}
		return c.JSON(fiber.Map{"change_token": middleware.CatalogChangeToken(revision, time.Now())})
	})
	v1.Get("/buyer-markets", orders.ListBuyerMarkets)
	v1.Get("/products/flash-sale", catalog.ListFlashSales)
	v1.Get("/public/brand/logo.png", func(c *fiber.Ctx) error {
		// The versioned email URL allows a logo refresh while keeping normal CDN
		// caching. Avoid immutable here because email image proxies cache hard.
		c.Set(fiber.HeaderCacheControl, "public, max-age=3600")
		c.Type("png")
		return c.Send(atlanticExpressLogo)
	})
	v1.Get("/products/:product_id/recommendations", catalog.ListRecommendations)
	v1.Get("/products/:product_id", catalog.GetProduct)
	v1.Get("/products/:product_id/reviews", reviews.ListProductReviews)
	v1.Get("/marketplace/listings", marketplaceController.ListPublicListings)
	v1.Get("/marketplace/nearby", marketplaceController.ListNearbyListings)
	v1.Get("/marketplace/listings/:listing_id", marketplaceController.GetPublicListing)
	v1.Get("/marketplace/listings/:listing_id/reviews", marketplaceController.ListListingReviews)
	v1.Get("/marketplace/listings/:listing_id/availability", marketplaceController.ListAvailability)
	v1.Get("/marketplace/subscription-plans", marketplaceController.ListPlans)
	v1.Get("/public/images/view/*", uploads.PublicImageView)
	v1.Post("/dev/login", dev.Login)
	v1.Post("/auth/signup", authRateLimit, authController.Signup)
	v1.Post("/auth/login", authRateLimit, authController.Login)
	v1.Post("/auth/gmail", authRateLimit, authController.Gmail)
	v1.Post("/auth/privy/verify", authRateLimit, authController.VerifyPrivy)
	v1.Post("/auth/resend-verification", authRateLimit, authController.ResendVerification)
	v1.Get("/auth/verify-email", authController.VerifyEmail)
	v1.Post("/auth/forgot-password", authRateLimit, authController.ForgotPassword)
	v1.Get("/auth/reset-password", authController.ResetPasswordPage)
	v1.Post("/auth/reset-password", authRateLimit, authController.ResetPassword)
	v1.Post("/webhooks/ses", sesController.Webhook)
	v1.Post("/payments/flutterwave/webhook", payments.FlutterwaveWebhook)

	v1.Post("/admin/login", authRateLimit, admin.Login)
	v1.Post("/admin/pricing/calculate", controllers.PriceBreakdown)
	adminRoutes := v1.Group("/admin")
	allAdmins := middleware.RequireAdminRoles(cfg, db, "super_admin", "catalog_admin")
	superOnly := middleware.RequireAdminRoles(cfg, db, "super_admin")
	catalogOnly := middleware.RequireAdminRoles(cfg, db, "super_admin", "catalog_admin")

	adminRoutes.Get("/session", allAdmins, admin.Session)
	adminRoutes.Get("/activity", allAdmins, admin.Activity)
	adminRoutes.Patch("/activity/read-all", allAdmins, admin.MarkAllActivityRead)
	adminRoutes.Patch("/activity/:event_id/read", allAdmins, admin.MarkActivityRead)
	adminRoutes.Get("/overview", allAdmins, admin.Overview)
	adminRoutes.Get("/ops/queue-health", allAdmins, ops.QueueHealth)
	adminRoutes.Post("/admins", superOnly, admin.CreateAdmin)
	adminRoutes.Patch("/admins/:admin_id/password", superOnly, admin.ResetAdminPassword)
	adminRoutes.Delete("/admins/:admin_id", superOnly, admin.DeleteAdmin)
	adminRoutes.Delete("/users/:user_id", superOnly, admin.DeleteUser)
	adminRoutes.Get("/admins", catalogOnly, admin.ListAdmins)
	adminRoutes.Get("/users", catalogOnly, admin.ListUsers)
	adminRoutes.Get("/orders", catalogOnly, admin.ListOrders)
	adminRoutes.Get("/transactions", catalogOnly, admin.ListTransactions)
	adminRoutes.Post("/payments/flutterwave/reconcile", superOnly, payments.AdminReconcileFlutterwavePayment)
	adminRoutes.Get("/providers", catalogOnly, marketplaceController.AdminListProviders)
	adminRoutes.Get("/providers/:provider_id/verification-documents", catalogOnly, marketplaceController.AdminListVerificationDocuments)
	adminRoutes.Patch("/providers/:provider_id/verification-documents/:document_id", catalogOnly, marketplaceController.AdminReviewVerificationDocument)
	adminRoutes.Patch("/providers/:provider_id/verification", catalogOnly, marketplaceController.AdminVerifyProvider)
	adminRoutes.Get("/provider-listings", catalogOnly, marketplaceController.AdminListListings)
	adminRoutes.Patch("/provider-listings/:listing_id/moderation", catalogOnly, marketplaceController.AdminModerateListing)
	adminRoutes.Get("/merchant-products", catalogOnly, marketplaceController.AdminListMerchantProducts)
	adminRoutes.Patch("/merchant-products/:product_id/moderation", catalogOnly, marketplaceController.AdminModerateMerchantProduct)
	adminRoutes.Get("/provider-subscription-plans", superOnly, marketplaceController.AdminListPlans)
	adminRoutes.Get("/provider-subscription-access", superOnly, marketplaceController.AdminProviderSubscriptionAccess)
	adminRoutes.Put("/provider-subscription-access", superOnly, marketplaceController.AdminSetProviderSubscriptionAccess)
	adminRoutes.Post("/provider-subscription-plans", superOnly, marketplaceController.AdminUpsertPlan)
	adminRoutes.Post("/provider-subscription-plans/:plan_id/price", superOnly, marketplaceController.AdminChangePlanPrice)
	adminRoutes.Get("/provider-gateway-subscriptions", superOnly, marketplaceController.AdminListGatewaySubscriptions)
	adminRoutes.Post("/provider-gateway-subscriptions/:subscription_id/cancel", superOnly, marketplaceController.AdminCancelGatewaySubscription)
	adminRoutes.Delete("/provider-subscription-plans/:plan_id", superOnly, marketplaceController.AdminDeactivatePlan)
	adminRoutes.Post("/provider-subscriptions/reconcile", superOnly, payments.AdminReconcileProviderSubscription)
	adminRoutes.Post("/uploads/presign", catalogOnly, uploads.AdminPresign)
	adminRoutes.Get("/merchant-fulfillments", allAdmins, marketplaceController.AdminListMerchantFulfillments)
	// Auto-confirm is a settlement safeguard, not a delivery operation.
	adminRoutes.Post("/deliveries/auto-confirm", superOnly, ops.AutoConfirmDeliveries)

	authed := v1.Group("", middleware.RequireAuth(cfg, db))
	authed.Get("/auth/session", authController.Session)
	authed.Get("/profile/bootstrap", orders.BootstrapProfile)
	authed.Post("/checkout/quote", orders.QuoteCheckout)
	authed.Post("/checkout/quotes/:order_id/release-xp", orders.ReleaseXPQuote)
	authed.Get("/orders", orders.ListOrders)
	authed.Get("/orders/:order_id/tracking", orders.Tracking)
	authed.Get("/orders/:order_id/payment-status", orders.PaymentStatus)
	authed.Get("/payments/options", payments.PaymentOptions)
	authed.Get("/payments/history", payments.BuyerPaymentHistory)
	authed.Post("/orders/:order_id/confirm-receipt", ops.ConfirmReceipt)
	authed.Post("/orders/:order_id/review-reward/claim", ops.ClaimReviewReward)
	authed.Post("/payments/flutterwave/checkout", payments.FlutterwaveCheckout)
	authed.Post("/payments/flutterwave/verify", payments.VerifyFlutterwavePayment)
	authed.Post("/payments/tokenized-charge", payments.TokenizedCharge)
	authed.Post("/uploads/presign", uploads.UserPresign)
	authed.Get("/products/:product_id/reviews/mine", reviews.MyProductReview)
	authed.Put("/products/:product_id/reviews", reviews.UpsertProductReview)
	authed.Get("/notifications", notifications.List)
	authed.Get("/notifications/unread-count", notifications.UnreadCount)
	authed.Get("/notifications/activity", notifications.Activity)
	authed.Patch("/notifications/read-all", notifications.MarkAllRead)
	authed.Patch("/notifications/:notification_id/read", notifications.MarkRead)
	authed.Post("/notifications/push-token", notifications.RegisterPushToken)
	authed.Post("/notifications/test-push", notifications.TestPush)
	authed.Delete("/notifications/push-token", notifications.UnregisterPushToken)

	// Provider marketplace. Providers authenticate through the verified buyer
	// identity system but operate through a separate organization membership;
	// none of these routes grant administrator privileges.
	authed.Post("/providers/onboarding", marketplaceController.Onboard)
	authed.Get("/providers/payout-banks", marketplaceController.ListPayoutBanks)
	authed.Get("/providers/me", marketplaceController.MyProvider)
	authed.Patch("/providers/me", marketplaceController.UpdateMyProvider)
	authed.Post("/providers/me/payout-account", marketplaceController.ConfigurePayoutAccount)
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
	authed.Patch("/providers/me/merchant-orders/fulfillment/bulk", marketplaceController.BulkTransitionMerchantOrders)
	authed.Patch("/providers/me/merchant-orders/:order_id/fulfillment", marketplaceController.TransitionMerchantOrder)
	authed.Get("/providers/me/manifests", marketplaceController.ListMerchantManifests)
	authed.Post("/providers/me/manifests", marketplaceController.CreateMerchantManifest)
	authed.Get("/providers/me/manifests/:manifest_id", marketplaceController.GetMerchantManifest)
	authed.Patch("/providers/me/manifests/:manifest_id", marketplaceController.TransitionMerchantManifest)
	authed.Post("/providers/me/listings/:listing_id/availability", marketplaceController.UpsertAvailability)
	authed.Get("/providers/me/requests", marketplaceController.ListProviderRequests)
	authed.Patch("/providers/me/requests/:request_id", marketplaceController.UpdateProviderRequest)
	authed.Get("/providers/me/notifications", marketplaceController.ListProviderNotifications)
	authed.Patch("/providers/me/notifications/read-all", marketplaceController.MarkProviderNotificationsRead)
	authed.Get("/providers/me/conversations", marketplaceController.ListProviderConversations)
	authed.Get("/providers/me/conversations/:conversation_id/messages", marketplaceController.ProviderConversationMessages)
	authed.Post("/providers/me/conversations/:conversation_id/messages", marketplaceController.SendProviderConversationMessage)
	authed.Post("/providers/me/subscription-checkout", marketplaceController.SubscriptionCheckout)
	authed.Post("/providers/me/subscription-confirm", payments.ConfirmProviderSubscription)
	authed.Get("/marketplace/requests", marketplaceController.ListMyRequests)
	authed.Post("/marketplace/listings/:listing_id/contact", marketplaceController.RevealContact)
	authed.Post("/marketplace/listings/:listing_id/requests", marketplaceController.CreateRequest)
	authed.Put("/marketplace/listings/:listing_id/review", marketplaceController.UpsertListingReview)
	authed.Post("/marketplace/listings/:listing_id/reports", marketplaceController.ReportListing)
	authed.Post("/marketplace/listings/:listing_id/conversations", marketplaceController.StartProviderConversation)
	authed.Post("/marketplace/products/:product_id/conversations", marketplaceController.StartProductConversation)
	authed.Post("/marketplace/chat-images/presign", marketplaceController.PresignChatImage)
	authed.Get("/marketplace/conversations", marketplaceController.ListBuyerConversations)
	authed.Get("/marketplace/conversations/:conversation_id/messages", marketplaceController.BuyerConversationMessages)
	authed.Post("/marketplace/conversations/:conversation_id/messages", marketplaceController.SendBuyerConversationMessage)

	// XP System
	authed.Post("/xp/daily-login", xpController.ClaimDailyLogin)
	authed.Get("/xp/balance", xpController.GetBalance)
	authed.Get("/xp/history", xpController.GetHistory)
	authed.Get("/xp/withdrawals", xpController.ListWithdrawals)
	authed.Post("/xp/withdrawals", xpController.RequestWithdrawal)
	adminRoutes.Get("/waitlist", superOnly, waitlist.AdminList)
	adminRoutes.Get("/waitlist/export", superOnly, waitlist.AdminExport)
	adminRoutes.Get("/xp/withdrawals", superOnly, xpController.AdminListWithdrawals)
	adminRoutes.Patch("/xp/withdrawals/:withdrawal_id", superOnly, xpController.AdminReviewWithdrawal)
	authed.Post("/orders/:order_id/xp-award", xpController.AwardPurchaseXP)

	// Support Tickets
	authed.Post("/support/tickets", supportController.CreateTicket)
	authed.Get("/support/tickets", supportController.ListMyTickets)
	authed.Get("/support/tickets/:ticket_id/messages", supportController.GetTicketMessages)
	authed.Post("/support/tickets/:ticket_id/reply", supportController.UserReply)

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
