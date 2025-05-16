package master

import (
	"demo/utils"
	"demo/worker"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

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
	Ws          *websocket.Conn
	Id          string `json:"id"`
	Name        string `json:"name"`
	TotalCPU    int    `json:"total_cpu"`
	StartWorkAt string `json:"start_work_at"`
	IsWorking   bool   `json:"is_working"`
	TimeoutRate int    `json:"timeout_rate"` // 超时的请求比例
	FinishRate  int    `json:"finish_rate"`  // 完成了总请求的比例
	AvgDelay    int    `json:"avg_delay"`    // 访问目标服务的平均延迟(最近10个请求)
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
	id := utils.GetRandom_md5()
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
			_, ok := nodeInfos[id]
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
			tempNode := node{
				Ws:          ws,
				Id:          id,
				Name:        msg.Name,
				TotalCPU:    msg.TotalCPU,
				StartWorkAt: msg.StartWorkAt,
				IsWorking:   msg.IsWorking,
				TimeoutRate: msg.TimeoutRate,
				FinishRate:  msg.FinishRate,
				AvgDelay:    msg.AvgDelay,
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

// 返回所有节点信息的数组(前端接口调用)
func HandleGetAllNodeInfos() []node {
	nodeLock.Lock()
	defer nodeLock.Unlock()
	nodeInfosArray := []node{}
	for _, node := range nodeInfos {
		nodeInfosArray = append(nodeInfosArray, node)
	}
	sort.Slice(nodeInfosArray, func(i, j int) bool {
		return nodeInfosArray[i].Id < nodeInfosArray[j].Id
	})
	return nodeInfosArray
}

// 启动所有设备执行新任务
func StartNewTaskAll(reqBashText string, enableRandomParams []string, totalRequestNums, usingThreadNums, timeConstraint int) error {
	log.Println("启动所有设备执行新任务")
	// 停止所有设备任务
	err := SwitchDeviceAll(false, "")
	if err != nil {
		return err
	}
	// 编辑发送数据
	msg := worker.WsMessage{
		RequestBashText:    reqBashText,
		EnableRandomParams: enableRandomParams,
		TotalRequestNums:   totalRequestNums,
		UsingThreadsNums:   usingThreadNums,
		TotalTime:          timeConstraint,
		IsWorking:          true,
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		log.Println("编码消息失败: ", err)
		return err
	}

	nodeLock.Lock()
	defer nodeLock.Unlock()
	// 遍历所有节点并发送消息
	for _, node := range nodeInfos {
		if err := node.Ws.WriteMessage(websocket.TextMessage, jsonData); err != nil {
			log.Println("启动新任务发送失败: ", node.Name, err)
			return err
		}
	}
	return nil
}

// 切换设备的启停状态
// id 为空字符串则操作所有设备
func SwitchDeviceAll(isWorking bool, id string) error {
	msg := worker.WsMessage{IsWorking: isWorking}
	jsonData, err := json.Marshal(msg)
	if err != nil {
		log.Println("编码消息失败: ", err)
		return err
	}
	nodeLock.Lock()
	defer nodeLock.Unlock()
	if id == "" {
		for _, node := range nodeInfos {
			if err := node.Ws.WriteMessage(websocket.TextMessage, jsonData); err != nil {
				log.Println("临时停止设备发送失败: ", node.Name, err)
				return err
			}
		}
		return nil
	} else {
		if node, ok := nodeInfos[id]; ok {
			if err := node.Ws.WriteMessage(websocket.TextMessage, jsonData); err != nil {
				log.Println("临时停止设备发送失败: ", node.Name, err)
				return err
			}
			return nil
		} else {
			log.Println("临时停止设备失败: ", id, "不存在")
			return err
		}
	}
}

// 单次访问测试（由后端服务对被攻击目标进行一次访问，并返回状态码、延迟和响应体到前端）
func SingleAttack(reqBashText string) (int, int, string, error) {
	file := strings.NewReader(reqBashText) // 跳过写入文件的步骤，避免污染原有数据
	reqs, err := worker.ParseCurlFileToRequest(file, 1)
	if err != nil {
		return 0, 0, "", err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	startTime := time.Now()
	resp, err := client.Do(reqs[0])
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()
	timeConsume := int(time.Since(startTime).Milliseconds())
	bodyContent, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, "", err
	}
	return resp.StatusCode, timeConsume, string(bodyContent), nil
}
