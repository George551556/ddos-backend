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
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
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

	reqNums           int           = -1    // 总请求数量
	reqNumsLock       sync.Mutex            // 修改请求数量的锁
	totalTime         time.Duration         // 总时长
	workerNum         int           = 0     // 并发的请求数
	workerRandomList  []string              // 工人获得的随机参数列表, 其中可能存在 value 或者 key=value 形式的字符串
	requestStatusCode string        = "未工作" // 对目标请求的状态码

	timeoutReqNums      int // 超时未完成的请求数量
	timeoutReqNumsLock  sync.Mutex
	finishedReqNums     int // 已经完成的请求数量
	finishedReqNumsLock sync.Mutex
	avgDelay            int

	showStatusSignal chan bool     // 显示状态的信号，工人若接收到该通道信号则当前请求后输出结果
	delayTimeChan    chan int      // 请求的耗时(ms)
	delayChanSize    int       = 8 // 延迟时间通道大小
	sharedClient     *http.Client
)

// socket通信的消息载体
type WsMessage struct {
	Name             string    `json:"name"`
	Cores            int       `json:"cores"`
	TotalCPU         int       `json:"total_cpu"`
	AllCPU           []float64 `json:"all_cpu"`
	IsWorking        bool      `json:"is_working"`
	RequestBashText  string    `json:"request_bash_text"`
	RandomList       []string  `json:"random_list"`
	UsingThreadsNums int       `json:"using_threads_nums"`
	TotalRequestNums int       `json:"total_request_nums"`
	TotalTime        int       `json:"total_time"`       // 运行的最长时间限制
	RequestTimeout   string    `json:"request_time_out"` // 单位: s 单次请求超时时间

	StartWorkAt       string `json:"start_work_at"`
	TimeoutRate       int    `json:"timeout_rate"`
	FinishRate        int    `json:"finish_rate"`
	AvgDelay          int    `json:"avg_delay"`
	RequestStatusCode string `json:"request_status_code"` // 对目标请求的状态码
}

var dialer = websocket.Dialer{
	Proxy: http.ProxyFromEnvironment,
}

func InitWorker() {
	// 初始化操作
	initConnClient()

	showStatusSignal = make(chan bool, 1)

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
			} else { // 否则为开启新任务
				if isWorking == true {
					log.Println("上一任务正在进行，暂不进行新任务")
					continue
				}

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

				reqTimeoutNum, err := strconv.ParseFloat(msg.RequestTimeout, 32)
				if err != nil {
					log.Println("请求超时参数转换失败: ", err)
					reqTimeoutNum = 1
					continue
				}
				sharedClient.Timeout = time.Duration(reqTimeoutNum*1000) * time.Millisecond

				workerNum = msg.UsingThreadsNums
				workerRandomList = msg.RandomList
				log.Printf("工作启动信息：总请求：%v, 线程数：%v, 超时：%v 运行时间：%v \n 随机参数：%v", reqNums, workerNum, totalTime, msg.RequestTimeout, workerRandomList)
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
		time.Sleep(5 * time.Millisecond)
	}
	wg.Wait()

	// 将工作中设置的变量或参数恢复到初始状态
	log.Println("任务状态：", finishStatus)
	isWorking = false
	requestStatusCode = "未工作"
}

// 单个工人协程
func worker(myTimeCtx context.Context, reqs *http.Request, wg *sync.WaitGroup, finishStatus *string) {
	defer wg.Done()
	var outputLog bool
	for {
		outputLog = false
		if !isWorking {
			*finishStatus = "主动停止..."
			break
		}
		select {
		case <-myTimeCtx.Done():
			*finishStatus = "任务超时..."
			return
		case <-showStatusSignal:
			outputLog = true
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

		// 在每次发送请求前，复制原始请求并重新设置请求体
		req := reqs.Clone(myTimeCtx) // 使用 Clone 方法复制请求，并传入上下文
		if reqs.Body != nil {        // 检查原始请求是否有请求体
			/*
				post请求的body经处理后是一个流式对象，被读取一次就会失效，因此需要每次复制原始请求体，
				并复制及设置请求体body
			*/
			newBody, err := reqs.GetBody()
			if err != nil {
				log.Printf("Failed to get new body for request: %v", err)
				continue
			}
			req.Body = newBody
		}

		startTime := time.Now()
		resp, err := sharedClient.Do(req)
		if err != nil {
			// 请求失败：可能包括超时
			timeoutReqNumsLock.Lock()
			timeoutReqNums++
			timeoutReqNumsLock.Unlock()

			if outputLog {
				requestStatusCode = "请求失败"
				log.Println("请求结果展示：请求失败：", err)
			}
			continue
		}
		io.Copy(io.Discard, resp.Body) // 读取Body 避免连接被标记为不可用
		resp.Body.Close()

		timeConsume := int(time.Since(startTime).Milliseconds())

		finishedReqNumsLock.Lock()
		finishedReqNums++
		finishedReqNumsLock.Unlock()

		if outputLog {
			requestStatusCode = resp.Status
			log.Println("请求结果展示：请求成功...")
		}

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
				rawURL = line[start+1 : end]
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
		var newRawURL string = rawURL
		var newBodyContent string = bodyContent
		newHeader := http.Header{}

		// 对rawURL和bodyContent中涉及字段进行随机替换，对第一个请求不做随机化处理
		if i == 0 {
			for k, v := range headers {
				copied := make([]string, len(v))
				copy(copied, v)
				newHeader[k] = copied
			}
		} else {
			newRawURL = replaceWithRandom(newRawURL)
			newBodyContent = replaceWithRandom(newBodyContent)
			for k, v := range headers {
				copied := make([]string, len(v))
				copy(copied, v)
				for j, val := range copied {
					copied[j] = replaceWithRandom(val)
				}
				newHeader[k] = copied
			}
		}

		var body io.Reader
		if bodyContent != "" {
			body = bytes.NewReader([]byte(newBodyContent))
		}
		req, err := http.NewRequest(method, newRawURL, body)
		if err != nil {
			return nil, err
		}

		// 复制 header
		req.Header = newHeader

		requests = append(requests, req)
	}

	return requests, nil
}

// 初始化连接池和其他初始化操作
func initConnClient() {
	delayTimeChan = make(chan int, delayChanSize)
	connNums := 8000 // 设置连接池的最大连接数

	sharedClient = &http.Client{
		Transport: &http.Transport{
			MaxConnsPerHost:     connNums,
			MaxIdleConns:        connNums,
			MaxIdleConnsPerHost: connNums,
			IdleConnTimeout:     15 * time.Second, // 空闲连接的维持时间
			DisableKeepAlives:   true,             // 禁止连接复用
		},
		Timeout: 6 * time.Second, // 设置单次请求的超时时间
	}
}

// 协程：定时更新数据并发送到主机, 并日志打印状态
func sendLocalStatus() {
	for {
		if isConnected {
			totalCPU, allCPU = utils.Get_CPU()

			timeoutRate := int(float64(timeoutReqNums) / float64(reqNums+finishedReqNums+timeoutReqNums) * 100)
			finishRate := int(float64(finishedReqNums) / float64(reqNums+finishedReqNums+timeoutReqNums) * 100)

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
				Name:              name,
				TotalCPU:          totalCPU,
				IsWorking:         isWorking,
				StartWorkAt:       startWork_at,
				TimeoutRate:       timeoutRate,
				FinishRate:        finishRate,
				AvgDelay:          avgDelay,
				RequestStatusCode: requestStatusCode,
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

			showStatusSignal <- true
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

// 工具函数: 将传入的target字符串存在的workerRandomList中的字符串进行同类型的随机替换
func replaceWithRandom(target string) string {
	for _, randomStr := range workerRandomList {
		if strings.Contains(target, randomStr) {
			var newRandomStr string
			if strings.Contains(randomStr, "=") {
				key, value := strings.Split(randomStr, "=")[0], strings.Split(randomStr, "=")[1]
				newRandomStr = key + "=" + sameRandomReplace(value)
			} else {
				newRandomStr = sameRandomReplace(randomStr)
			}

			target = strings.Replace(target, randomStr, newRandomStr, 1)
		}
	}
	return target
}

// 工具函数: 将传入的字符串进行同样形式的随机替换
// 输入：12345 返回同样长度随机串83921
// 输入：abc23d  返回同样长度随机串9c2d32
func sameRandomReplace(target string) string {
	rand.Seed(time.Now().UnixNano())

	reg_number := regexp.MustCompile(`^\d+$`)                 // 纯数字
	reg_item := regexp.MustCompile(`^[A-Za-z]+$`)             // 字母
	ret_numberAndItem := regexp.MustCompile(`^[A-Za-z0-9]+$`) // 数字和字母

	if reg_number.MatchString(target) {
		temp := make([]byte, len(target))
		charset := "0123456789"
		for i := range temp {
			temp[i] = charset[rand.Intn(len(charset))]
		}
		return string(temp)
	} else if reg_item.MatchString(target) {
		temp := make([]byte, len(target))
		charset := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
		for i := range temp {
			temp[i] = charset[rand.Intn(len(charset))]
		}
		return string(temp)
	} else if ret_numberAndItem.MatchString(target) {
		temp := make([]byte, len(target))
		charset := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
		for i := range temp {
			temp[i] = charset[rand.Intn(len(charset))]
		}
		return string(temp)
	} else { // 可能包含多种字符类型的字符串，直接乱随机生成
		temp := make([]byte, len(target))
		for i := range temp {
			temp[i] = byte(rand.Intn(95) + 32)
		}
		return string(temp)
	}
}
