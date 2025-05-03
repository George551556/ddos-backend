package front

import (
	"time"

	"github.com/gin-gonic/gin"
)

func InitFrontAPI(r *gin.Engine) {
	front := r.Group("/front")
	front.GET("/", handleRoot)
	front.GET("/ping", handlePing)
	front.GET("/clock", getClockTime)

}

func handleRoot(c *gin.Context) {
	c.String(200, "Hello World")
}

func handlePing(c *gin.Context) {
	c.String(200, "pong")
}

func getClockTime(c *gin.Context) {
	c.JSON(200, gin.H{
		"message": time.Now().Format("15:04:05"),
	})
}
