package front

import (
	"demo/master"
	"time"

	"github.com/gin-gonic/gin"
)

func InitFrontAPI(r *gin.Engine) {
	front := r.Group("/front")
	front.GET("/", handleRoot)
	front.GET("/ping", handlePing)
	front.GET("/clock", getClockTime)
	front.GET("/queryDevices", queryDevices)

}

func handleRoot(c *gin.Context) {
	c.String(200, "前后端分离，请访问前端端口")
}

func handlePing(c *gin.Context) {
	c.String(200, "pong")
}

func getClockTime(c *gin.Context) {
	c.JSON(200, gin.H{
		"message": time.Now().Format("15:04:05"),
	})
}

func queryDevices(c *gin.Context) {
	devices := master.HandleGetAllNodeInfos()
	deviceNums := len(devices)
	c.JSON(200, gin.H{
		"message":    "",
		"deviceNums": deviceNums,
		"data":       devices,
	})
}
