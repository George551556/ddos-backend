package front

import (
	"demo/master"
	"demo/utils"
	"demo/worker"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

// 前后端通信的消息载体
type FrontMessage struct {
	// 表单信息
	RequestBashText    string   `json:"request_bash_text"`
	EnableRandomParams []string `json:"enable_random_params"` // 需要在url中随机化的param名称列表
	TotalRequestNums   int      `json:"total_request_nums"`
	UsingThreadsNums   int      `json:"using_threads_nums"`
	TimeConstraint     int      `json:"time_constraint"`

	// 控制信息
	IsWorking bool   `json:"is_working"`
	DeviceId  string `json:"device_id"` // 控制目标设备的id，若为空，则控制所有设备
}

func InitFrontAPI(r *gin.Engine) {
	front := r.Group("/front")
	front.GET("/ping", handlePing)
	front.GET("/clock", getClockTime)
	front.GET("/queryDevices", queryDevices)
	front.GET("/getDefaultRequestBashText", getDefaultRequestBashText)

	front.POST("/startTaskAll", startTaskAll)
	front.POST("/switchDeviceAll", switchDeviceAll)
	front.POST("/singleAttack", handleSingleAttack)

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

// 启动所有设备(新)任务
func startTaskAll(c *gin.Context) {
	// 解析数据
	var msg FrontMessage
	if err := c.ShouldBindJSON(&msg); err != nil {
		c.JSON(400, gin.H{
			"message": fmt.Sprintf("绑定数据失败: %v", err),
		})
		return
	}

	// 保存请求数据到本地
	if err := worker.WriteFile(msg.RequestBashText); err != nil {
		c.JSON(400, gin.H{
			"message": fmt.Sprintf("保存请求数据失败: %v", err),
		})
		return
	}

	// 启动所有设备
	if err := master.StartNewTaskAll(msg.RequestBashText, msg.EnableRandomParams, msg.TotalRequestNums, msg.UsingThreadsNums, msg.TimeConstraint); err != nil {
		c.JSON(400, gin.H{
			"message": fmt.Sprintf("启动所有设备失败: %v", err),
		})
		return
	}

	c.JSON(200, gin.H{})
}

// 切换所有设备工作状态
// 两个参数：isWorking: 是否启动工作状态，deviceId: 控制目标设备的id，若为空，则控制所有设备
func switchDeviceAll(c *gin.Context) {
	var msg FrontMessage
	if err := c.ShouldBindJSON(&msg); err != nil {
		c.JSON(400, gin.H{
			"message": fmt.Sprintf("绑定数据失败: %v", err),
		})
	}
	if err := master.SwitchDeviceAll(msg.IsWorking, msg.DeviceId); err != nil {
		c.JSON(400, gin.H{
			"message": fmt.Sprintf("切换设备失败: %v", err),
		})
	}
}

// 获取后端默认保存的请求数据
func getDefaultRequestBashText(c *gin.Context) {
	content, err := utils.ReadFile()
	if err != nil {
		c.JSON(400, gin.H{
			"message": fmt.Sprintf("读取后端默认请求数据失败: %v", err),
		})
		return
	}
	c.JSON(200, gin.H{
		"message": "",
		"data":    content,
	})
}

// 单次访问目标，以测试接口可用性
func handleSingleAttack(c *gin.Context) {
	var msg struct {
		RequestBashText string `json:"request_bash_text"`
	}
	if err := c.ShouldBindJSON(&msg); err != nil {
		c.JSON(400, gin.H{
			"message": fmt.Sprintf("解析请求数据失败: %v", err),
		})
		return
	}
	statusCode, delayTime, respBody, err := master.SingleAttack(msg.RequestBashText)
	if err != nil {
		c.JSON(400, gin.H{
			"message": fmt.Sprintf("单次攻击失败: %v", err),
		})
		return
	}
	c.JSON(200, gin.H{
		"status_code": statusCode,
		"delay_time":  delayTime,
		"resp_body":   respBody,
	})
}
