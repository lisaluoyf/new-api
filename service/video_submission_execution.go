package service

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

type videoSubmissionExecutionKey struct{}

// BeginVideoSubmissionExecution is called only after a fresh durable claim.
// All downstream material I/O, adapter calls and persistence share one bounded
// execution, independent of the connection that delivered the request. Replays
// never enter it. Restore the transport context for outer logging middleware.
func BeginVideoSubmissionExecution(c *gin.Context) func() {
	original := c.Request
	ctx, cancel := context.WithTimeout(context.WithoutCancel(original.Context()), 5*time.Minute)
	ctx = context.WithValue(ctx, videoSubmissionExecutionKey{}, true)
	c.Request = original.WithContext(ctx)
	return func() {
		cancel()
		c.Request = original
	}
}

// VideoSubmissionExecutionActive prevents adapter helpers from discarding the
// shared execution deadline when creating their per-operation contexts.
func VideoSubmissionExecutionActive(c *gin.Context) bool {
	active, _ := c.Request.Context().Value(videoSubmissionExecutionKey{}).(bool)
	return active
}
