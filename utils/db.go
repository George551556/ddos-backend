package utils

import (
	"fmt"
	"time"

	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var (
	db  *gorm.DB
	err error
	dsn = "root:123456789@tcp(127.0.0.1:3306)/todolist?charsetutf8mb4&parseTime=true&loc=Local"
)

type DdosRecord struct {
	gorm.Model
	Abstract string `gorm:"abstract"`
	BashText string `gorm:"bash_text"`
} // 在数据库中表名会自动转换为小写蛇形复数

// 创建表，只调用一次
func Db_makeTable() {
	db, err = gorm.Open(mysql.Open(dsn))
	if err != nil {
		panic(err)
	}
	//迁移表
	db.AutoMigrate(&DdosRecord{})

	//连接数
	sqlDB, err1 := db.DB()
	sqlDB.SetMaxIdleConns(3)
	sqlDB.SetMaxOpenConns(3)
	sqlDB.SetConnMaxLifetime(time.Minute * 5)
	if err1 != nil {
		panic("连接数据库失败")
	}

	fmt.Println("tables make succs")
}

// 初始化连接数据库连接
func InitDB() error {
	db, err = gorm.Open(mysql.Open(dsn))
	if err != nil {
		return fmt.Errorf("数据库连接失败: %v", err)
	}
	log.Println("数据库连接成功...")

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取底层 DB 对象失败: %v", err)
	}
	// 设置连接池参数
	sqlDB.SetMaxIdleConns(2)                  // 最大空闲连接数
	sqlDB.SetMaxOpenConns(2)                  // 最大打开连接数
	sqlDB.SetConnMaxLifetime(time.Minute * 5) // 连接的最大生命周期
	return nil
}

// 添加一条数据
func Db_CreateRecord(abstract string, bashText string) error {
	// 比较与最新部分数据是否相同
	newestData, err := QueryNewestData()
	if err != nil {
		return err
	}
	for _, item := range newestData {
		if abstract == item.Abstract {
			return fmt.Errorf("数据已存在，不再添加")
		}
	}

	msg := DdosRecord{
		Abstract: abstract,
		BashText: bashText,
	}

	if err := db.Create(&msg).Error; err != nil {
		return fmt.Errorf("添加一条数据失败: %v", err)
	}

	return nil
}

// 查询数据记录（分页查询）
func Db_QueryPaginateRecords(page int, pageSize int) ([]DdosRecord, error) {
	time.Sleep(250 * time.Millisecond)
	var msg []DdosRecord

	offset := (page - 1) * pageSize
	err := db.Order("id desc").Offset(offset).Limit(pageSize).Find(&msg).Error
	if err != nil {
		return nil, fmt.Errorf("分页查询失败: %v", err)
	}

	return msg, nil
}

// 查询最新的10条数据：用于在新增数据时比对abstract是否有相同，相同则不添加
func QueryNewestData() ([]DdosRecord, error) {
	var msg []DdosRecord
	if err := db.Order("id desc").Limit(10).Find(&msg).Error; err != nil {
		return []DdosRecord{}, fmt.Errorf("查询数据失败: %v", err)
	}
	return msg, nil
}

// 测试：查询所有数据
func Db_queryAll() ([]DdosRecord, error) {
	var msg []DdosRecord
	err := db.Order("id desc").Find(&msg).Error
	if err != nil {
		return nil, fmt.Errorf("查询所有数据失败: %v", err)
	}
	return msg, nil
}

func ShowRecords(msg []DdosRecord) {
	if len(msg) == 0 {
		fmt.Println("没有数据")
		return
	}
	for _, record := range msg {
		fmt.Printf("ID: %4v", record.ID)
		fmt.Println(" 摘要: ", record.Abstract)
	}
}
