package worker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/spf13/viper"
)

// 全局变量
var (
	name         string
	totalCPU     float64
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

	finishedReqNums     int // 已经完成的请求数量
	finishedReqNumsLock sync.Mutex
	avgDelay            int

	charsets      = "abcdefghijklmnopqrstuvwxyz0123456789"
	delayTimeChan chan int
	sharedClient  *http.Client
)

// socket通信的消息载体
type WsMessage struct {
	Name               string    `json:"name"`
	Cores              int       `json:"cores"`
	TotalCPU           float64   `json:"total_cpu"`
	AllCPU             []float64 `json:"all_cpu"`
	IsWorking          bool      `json:"is_working"`
	RequestBashText    string    `json:"request_bash_text"`
	EnableRandomParams []string  `json:"enable_random_params"`
	UsingThreadsNums   int       `json:"using_threads_nums"`
	TotalRequestNums   int       `json:"total_request_nums"`
	TotalTime          int       `json:"total_time"` // 运行的最长时间限制

	StartWorkAt string `json:"start_work_at"`
	FinishRate  int    `json:"finish_rate"`
	AvgDelay    int    `json:"avg_delay"`
}

var dialer = websocket.Dialer{
	Proxy: http.ProxyFromEnvironment,
}

func InitWorker() {
	// 初始化操作
	initConnClient()
	delayTimeChan = make(chan int, 6000)

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
		panic(err)
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
			fmt.Println("error: ", err)
			time.Sleep(2 * time.Second)
			continue
		}
		io.Copy(io.Discard, resp.Body) // 读取Body 避免连接被标记为不可用
		resp.Body.Close()

		timeConsume := int(time.Since(startTime).Milliseconds())
		fmt.Printf("time consume: %4v ms | status: %3v \n", timeConsume, resp.StatusCode)
		finishedReqNumsLock.Lock()
		finishedReqNums++
		finishedReqNumsLock.Unlock()
		delayTimeChan <- timeConsume
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

// 初始化连接池
func initConnClient() {
	sharedClient = &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        500,
			MaxIdleConnsPerHost: 500,
			IdleConnTimeout:     60 * time.Second,
		},
		Timeout: 10 * time.Second,
	}
}

// 协程：定时更新数据并发送到主机
func sendLocalStatus() {
	for {
		if isConnected {
			totalCPU, allCPU = Get_CPU()
			finishedReqNumsLock.Lock()
			finishRate := int(float64(finishedReqNums) / float64(reqNums+finishedReqNums) * 100)
			finishedReqNumsLock.Unlock()
			if isWorking && len(delayTimeChan) >= workerNum {
				avgDelay = 0
				for i := 0; i < workerNum; i++ {
					avgDelay += <-delayTimeChan
				}
				avgDelay = int(avgDelay / workerNum)

				clearChan(delayTimeChan)
			}
			if isWorking {
				log.Printf("平均延迟: %4v ms , 完成率: %3v%% ", avgDelay, finishRate)
			}

			// 发送到主机
			msg := WsMessage{
				Name:        name,
				TotalCPU:    totalCPU,
				IsWorking:   isWorking,
				StartWorkAt: startWork_at,
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
		time.Sleep(2 * time.Second)
	}
}

// 协程：持续维持连接
func connect_host() {
	var err error
	for {
		if !isConnected {
			conn, _, err = dialer.Dial(u.String(), nil)
			if err != nil {
				log.Println("ws连接失败：", err)
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

// 读取请求文件的内容并返回字符串
func ReadFile() (string, error) {
	filePath := "text"
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %v", err)
	}
	return string(content), nil
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

// 将传入的字符串生成为一个md5码
func Str2md5(str string) string {
	str_1 := []byte(str)
	md5New := md5.New()
	md5New.Write(str_1)
	md5string := hex.EncodeToString(md5New.Sum(nil))
	return md5string
}

// 以随机数字生成一个md5值
func GetRandom_md5() string {
	temp := make([]byte, 15)
	for i := range temp {
		temp[i] = charsets[rand.Int()%len(charsets)]
	}
	temp_str := string(temp)
	return Str2md5(temp_str)
}

func Get_CPU() (float64, []float64) {
	totalCPU, _ := cpu.Percent(time.Second, false)
	allCPU, _ := cpu.Percent(time.Second, true)
	// totalCPU = fmt.Sprintf("%.4f", totalCPU)
	// for i := range allCPU {
	// 	allCPU[i] = fmt.Sprintf("%.4f", allCPU[i])
	// }
	return totalCPU[0], allCPU
}
