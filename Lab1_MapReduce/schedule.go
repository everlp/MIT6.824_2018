package mapreduce

import (
	"fmt"
	"sync"
)


//
// schedule() starts and waits for all tasks in the given phase (mapPhase
// or reducePhase). the mapFiles argument holds the names of the files that
// are the inputs to the map phase, one per map task. nReduce is the
// number of reduce tasks. the registerChan argument yields a stream
// of registered workers; each item is the worker's RPC address,
// suitable for passing to call(). registerChan will yield all
// existing registered workers (if any) and new ones as they register.
//
func schedule(jobName string, mapFiles []string, nReduce int, phase jobPhase, registerChan chan string) {
	var ntasks int
	var n_other int // number of inputs (for reduce) or outputs (for map)
	switch phase {
	case mapPhase:
		// 任务数
		ntasks = len(mapFiles)
		// 输出数
		n_other = nReduce
	case reducePhase:
		ntasks = nReduce
		n_other = len(mapFiles)
	}

	fmt.Printf("Schedule: %v %v tasks (%d I/Os)\n", ntasks, phase, n_other)

	// All ntasks tasks have to be scheduled on workers. Once all tasks
	// have completed successfully, schedule() should return.
	// 等待所有 goroutines finish 需要用 sync.WaitGroup 实现
	var waitGp sync.WaitGroup
	waitGp.Add(ntasks)

	for i:=0; i<ntasks; i++ {
		// client dials the server, but we don't konw the server's address and port!
		// common_rpc 已经写好了服务器连接函数 call ！
		args := DoTaskArgs{jobName, mapFiles[i], phase, i, n_other}

		go func(args DoTaskArgs) {
			for {
				wrk := <- registerChan
				// send a Worker.Dotask to the Worker,
				ok := call(wrk, "Worker.DoTask", &args, nil)
				if ok  {
					// 这样的 Done 貌似不能保证所有任务都完成，只能说任务开始。
					waitGp.Done()
					// 再写回利用， wrk 意义上应该只是一个接收请求的网络监听，创建任务后就可以继续监听
					registerChan <- wrk
					break
				}
			}
		}(args)
	}
	waitGp.Wait()
	fmt.Printf("Schedule: %v done\n", phase)
}
