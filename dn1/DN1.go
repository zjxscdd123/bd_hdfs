package main

import "hdfs/hdfs"

// import "tidydfs/tdfs"

// "fmt"
// "tdfs"
// "runtime"
// "sync"
const DN1_DIR string = "./dn1/datanode"
const DN1_LOCATION string = "http://localhost:11091"
const DN1_CAPACITY int = 400

func main() {
	var dn1 hdfs.DataNode
	dn1.DATANODE_DIR = DN1_DIR
	dn1.Reset()
	// 位置  容量
	dn1.SetConfig(DN1_LOCATION, DN1_CAPACITY)
	dn1.Run()
}
