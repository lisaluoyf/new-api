package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	mediaLibrary := router.Group("/v1")
	mediaLibrary.Use(middleware.RouteTag("relay"), middleware.TokenAuth(), middleware.ModelRequestRateLimit())
	mediaLibrary.POST("/uploads/images", middleware.UploadRateLimit(), controller.UploadMediaImage)
	library := mediaLibrary.Group("/seedance2/private-avatar")
	library.POST("", middleware.UploadRateLimit(), controller.SubmitSeedanceAssets)
	library.POST("/assets", middleware.UploadRateLimit(), controller.SubmitSeedanceAssets)
	for _, kind := range []string{"asset", "group"} {
		resourceKind := kind
		resources := library.Group("/"+kind+"s", func(c *gin.Context) { c.Set("seedance_resource_kind", resourceKind); c.Next() })
		if kind == "group" {
			resources.POST("", middleware.UploadRateLimit(), controller.CreateSeedanceAssetGroup)
		}
		resources.GET("", controller.ListSeedanceResources)
		resources.GET("/:resource_id", controller.GetSeedanceResource)
		resources.PATCH("/:resource_id", controller.UpdateSeedanceResource)
		resources.DELETE("/:resource_id", controller.DeleteSeedanceResource)
	}
	// Video proxy: accepts either session auth (dashboard) or token auth (API clients)
	videoProxyRouter := router.Group("/v1")
	videoProxyRouter.Use(middleware.RouteTag("relay"))
	videoProxyRouter.Use(middleware.TokenOrUserAuth())
	{
		videoProxyRouter.GET("/videos/:task_id/content", controller.VideoProxy)
		videoProxyRouter.GET("/videos/:task_id/last-frame", controller.VideoLastFrame)
	}

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth(), middleware.PrepareSeedanceAssetGeneration(), middleware.Distribute(), middleware.ApplySeedanceAssetKey())
	{
		videoV1Router.POST("/video/generations", controller.RelayTask)
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/videos/generations", controller.RelayTask)
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
	}
	// openai compatible API video routes
	// docs: https://platform.openai.com/docs/api-reference/videos/create
	{
		videoV1Router.POST("/videos", controller.RelayTask)
		videoV1Router.GET("/videos/:task_id", controller.RelayTaskFetch)
	}

	klingV1Router := router.Group("/kling/v1")
	klingV1Router.Use(middleware.RouteTag("relay"))
	klingV1Router.Use(middleware.KlingRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		klingV1Router.POST("/videos/text2video", controller.RelayTask)
		klingV1Router.POST("/videos/image2video", controller.RelayTask)
		klingV1Router.GET("/videos/text2video/:task_id", controller.RelayTaskFetch)
		klingV1Router.GET("/videos/image2video/:task_id", controller.RelayTaskFetch)
	}

	// Jimeng official API routes - direct mapping to official API format
	jimengOfficialGroup := router.Group("jimeng")
	jimengOfficialGroup.Use(middleware.RouteTag("relay"))
	jimengOfficialGroup.Use(middleware.JimengRequestConvert(), middleware.TokenAuth(), middleware.Distribute())
	{
		// Maps to: /?Action=CVSync2AsyncSubmitTask&Version=2022-08-31 and /?Action=CVSync2AsyncGetResult&Version=2022-08-31
		jimengOfficialGroup.POST("/", controller.RelayTask)
	}
}
