package worker

import (
	"bufio"
	"bytes"
	"context"
	"demo/utils"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
)

// 全局变量
var (
	name         string
	totalCPU     int
	allCPU       []float64
	isConnected  bool = false
	conn         *websocket.Conn
	host         string
	u            url.URL
	isWorking    bool = false
	startWork_at string

	reqNums     int           = -1 // 总请求数量
	reqNumsLock sync.Mutex         // 修改请求数量的锁
	totalTime   time.Duration      // 总时长
	workerNum   int           = 0  // 并发的请求数

	timeoutReqNums      int // 超时未完成的请求数量
	timeoutReqNumsLock  sync.Mutex
	finishedReqNums     int // 已经完成的请求数量
	finishedReqNumsLock sync.Mutex
	avgDelay            int

	delayTimeChan chan int     // 请求的耗时(ms)
	delayChanSize int      = 8 // 延迟时间通道大小
	sharedClient  *http.Client
)

// socket通信的消息载体
type WsMessage struct {
	Name               string    `json:"name"`
	Cores              int       `json:"cores"`
	TotalCPU           int       `json:"total_cpu"`
	AllCPU             []float64 `json:"all_cpu"`
	IsWorking          bool      `json:"is_working"`
	RequestBashText    string    `json:"request_bash_text"`
	EnableRandomParams []string  `json:"enable_random_params"`
	UsingThreadsNums   int       `json:"using_threads_nums"`
	TotalRequestNums   int       `json:"total_request_nums"`
	TotalTime          int       `json:"total_time"` // 运行的最长时间限制

	StartWorkAt string `json:"start_work_at"`
	TimeoutRate int    `json:"timeout_rate"`
	FinishRate  int    `json:"finish_rate"`
	AvgDelay    int    `json:"avg_delay"`
}

var dialer = websocket.Dialer{
	Proxy: http.ProxyFromEnvironment,
}

func InitWorker() {
	// 初始化操作
	initConnClient()

	viper.SetConfigName("config")
	viper.SetConfigType("json")
	viper.AddConfigPath("./")
	if err := viper.ReadInConfig(); err != nil {
		log.Fatal(err)
	}

	name = viper.GetString("name")
	remote_address := viper.GetString("remote_address")
	remote_port := viper.GetInt("remote_port")
	host = fmt.Sprintf("%v:%v", remote_address, remote_port)
	u = url.URL{
		Scheme:   "ws",
		Host:     host,
		Path:     "/master/myws",
		RawQuery: fmt.Sprintf("name=%v", name),
	}

	//协程-维持连接
	go connect_host()

	// 协程-定时更新本地数据并发送到主机
	go sendLocalStatus()

	//从socket中循环读取消息
	for {
		if isConnected {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("读取消息失败: ", err)
				conn.Close()
				isConnected = false
			}
			//处理收到的消息
			var msg WsMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				log.Println("消息解码错误: ", err)
				continue
			}

			// 若目标请求数据为空, 临时中止或继续任务
			if msg.RequestBashText == "" {
				if isWorking != msg.IsWorking {
					if msg.IsWorking {
						log.Println("开始任务")
						isWorking = true
						startWork_at = time.Now().Format("01-02 15:04:05")
						go startTasks()
					} else {
						log.Println("临时停止任务")
						isWorking = false
					}
				} else {
					log.Println("开始/停止 状态不变")
				}
			} else {
				// 否则为开启新任务
				isWorking = true
				startWork_at = time.Now().Format("01-02 15:04:05")
				// 保存请求数据到本地文件
				if err := WriteFile(msg.RequestBashText); err != nil {
					log.Println("保存请求数据失败: ", err)
					continue
				}
				// 更新本地全局变量
				reqNums = msg.TotalRequestNums
				finishedReqNums = 0
				timeoutReqNums = 0
				totalTime = time.Duration(msg.TotalTime) * time.Minute
				workerNum = msg.UsingThreadsNums
				log.Printf("工作启动信息：总请求：%v, 线程数：%v, 运行时间：%v", reqNums, workerNum, totalTime)
				go startTasks()
			}

		} else {
			time.Sleep(2 * time.Second)
		}
	}
}

// 开始任务
func startTasks() {
	// 读取文件，获取请求体数组
	file, err := os.Open("text")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	reqs, err := ParseCurlFileToRequest(file, workerNum)
	if err != nil {
		log.Printf("读取文件失败: %s\n 停止工作...", err.Error())
		isWorking = false
		return
	}
	// 启动工人
	wg := sync.WaitGroup{}
	myTimeCtx, cancel := context.WithTimeout(context.Background(), totalTime)
	defer cancel()
	finishStatus := "未知"
	for i := 0; i < workerNum; i++ {
		wg.Add(1)
		go worker(myTimeCtx, reqs[i], &wg, &finishStatus)
	}
	wg.Wait()
	log.Println("任务状态：", finishStatus)
	isWorking = false
}

// 单个工人协程
func worker(myTimeCtx context.Context, reqs *http.Request, wg *sync.WaitGroup, finishStatus *string) {
	defer wg.Done()
	for {
		if !isWorking {
			*finishStatus = "主动停止..."
			break
		}
		select {
		case <-myTimeCtx.Done():
			*finishStatus = "任务超时..."
			return
		default:
		}

		reqNumsLock.Lock()
		if reqNums <= 0 { // 请求数量为0，退出
			*finishStatus = "任务完成..."
			reqNumsLock.Unlock()
			break
		} else {
			reqNums--
		}
		reqNumsLock.Unlock()

		startTime := time.Now()
		resp, err := sharedClient.Do(reqs)
		if err != nil {
			// 请求失败：可能包括超时
			timeoutReqNumsLock.Lock()
			timeoutReqNums++
			timeoutReqNumsLock.Unlock()
			continue
		}
		io.Copy(io.Discard, resp.Body) // 读取Body 避免连接被标记为不可用
		resp.Body.Close()

		timeConsume := int(time.Since(startTime).Milliseconds())

		finishedReqNumsLock.Lock()
		finishedReqNums++
		finishedReqNumsLock.Unlock()
		// 非阻塞式数据写入通道
		select {
		case delayTimeChan <- timeConsume:
		default:
		}
	}
}

/*
ParseCurlFileToRequest 解析包含 curl 请求的文件，构造并返回 http.Request 对象
参数：num表示返回的请求个数
*/
func ParseCurlFileToRequest(file io.Reader, num int) ([]*http.Request, error) {
	scanner := bufio.NewScanner(file)

	var method = "GET"
	var rawURL string
	var bodyContent string
	headers := http.Header{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimSuffix(line, "\\")

		if strings.HasPrefix(line, "curl") {
			start := strings.Index(line, "'")
			end := strings.LastIndex(line, "'")
			if start != -1 && end != -1 && start < end {
				rawURL = line[start+1 : end] // TODO: 此处可增加url参数随机替换
			}
		} else if strings.HasPrefix(line, "-X") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				method = strings.Trim(parts[1], "'\"")
			}
		} else if strings.HasPrefix(line, "-H") {
			start := strings.Index(line, "'")
			end := strings.LastIndex(line, "'")
			if start == -1 || end == -1 || start >= end {
				continue
			}
			headerLine := line[start+1 : end]
			if !strings.Contains(headerLine, ":") {
				continue
			}
			parts := strings.SplitN(headerLine, ":", 2)
			headers.Add(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		} else if strings.HasPrefix(line, "-b") {
			start := strings.Index(line, "'")
			end := strings.LastIndex(line, "'")
			if start != -1 && end != -1 && start < end {
				cookieVal := line[start+1 : end]
				headers.Set("Cookie", cookieVal)
			}
		} else if strings.HasPrefix(line, "--data-raw") || strings.HasPrefix(line, "--data") {
			start := strings.Index(line, "'")
			end := strings.LastIndex(line, "'")
			if start != -1 && end != -1 && start < end {
				bodyContent = line[start+1 : end]
				method = "POST"
			}
		}
	}

	if rawURL == "" {
		return nil, fmt.Errorf("URL not found in curl command")
	}

	var requests []*http.Request
	for i := 0; i < num; i++ {
		var body io.Reader
		if bodyContent != "" {
			body = bytes.NewReader([]byte(bodyContent))
		}
		req, err := http.NewRequest(method, rawURL, body)
		if err != nil {
			return nil, err
		}

		// 复制 header
		req.Header = http.Header{}
		for k, v := range headers {
			copied := make([]string, len(v))
			copy(copied, v)
			req.Header[k] = copied
		}

		requests = append(requests, req)
	}

	return requests, nil
}

// 初始化连接池和其他初始化操作
func initConnClient() {
	delayTimeChan = make(chan int, delayChanSize)

	sharedClient = &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        10000,
			MaxIdleConnsPerHost: 10000,
			IdleConnTimeout:     60 * time.Second, // 空闲连接的维持时间
		},
		Timeout: 5 * time.Second, // 设置单次请求的超时时间
	}
}

// 协程：定时更新数据并发送到主机, 并日志打印状态
func sendLocalStatus() {
	for {
		if isConnected {
			totalCPU, allCPU = utils.Get_CPU()
			finishedReqNumsLock.Lock()
			timeoutRate := int(float64(timeoutReqNums) / float64(reqNums+finishedReqNums+timeoutReqNums) * 100)
			finishRate := int(float64(finishedReqNums) / float64(reqNums+finishedReqNums+timeoutReqNums) * 100)
			finishedReqNumsLock.Unlock()
			if isWorking && len(delayTimeChan) >= delayChanSize/2 {
				length := len(delayTimeChan)
				avgDelay = 0
				for range length {
					avgDelay += <-delayTimeChan
				}
				avgDelay = int(avgDelay / length)

				clearChan(delayTimeChan)
			}

			if isWorking {
				log.Printf("平均延迟:%4vms  超时率:%3v%%  完成率:%3v%% ", avgDelay, timeoutRate, finishRate)
			}

			// 发送到主机
			msg := WsMessage{
				Name:        name,
				TotalCPU:    totalCPU,
				IsWorking:   isWorking,
				StartWorkAt: startWork_at,
				TimeoutRate: timeoutRate,
				FinishRate:  finishRate,
				AvgDelay:    avgDelay,
			}
			jsonData, err := json.Marshal(msg)
			if err != nil {
				log.Println("编码消息失败: ", err)
				continue
			}
			if err := conn.WriteMessage(websocket.TextMessage, jsonData); err != nil {
				log.Println("发送消息失败: ", err)
				isConnected = false
			}
		}
		time.Sleep(2000 * time.Millisecond)
	}
}

// 协程：持续维持连接
func connect_host() {
	var err error
	for {
		if !isConnected {
			conn, _, err = dialer.Dial(u.String(), nil)
			if err != nil {
				log.Println("ws连接失败: ", err)
				time.Sleep(4 * time.Second)
			} else {
				log.Println("ws连接成功")
				isConnected = true
			}
		} else {
			time.Sleep(2 * time.Second)
		}
	}
}

// 将请求文本写入文件text中
func WriteFile(content string) error {
	filePath := "text" //二进制文件读取该路径，因此创建的文件在项目根目录下
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入文件失败: %v", err)
	}
	return nil
}

// 清空chan
func clearChan(ch chan int) {
	for {
		select {
		case <-ch:
			// 读出一个元素
		default:
			// channel 已空，退出
			return
		}
	}
}
