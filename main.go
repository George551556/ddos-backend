package main

import (
	"demo/front"
	"demo/master"
	"demo/worker"
	"fmt"
	"os"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	args := os.Args
	if len(args) != 1 {
		fmt.Println("master mode...")
		r := gin.Default()
		r.Use(cors.Default()) // 允许跨域
		front.InitFrontAPI(r)
		master.InitMaster(r)
		r.Run(":55155")
	} else {
		// worker mode
		fmt.Println("worker mode...")
		worker.InitWorker()
	}
}
