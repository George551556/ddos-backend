package utils

import (
	"testing"
)

func TestCreateTable(t *testing.T) {
	Db_makeTable()
}

func TestCreateRecord(t *testing.T) {
	err := InitDB()
	if err != nil {
		t.Error(err)
	}
	err = Db_CreateRecord("test-051711", "test32453848g345g45g\n23rq324r3434gw45g45\n")
	if err != nil {
		t.Error(err)
	}
}

func TestQueryAll(t *testing.T) {
	InitDB()
	msg, err := Db_queryAll()
	if err != nil {
		t.Error(err)
	}
	ShowRecords(msg)
}

func TestPaginateQuery(t *testing.T) {
	InitDB()
	msg, _ := Db_QueryPaginateRecords(1, 3)
	ShowRecords(msg)
}
