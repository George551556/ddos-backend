package main

import (
	"demo/front"
	"demo/master"
	"demo/worker"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	args := os.Args
	if len(args) != 1 {
		fmt.Println("master mode...")
		r := gin.Default()
		front.InitFrontAPI(r)
		master.InitMaster(r)
		r.Run(":55155")
	} else {
		// worker mode
		fmt.Println("worker mode...")
		worker.InitWorker()
	}
}
