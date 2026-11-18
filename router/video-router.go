package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	videoSharedRouter := router.Group("/v1")
	videoSharedRouter.Use(middleware.RouteTag("relay"))
	videoSharedRouter.Use(middleware.TokenAuth())
	videoSharedRouter.Use(middleware.SystemPerformanceCheck())
	videoSharedRouter.POST(
		"/video/generations",
		middleware.PinTaskPluginEndpoint(),
		middleware.TaskPluginEndpointOnly(middleware.ModelRequestRateLimit()),
		middleware.PrepareTaskPluginEndpoint(),
		middleware.Distribute(),
		func(c *gin.Context) {
			controller.RelayTaskPluginEndpoint(c, controller.RelayTask)
		},
	)

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth(), middleware.Distribute())
	{
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
	}

	// Doubao Asset API - per-user isolated asset operations.
	// Token auth + per-user rate limit; each user only sees/manages assets in their own auto-managed group.
	// Upstream's asset branch serves these under /doubao/open. That prefix now
	// belongs to the doubao task plugin: a host route under a plugin's key takes
	// the whole plugin off the public routes, Seedance generation included.
	doubaoAssetGroup := router.Group("/volc-asset")
	doubaoAssetGroup.Use(middleware.RouteTag("relay"))
	doubaoAssetGroup.Use(middleware.TokenAuth(), middleware.AssetRateLimit())
	{
		doubaoAssetGroup.POST("/ListAssets", controller.RelayListAssets)
		doubaoAssetGroup.POST("/GetAsset", controller.RelayGetAsset)
		doubaoAssetGroup.POST("/CreateAsset", controller.RelayCreateAsset)
		doubaoAssetGroup.POST("/UpdateAsset", controller.RelayUpdateAsset)
		doubaoAssetGroup.POST("/DeleteAsset", controller.RelayDeleteAsset)
	}

	// Doubao Asset Group management - admin only.
	// Regular users' groups are auto-managed; group CRUD is not exposed to them to preserve isolation.
	doubaoAssetGroupAdmin := router.Group("/volc-asset")
	doubaoAssetGroupAdmin.Use(middleware.RouteTag("relay"))
	doubaoAssetGroupAdmin.Use(middleware.TokenAuth(), middleware.AssetRateLimit(), middleware.AssetGroupAdminOnly())
	{
		doubaoAssetGroupAdmin.POST("/CreateAssetGroup", controller.RelayCreateAssetGroup)
		doubaoAssetGroupAdmin.POST("/ListAssetGroups", controller.RelayListAssetGroups)
		doubaoAssetGroupAdmin.POST("/GetAssetGroup", controller.RelayGetAssetGroup)
		doubaoAssetGroupAdmin.POST("/UpdateAssetGroup", controller.RelayUpdateAssetGroup)
		doubaoAssetGroupAdmin.POST("/DeleteAssetGroup", controller.RelayDeleteAssetGroup)
	}
}
