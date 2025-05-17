package main

import (
	"demo/front"
	"demo/master"
	"demo/utils"
	"demo/worker"
	"log"
	"os"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	args := os.Args
	if len(args) != 1 {
		// master mode
		log.Println("master mode...")

		// 数据库初始化
		utils.InitDB()

		r := gin.Default()
		r.Use(cors.Default()) // 允许跨域
		front.InitFrontAPI(r)
		master.InitMaster(r)
		r.Run(":55155")
	} else {
		// worker mode
		log.Println("worker mode...")
		worker.InitWorker()
	}
}
