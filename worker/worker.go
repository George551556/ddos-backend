package worker

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v3/cpu"
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

	charsets = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// socket通信的消息载体
type WsMessage struct {
	Name             string    `json:"name"`
	Cores            int       `json:"cores"`
	TotalCPU         float64   `json:"total_cpu"`
	AllCPU           []float64 `json:"all_cpu"`
	IsWorking        bool      `json:"is_working"`
	UsingThreadsNums int       `json:"using_threads_nums"`
	TotalRequestNums int       `json:"total_request_nums"`
	StartWorkAt      string    `json:"start_work_at"`
	FinishRate       int       `json:"finish_rate"`
	AvgDelay         int       `json:"avg_delay"`
}

var dialer = websocket.Dialer{
	Proxy: http.ProxyFromEnvironment,
}

func InitWorker() {
	// TODO: viper read data viper.GetString("example")
	name = "test_123"
	remote_address := "127.0.0.1"
	remote_port := "55155"
	host = fmt.Sprintf("%v:%v", remote_address, remote_port)
	u = url.URL{
		Scheme:   "ws",
		Host:     host,
		Path:     "/master/myws",
		RawQuery: fmt.Sprintf("name=%v", name),
	}
	totalCPU, allCPU = Get_CPU()

	//协程：持续请求连接以及发送信息
	go func() {
		for {
			if !isConnected {
				if connect_host() {
					continue
				}
				time.Sleep(4 * time.Second)
			} else {
				// 持续发送信息
				fmt.Println("发送信息")
				time.Sleep(time.Second)
			}
		}
	}()

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
			}
			fmt.Println(msg)
		} else {
			time.Sleep(1 * time.Second)
		}
	}
}

// 请求一次连接
func connect_host() bool {
	var err error
	conn, _, err = dialer.Dial(u.String(), nil)
	if err != nil {
		log.Println("ws连接失败：", err)
		return false
	}
	log.Println("连接成功！！！")
	isConnected = true
	return true
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
