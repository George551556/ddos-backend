package master

import (
	"demo/worker"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域请求
	},
}

// 全局变量
var (
	totalWorkerNums = 0
	nodeInfos       = make(map[string]node)
	nodeLock        sync.Mutex
)

type node struct {
	Ws         *websocket.Conn
	Id         string  `json:"id"`
	Name       string  `json:"name"`
	IsWorking  bool    `json:"is_working"`
	FinishRate float64 `json:"finish_rate"` // 完成了总请求的比例
	AvgDelay   int     `json:"avg_delay"`   // 访问目标服务的平均延迟(最近10s)
}

func InitMaster(r *gin.Engine) {
	master := r.Group("/master")
	master.GET("/myws", myWS)
}

// 处理socket连接请求
func myWS(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		c.JSON(400, gin.H{"message": err.Error()})
	}
	//保存连接对象
	name := c.Query("name") // !!!!!!
	id := worker.GetRandom_md5()
	nodeLock.Lock()
	nodeInfos[id] = node{
		Ws:   ws,
		Id:   id,
		Name: name,
	}
	totalWorkerNums++
	log.Printf("工人 %v 上线...\n", name)
	nodeLock.Unlock()

	//开协程持续接收消息
	go func() {
		for {
			tempNode, ok := nodeInfos[id]
			if !ok {
				memberOut(id)
				break
			}

			// 读取消息
			_, message, err := ws.ReadMessage()
			if err != nil {
				log.Println("读取消息失败: ", err)
				memberOut(id)
				break
			}

			var msg worker.WsMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				log.Println("解码消息失败")
				memberOut(id)
				break
			}

			// 更新节点信息
			tempNode = node{
				Name:       msg.Name,
				IsWorking:  msg.IsWorking,
				FinishRate: msg.FinishRate,
				AvgDelay:   msg.AvgDelay,
			}
			nodeLock.Lock()
			nodeInfos[id] = tempNode
			nodeLock.Unlock()
		}
	}()
}

// 删除map中信息并更新相关全局变量
func memberOut(id string) {
	log.Printf("工人%v下线...\n", nodeInfos[id].Name)
	nodeInfos[id].Ws.Close()
	nodeLock.Lock()
	delete(nodeInfos, id)
	totalWorkerNums--
	nodeLock.Unlock()
}
