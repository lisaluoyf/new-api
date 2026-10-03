package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func SetTaskWebhookRouter(r *gin.Engine) {
	api := r.Group("/v1", middleware.RouteTag("relay"), middleware.TokenOrUserAuth())
	api.POST("/webhook-endpoints", controller.CreateTaskWebhookEndpoint)
	api.GET("/webhook-endpoints", controller.ListTaskWebhookEndpoints)
	api.POST("/webhook-endpoints/:endpoint_id/verify", controller.VerifyTaskWebhookEndpoint)
	api.PATCH("/webhook-endpoints/:endpoint_id", controller.UpdateTaskWebhookEndpoint)
	api.POST("/webhook-endpoints/:endpoint_id/rotate-secret", controller.RotateTaskWebhookSecret)
	api.GET("/webhook-events", controller.ListTaskWebhookEvents)
	api.GET("/webhook-events/:event_id", controller.GetTaskWebhookEvent)
	api.GET("/webhook-events/:event_id/deliveries", controller.GetTaskWebhookEvent)
	api.POST("/webhook-events/:event_id/redeliver", controller.RedeliverTaskWebhookEvent)
	api.GET("/webhook-capabilities", controller.TaskWebhookCapabilities)
	api.GET("/tasks/:task_id/outputs/:output_id/content", controller.TaskWebhookOutput)
}
