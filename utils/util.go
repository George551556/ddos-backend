package utils

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
)

var (
	charsets = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// 读取请求文件的内容并返回字符串
func ReadFile() (string, error) {
	filePath := "text"
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %v", err)
	}
	return string(content), nil
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

func Get_CPU() (int, []float64) {
	totalCPU, _ := cpu.Percent(2*time.Second, false)
	allCPU, _ := cpu.Percent(time.Second, true)
	// totalCPU = fmt.Sprintf("%.4f", totalCPU)
	// for i := range allCPU {
	// 	allCPU[i] = fmt.Sprintf("%.4f", allCPU[i])
	// }
	return int(totalCPU[0]), allCPU
}
