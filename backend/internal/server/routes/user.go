package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterUserRoutes 注册用户相关路由（需要认证）
func RegisterUserRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	jwtAuth middleware.JWTAuthMiddleware,
	settingService *service.SettingService,
) {
	authenticated := v1.Group("")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	authenticated.Use(middleware.BackendModeUserGuard(settingService))
	{
		// 用户接口
		user := authenticated.Group("/user")
		{
			user.GET("/profile", h.User.GetProfile)
			user.PUT("/password", h.User.ChangePassword)
			user.PUT("", h.User.UpdateProfile)
			user.GET("/aff", h.User.GetAffiliate)
			user.POST("/aff/transfer", h.User.TransferAffiliateQuota)
			user.POST("/account-bindings/email/send-code", h.User.SendEmailBindingCode)
			user.POST("/account-bindings/email", h.User.BindEmailIdentity)
			user.DELETE("/account-bindings/:provider", h.User.UnbindIdentity)
			user.POST("/auth-identities/bind/start", h.User.StartIdentityBinding)
			user.GET("/api-keys/:id/usage/daily", h.Usage.GetMyAPIKeyDailyUsage)
			user.GET("/platform-quotas", h.User.GetMyPlatformQuotas)

			// 通知邮箱管理
			notifyEmail := user.Group("/notify-email")
			{
				notifyEmail.POST("/send-code", h.User.SendNotifyEmailCode)
				notifyEmail.POST("/verify", h.User.VerifyNotifyEmail)
				notifyEmail.PUT("/toggle", h.User.ToggleNotifyEmail)
				notifyEmail.DELETE("", h.User.RemoveNotifyEmail)
			}

			// TOTP 双因素认证
			totp := user.Group("/totp")
			{
				totp.GET("/status", h.Totp.GetStatus)
				totp.GET("/verification-method", h.Totp.GetVerificationMethod)
				totp.POST("/send-code", h.Totp.SendVerifyCode)
				totp.POST("/setup", h.Totp.InitiateSetup)
				totp.POST("/enable", h.Totp.Enable)
				totp.POST("/disable", h.Totp.Disable)
			}
		}

		// API Key管理
		keys := authenticated.Group("/keys")
		{
			keys.GET("", h.APIKey.List)
			keys.GET("/:id", h.APIKey.GetByID)
			keys.POST("", h.APIKey.Create)
			keys.PUT("/:id", h.APIKey.Update)
			keys.DELETE("/:id", h.APIKey.Delete)
		}

		// 用户可用分组（非管理员接口）
		groups := authenticated.Group("/groups")
		{
			groups.GET("/available", h.APIKey.GetAvailableGroups)
			groups.GET("/rates", h.APIKey.GetUserGroupRates)
		}

		// 用户可用渠道（非管理员接口）
		channels := authenticated.Group("/channels")
		{
			channels.GET("/available", h.AvailableChannel.List)
		}

		// 使用记录
		usage := authenticated.Group("/usage")
		{
			usage.GET("", h.Usage.List)
			usage.GET("/errors", h.Usage.ListErrors)
			usage.GET("/errors/:id", h.Usage.GetErrorDetail)
			usage.GET("/:id", h.Usage.GetByID)
			usage.GET("/stats", h.Usage.Stats)
			// User dashboard endpoints
			usage.GET("/dashboard/stats", h.Usage.DashboardStats)
			usage.GET("/dashboard/trend", h.Usage.DashboardTrend)
			usage.GET("/dashboard/models", h.Usage.DashboardModels)
			usage.GET("/dashboard/snapshot-v2", h.Usage.DashboardSnapshotV2)
			usage.POST("/dashboard/api-keys-usage", h.Usage.DashboardAPIKeysUsage)
		}

		// 公告（用户可见）
		announcements := authenticated.Group("/announcements")
		{
			announcements.GET("", h.Announcement.List)
			announcements.POST("/:id/read", h.Announcement.MarkRead)
		}

		// 卡密兑换
		redeem := authenticated.Group("/redeem")
		{
			redeem.POST("", h.Redeem.Redeem)
			redeem.GET("/history", h.Redeem.GetHistory)
		}

		// 用户订阅
		subscriptions := authenticated.Group("/subscriptions")
		{
			subscriptions.GET("", h.Subscription.List)
			subscriptions.GET("/active", h.Subscription.GetActive)
			subscriptions.GET("/progress", h.Subscription.GetProgress)
			subscriptions.GET("/summary", h.Subscription.GetSummary)
		}

		// 渠道监控（用户只读）
		monitors := authenticated.Group("/channel-monitors")
		{
			monitors.GET("", h.ChannelMonitor.List)
			monitors.GET("/:id/status", h.ChannelMonitor.GetStatus)
		}

		registerUserImageCanvasRoutes(authenticated, h, settingService)
	}
}

func registerUserImageCanvasRoutes(authenticated *gin.RouterGroup, h *handler.Handlers, settingService *service.SettingService) {
	if h == nil || h.ImageCanvas == nil {
		return
	}
	canvas := authenticated.Group("/image-canvas")
	canvas.Use(middleware.ImageCanvasEnabled(settingService))
	{
		canvas.GET("/config", h.ImageCanvas.GetConfig)
		canvas.GET("/projects", h.ImageCanvas.ListProjects)
		canvas.POST("/projects", h.ImageCanvas.CreateProject)
		canvas.GET("/projects/:project_id", h.ImageCanvas.GetProject)
		canvas.PATCH("/projects/:project_id", h.ImageCanvas.UpdateProject)
		canvas.DELETE("/projects/:project_id", h.ImageCanvas.DeleteProject)
		canvas.POST("/assets", h.ImageCanvas.UploadAsset)
		canvas.GET("/assets/:asset_id", h.ImageCanvas.DownloadAsset)
		canvas.GET("/library-items", h.ImageCanvas.ListLibraryItems)
		canvas.POST("/library-items", h.ImageCanvas.CreateLibraryItem)
		canvas.PATCH("/library-items/:item_id", h.ImageCanvas.UpdateLibraryItem)
		canvas.DELETE("/library-items/:item_id", h.ImageCanvas.DeleteLibraryItem)
		canvas.POST("/editor-documents", h.ImageCanvas.CreateEditorDocument)
		canvas.GET("/editor-documents/:document_id", h.ImageCanvas.GetEditorDocument)
		canvas.PATCH("/editor-documents/:document_id", h.ImageCanvas.UpdateEditorDocument)
		canvas.POST("/editor-documents/:document_id/assets", h.ImageCanvas.UploadEditorDerivedAsset)
		canvas.POST("/jobs", h.ImageCanvas.CreateJob)
		canvas.GET("/jobs/:job_id", h.ImageCanvas.GetJob)
		canvas.GET("/jobs/:job_id/events", h.ImageCanvas.StreamJobEvents)
		canvas.DELETE("/jobs/:job_id", h.ImageCanvas.CancelJob)
		canvas.GET("/jobs/:job_id/results/:index", h.ImageCanvas.DownloadJobResult)
		canvas.POST("/media/video/tasks", h.ImageCanvas.CreateVideoTask)
		canvas.POST("/media/audio", h.ImageCanvas.GenerateAudio)
		canvas.GET("/media/tasks/:task_id", h.ImageCanvas.GetMediaTask)
		canvas.DELETE("/media/tasks/:task_id", h.ImageCanvas.CancelMediaTask)
	}
}
