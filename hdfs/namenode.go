package hdfs

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"strconv"
	"strings"
	"time"
)

/*
	未添加功能：
		防止重复
*/
func (namenode *NameNode) Run() {
	router := gin.Default()
	router.Use(MwPrometheusHttp)
	// register the `/metrics` route.
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	router.GET("/leader", func(c *gin.Context) {
		c.String(http.StatusOK, namenode.LeaderLocation)
	})

	router.POST("/nn_heartbeat", func(c *gin.Context) {
		b, _ := c.GetRawData() // 从c.Request.Body读取请求数据
		if len(b) == 0 {
			fmt.Println("empty data")
			c.JSON(http.StatusBadRequest, nil)
			return
		}
		heartBeat := &NNHeartBeat{}
		if err := json.Unmarshal(b, heartBeat); err != nil {
			fmt.Println("unmarshal heartbeat data error")
			c.JSON(http.StatusBadRequest, nil)
			return
		}
		fmt.Printf("receive heartbeat=%+v\n", heartBeat)
		fmt.Printf("editLog=%+v\n", heartBeat.EditLog)
		if heartBeat.Term < namenode.Term {
			fmt.Println("term is too low")
			c.JSON(http.StatusBadRequest, nil)
			return
		} else if heartBeat.Term > namenode.Term {
			fmt.Println("bigger term")
			namenode.Term = heartBeat.Term
			namenode.IsLeader = false
			namenode.LeaderLocation = heartBeat.LeaderLocation
		}
		if !namenode.IsLeader {
			fmt.Println("reset ticker")
			namenode.HeartBeatTicker.Reset(3 * HeartBeatInterval)
			namenode.LeaderLocation = heartBeat.LeaderLocation
			namenode.Term = heartBeat.Term
		}
		if len(heartBeat.EditLog) == 0 {
			// 不带editlog的心跳同步
			fmt.Println("without edit log")
			fmt.Printf("leader commit=%+v follower commit=%+v\n", heartBeat.LeaderCommitIndex, namenode.CommitIndex)
			if heartBeat.LeaderCommitIndex == namenode.CommitIndex {
				c.JSON(http.StatusOK, namenode.CommitIndex)
				return
			}
			if heartBeat.LeaderCommitIndex > namenode.CommitIndex {
				for _, log := range namenode.TmpLog[namenode.CommitIndex:] {
					// TODO: 应用变动到namenode文件树
					fmt.Println("change namenode file tree")
					namenode.CommitIndex = log.CommitIndex
				}
				c.JSON(http.StatusOK, namenode.CommitIndex)
				return
			}
			c.JSON(http.StatusBadRequest, namenode.CommitIndex)
			return
		} else {
			fmt.Println("has edit log")
			fmt.Printf("pre log index=%+v tmpLog=%+v\n", heartBeat.PreLogIndex, namenode.TmpLog)
			if heartBeat.PreLogIndex > len(namenode.TmpLog) {
				fmt.Println("bigger pre log index")
				c.JSON(http.StatusNotAcceptable, namenode.CommitIndex)
				return
			}
			if heartBeat.PreLogIndex > 0 && heartBeat.PreLogTerm != namenode.TmpLog[heartBeat.PreLogIndex-1].Term {
				c.JSON(http.StatusBadRequest, namenode.CommitIndex)
				return
			}
			for _, log := range heartBeat.EditLog {
				if log.CommitIndex > len(namenode.TmpLog) {
					namenode.TmpLog = append(namenode.TmpLog, log)
				} else {
					if namenode.TmpLog[log.CommitIndex-1].Term != log.Term ||
						namenode.TmpLog[log.CommitIndex-1].CommitIndex != log.CommitIndex {
						namenode.TmpLog[log.CommitIndex-1] = log
					}
				}
				if log.CommitIndex <= heartBeat.LeaderCommitIndex &&
					log.CommitIndex > namenode.CommitIndex {
					// TODO: 应用到文件树
					fmt.Println("change namenode file tree")
					namenode.CommitIndex = log.CommitIndex
				}
			}
			logStr, _ := json.Marshal(namenode.TmpLog)
			fmt.Printf("tmpLog=%+v commitIndex=%+v\n", string(logStr), namenode.CommitIndex)
			c.JSON(http.StatusOK, namenode.CommitIndex)
			return
		}
	})

	router.POST("/vote", func(c *gin.Context) {
		b, _ := c.GetRawData() // 从c.Request.Body读取请求数据
		vote := &Vote{}
		// 反序列化
		if len(b) == 0 {
			fmt.Println("put request body为空")
		}
		if err := json.Unmarshal(b, vote); err != nil {
			fmt.Println("namenode put json to byte error", err)
		}
		fmt.Printf("receive vote=%+v\n time=%+v", vote, time.Now())
		if vote.Term <= namenode.Term {
			c.JSON(http.StatusBadRequest, nil)
			return
		}
		if vote.LeaderCommitIndex < namenode.CommitIndex {
			c.JSON(http.StatusBadRequest, nil)
			return
		}
		namenode.Term = vote.Term
		c.JSON(http.StatusOK, nil)
	})

	router.POST("/put", func(c *gin.Context) {
		b, _ := c.GetRawData() // 从c.Request.Body读取请求数据
		file := &File{}
		// 反序列化
		if len(b) == 0 {
			fmt.Println("put request body为空")
		}
		if err := json.Unmarshal(b, file); err != nil {
			fmt.Println("namenode put json to byte error", err)
		}
		success := namenode.AddEditLog("put", file.RemotePath+file.Name, false)
		if !success {
			c.JSON(http.StatusBadRequest, nil)
			return
		}
		fmt.Printf("add log success=%v\n", success)
		// 去除最开始的斜杠
		path := strings.Split(file.RemotePath, "/")[1:]

		var n *Folder
		ff := namenode.NameSpace
		//例如：path = /root/temp/dd/
		//遍历所有文件夹，/root/下的所有文件夹
		folder := &ff.Folder
		// folder := &namenode.NameSpace.Folder
		for _, p := range path[1:len(path)-1] {
			if p == ""{
				continue
			}
			//fmt.Println(p)
			exist := false
			for _, n = range *folder {
				if p == n.Name {
					exist = true
					break
				}
			}
			//如果不存在，就新建一个文件夹
			if !exist {
				TDFSLogger.Println("namenode: file not exist")
				var tempFloder Folder = Folder{}
				tempFloder.Name = p
				*folder = append(*folder, &tempFloder)
				//下一层
				folder = &(*folder)[len(*folder)-1].Folder
				n = &tempFloder
			} else {
				folder = &n.Folder
			}

		}

		//直接把文件写在当前文件夹下
		var exist bool
		var changed bool = true
		var f *File
		for _, f = range n.Files {
			exist = false
			//找到目标文件
			if f.Name == file.Name {
				exist = true
				//校验文件是否改变
				if f.Info == file.Info {
					//如果没改变，client就不用向datanode改变信息
					TDFSLogger.Println("namenode: file exists and not changed")
					changed = false
				}
				break
			}
		}

		var chunkNum int
		var fileLength = int(file.Length)
		if file.Length%int64(SPLIT_UNIT) == 0 {
			chunkNum = fileLength / SPLIT_UNIT
			file.OffsetLastChunk = 0
		} else {
			chunkNum = fileLength/SPLIT_UNIT + 1
			file.OffsetLastChunk = chunkNum*SPLIT_UNIT - fileLength
		}
		for i := 0; i < int(chunkNum); i++ {
			replicaLocationList := namenode.AllocateChunk()
			fileChunk := &FileChunk{}
			file.Chunks = append(file.Chunks, *fileChunk)
			file.Chunks[i].ReplicaLocationList = replicaLocationList
		}

		if !exist {
			n.Files = append(n.Files, file)
		} else if changed {
			TDFSLogger.Println("namenode: file exists and changed")
			f = file
		}
		if !changed {
			file = &File{}
		}
		c.JSON(http.StatusOK, file)
	})
	//
	router.GET("/getfile", func(c *gin.Context) {
		filename := c.Query("filename")
		fmt.Println("$ getfile ...", filename)
		TDFSLogger.Println("filename")
		node := namenode.NameSpace
		file, err := node.GetFileNode(filename)
		if err != nil {
			TDFSLogger.Printf("get file=%v error=%v\n", filename, err.Error())
			fmt.Printf("get file=%v error=%v\n", filename, err.Error())
			c.JSON(http.StatusNotFound, err.Error())
			return
		}
		c.JSON(http.StatusOK, file)
	})

	router.GET("/delfile/:filename", func(c *gin.Context) {
		filename := c.Param("filename")
		fmt.Println("$ delfile ...", filename)
		var targetFile *File = nil
		files := namenode.NameSpace.Files
		for i := 0; i < len(files); i++ {
			if files[i].Name == filename {
				targetFile = files[i]
				for j := 0; j < len(targetFile.Chunks); j++ {
					namenode.DelChunk(*targetFile, j)
				}
			}
		}

		c.JSON(http.StatusOK, targetFile)
	})

	router.POST("/getfolder", func(context *gin.Context) {
		b, _ := context.GetRawData() // 从c.Request.Body读取请求数据
		var dataMap map[string]string
		if err := json.Unmarshal(b, &dataMap); err != nil {
			fmt.Println("namenode put json to byte error", err)
		}
		fmt.Println("there:")
		fmt.Println(dataMap["fname"])
		files, folders := namenode.NameSpace.GetFileList(dataMap["fname"])
		var filenames []string
		for i := 0; i < len(files); i++ {
			filenames = append(filenames, files[i].Name)
		}
		fmt.Println("folder:")
		fmt.Println(folders[0].Name)
		context.JSON(http.StatusOK, filenames)
		//context.JSON(http.StatusOK, 1)
	})

	//router.GET("/getfolder/:foldername", func(c *gin.Context) {
	//	foldername := c.Param("foldername")
	//	fmt.Println("$ getfolder ...", foldername)
	//	TDFSLogger.Fatal("$ getfolder ...", foldername)
	//	files := namenode.NameSpace.GetFileList(foldername)
	//	var filenames []string
	//	for i := 0; i < len(files); i++ {
	//		filenames = append(filenames, files[i].Name)
	//	}
	//	c.JSON(http.StatusOK, filenames)
	//})

	//创建文件目录
	router.POST("/mkdir", func(context *gin.Context) {
		b, _ := context.GetRawData() // 从c.Request.Body读取请求数据
		var dataMap map[string]string
		if err := json.Unmarshal(b, &dataMap); err != nil {
			fmt.Println("namenode put json to byte error", err)
		}
		if namenode.NameSpace.CreateFolder(dataMap["curPath"], dataMap["folderName"]) {
			context.JSON(http.StatusOK, 1)
		}
		context.JSON(http.StatusOK, -1)
	})

	router.Run(":" + strconv.Itoa(namenode.Port))
}

func (namenode *NameNode) DelChunk(file File, num int) {
	//预删除文件的块信息
	//修改namenode.DataNodes[].ChunkAvail
	//和namenode.DataNodes[].StorageAvail
	for i := 0; i < REDUNDANCE; i++ {
		chunklocation := file.Chunks[num].ReplicaLocationList[i].ServerLocation
		chunknum := file.Chunks[num].ReplicaLocationList[i].ReplicaNum

		index := namenode.Map[chunklocation]
		namenode.DataNodes[index].ChunkAvail = append(namenode.DataNodes[index].ChunkAvail, chunknum)
		namenode.DataNodes[index].StorageAvail++
	}
}

func (namenode *NameNode) AllocateChunk() (rlList [REDUNDANCE]ReplicaLocation) {
	redundance := namenode.REDUNDANCE
	var max [REDUNDANCE]int
	for i := 0; i < redundance; i++ {
		max[i] = 0
		//找到目前空闲块最多的NA
		for j := 0; j < namenode.DNNumber; j++ {
			//遍历每一个DN，找到空闲块最多的前redundance个DN
			if namenode.DataNodes[j].StorageAvail > namenode.DataNodes[max[i]].StorageAvail {
				max[i] = j
			}
		}

		//ServerLocation是DN地址
		rlList[i].ServerLocation = namenode.DataNodes[max[i]].Location
		//ReplicaNum是DN已用的块
		rlList[i].ReplicaNum = namenode.DataNodes[max[i]].ChunkAvail[0]
		n := namenode.DataNodes[max[i]].StorageAvail

		namenode.DataNodes[max[i]].ChunkAvail[0] = namenode.DataNodes[max[i]].ChunkAvail[n-1]
		namenode.DataNodes[max[i]].ChunkAvail = namenode.DataNodes[max[i]].ChunkAvail[0 : n-1]
		namenode.DataNodes[max[i]].StorageAvail--
	}

	return rlList
}

func (namenode *NameNode) SetConfig(location string, dnnumber int, redundance int, dnlocations []string, nnlocations []string) {
	temp := strings.Split(location, ":")
	res, err := strconv.Atoi(temp[2])
	if err != nil {
		fmt.Println("XXX NameNode error at Atoi parse Port", err.Error())
		TDFSLogger.Fatal("XXX NameNode error: ", err)
	}
	namenode.NameSpace = &Folder{
		Name:   "root",
		Folder: make([]*Folder, 0),
		Files:  make([]*File, 0),
	}
	namenode.Port = res
	namenode.Location = location
	namenode.NNLocations = nnlocations
	namenode.DNNumber = dnnumber
	namenode.DNLocations = dnlocations
	namenode.REDUNDANCE = redundance
	namenode.TmpLog = make([]*EditLog, 0)
	namenode.MatchIndex = make(map[string]int)
	namenode.HeartBeatTicker = time.NewTicker(HeartBeatInterval)
	fmt.Println("************************************************************")
	fmt.Println("************************************************************")
	fmt.Printf("*** Successfully Set Config data for the namenode\n")
	namenode.ShowInfo()
	fmt.Println("************************************************************")
	fmt.Println("************************************************************")
}

func (namenode *NameNode) ShowInfo() {
	fmt.Println("************************************************************")
	fmt.Println("****************** showinf for NameNode ********************")
	fmt.Printf("Location: %s\n", namenode.Location)
	fmt.Printf("DATANODE_DIR: %s\n", namenode.NAMENODE_DIR)
	fmt.Printf("Port: %d\n", namenode.Port)
	fmt.Printf("DNNumber: %d\n", namenode.DNNumber)
	fmt.Printf("REDUNDANCE: %d\n", namenode.REDUNDANCE)
	fmt.Printf("DNLocations: %s\n", namenode.DNLocations)
	fmt.Printf("DataNodes: ")
	fmt.Println(namenode.DataNodes)
	fmt.Println("******************** end of showinfo ***********************")
	fmt.Println("************************************************************")
}

func (namenode *NameNode) GetDNMeta() { // UpdateMeta
	namenode.Map = make(map[string]int)
	for i := 0; i < len(namenode.DNLocations); i++ {
		namenode.Map[namenode.DNLocations[i]] = i
		response, err := http.Get(namenode.DNLocations[i] + "/getmeta")
		if err != nil {
			fmt.Println("XXX NameNode error at Get meta of ", namenode.DNLocations[i], ": ", err.Error())
			TDFSLogger.Fatal("XXX NameNode error: ", err)
		}
		defer response.Body.Close()

		var dn DataNode
		err = json.NewDecoder(response.Body).Decode(&dn)
		if err != nil {
			fmt.Println("XXX NameNode error at decode response to json.", err.Error())
			TDFSLogger.Fatal("XXX NameNode error: ", err)
		}
		// fmt.Println(dn)
		// err = json.Unmarshal([]byte(str), &dn)
		namenode.DataNodes = append(namenode.DataNodes, dn)
	}
	namenode.ShowInfo()
}
