# 1. MIT6.824_Lab2_Raft_PartB
在这个部分我们想要实现 Raft 保持一致的、复制的操作日志。在 Leader 中对`Start()`的调用开启向日志中添加一个新操作的过程；同时将这个新操作使用`AppendEntries`RPC发送给其他服务器。

> 实现 leader 和 follower 代码完成日志项的添加。这涉及到实现`start()`，补充 AppendEntries RPC 结构体，发送请求，充实AppendEntry RPC处理程序以及更新leader 的commitIndex。

[详细完整的代码我将贴在 GitHub 上](https://github.com/SmallPond/MIT6.824_2018)。

## 1.1. FAILS
`TestBasicAgree2B` 创建了5个 server, `nCommitted`求得有多少服务器认为日志项已经提交。

1. 不明白为什么一直无法通过 2B 的第一个test，而且拿其他大佬的`raft.go`代码直接过来跑也是无法通过。但是其跑其对应的测试代码是可以通过的。**仔细对比发现可能是`config.go`中的`start1`函数不一样导致的（还有在Start中忘记使用`AppendEntries`RPC将新操作发送给其他服务器）。** 在调试的过程中我的代码不小心被覆盖找不回来了（大哭..Debug D到疯，实在是不想再重写一遍（~~也感觉找不出到达时哪里出现了问题~~**无法通过BasicAgree其中一个原因是我在写入applyMsg时没有写入`CommanValid=true`，也就不能在start1中将日志写入log中，导致一直读出来nCommited为0**），
```
// config.go 中的 start1()部分代码
if m.CommandValid == false {
				// ignore other types of ApplyMsg
			} else if v, ok := (m.Command).(int); ok {
    			  cfg.mu.Lock()
    				for j := 0; j < len(cfg.logs); j++ {
    					if old, oldok := cfg.logs[j][m.CommandIndex]; oldok && old != v {
    						// some server has already committed a different value for this entry!
    						err_msg = fmt.Sprintf("commit index=%v server=%v %v != server=%v %v",
    							m.CommandIndex, i, m.Command, j, old)
    					}
    				}
```
同时不想再纠结于Raft的实现细节（做这个实现的目的只是明白 分布式系统的数据一致性实现的原理），所以直接在GitHub 上找了另外一位大神的代码[Wangzhike/MIT6.824_DistributedSystem](https://github.com/Wangzhike/MIT6.824_DistributedSystem)，这是一份2018年的代码，可以直接通过2B所有的Test。剩下的就是看懂其实现思路，删除多余的2C代码，仅仅保留可通过2B测试的代码。
```
--- FAIL: TestBasicAgree2B (2.16s)
	config.go:465: one(100) failed to reach agreement
```

## 1.2. 实现
1. 实现`Start()`函数，`Start()`的调用开启向日志中添加一个新操作的过程；同时将这个新操作使用`AppendEntries`RPC发送给其他服务器。
```
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).
	if term, isLeader = rf.GetState(); isLeader {

		// 1. leader将客户端command作为新的entry追加到自己的本地log
		rf.mu.Lock()
		logEntry := LogEntry{Command:command, Term:rf.CurrentTerm}
		rf.Log = append(rf.Log, logEntry)
		index = len(rf.Log) - 1
		DPrintf("[Start]: Id %d Term %d State %s\t||\treplicate the command to Log index %d\n",
			rf.me, rf.CurrentTerm, state2name(rf.state), index)
		nReplica := 1
		// 发送AppendEntries RPC时也更新下最近发送时间
		rf.latestIssueTime = time.Now().UnixNano()

		// 接收到客户端命令，并写入log，保存下持久状态
		rf.persist()
		rf.mu.Unlock()

		// 2. 给其他peers并行发送AppendEntries RPC以复制该entry
		rf.mu.Lock()
		go rf.broadcastAppendEntries(index, rf.CurrentTerm, rf.commitIndex, nReplica, "Start")
		rf.mu.Unlock()
	}
	return index, term, isLeader
}
```

2. 实现 `broadcastAppendEntries`以及`AppendEntries`函数，其中涉及到一些比较细节的操作，需要仔细阅读论文。这里就不再赘述，我探究的重点也不在这个方面。

3. 仔细阅读Down下来的这份代码后，发现这个大神的代码有好多处亮点。在定时操作中，并没有使用我们之前使用的`timer.C`方式，而是开启几个线程去tick，tick完后向 chan 中写入信号，在 `eventLoop` 中开始相应的超时工作。还使用了`sync.WaitGroup`来同步等待所有请求 vote or append 操作。
- `heartbeatPeriodTick`,其同样通过一个线程来实现，其中有一个同步变量`rf.leaderCond`，在不是leader时，此线程会一直wait，直到获得同步变量（state 由 candidate 转为 leader时）。



# 2. MIT6.824_Lab2_Raft_PartC
如果基于Raft的服务器重新启动，它应该从中断处恢复服务。 这要求Raft重启后仍然保持持久状态。 本文的图2提到哪个状态应该是持久的，而raft.go包含了如何保存和恢复持久状态的示例。

Persistent state on all servers:
(Updated on stable storage before responding to RPCs)
name|explanation
:-:|:-:
currentTerm| latest term server has seen (initialized to 0 on first boot, increases monotonically)
votedFor| candidateId that received vote in current term (or null if none)
log[] log entries |each entry contains command for state machine, and term when entry was received by leader (first index is 1)

“实际”实现可以通过每次发生更改时将Raft的持久状态写入磁盘，并在重新启动后从磁盘读取最新保存状态来实现此目的。 本部分实现不使用磁盘; 相反，它将从Persister对象保存和恢复持久状态（请参阅`persister.go`）。 无论是谁调用Raft.Make（）都会提供一个最初持有Raft最近持久状态（如果有的话）的Persister。 Raft应该从Persister初始化它的状态，并且应该在每次状态改变时使用它来保存其持久状态。 使用Persister的`ReadRaftState()`和`SaveRaftState()`方法。

## 2.1. Task
> 通过添加 save and restore 持久状态的代码完成`raft.go`中`persist()`和`readPersist()`函数。为了将state 传递到Persister中，你将需要对 state 编码（序列化）为 bytes 数组。具体操作可以阅读函数中的注释。labgod 起源于 GO 的gob 编码器，唯一的区别在于如果你尝试对小写域名的结构体进行编码时， labgob将会打印出错误信息。

1. 根据注释，可以很快写出状态保存以及状态读取代码。
```
func (rf *Raft) persist() {

	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.CurrentTerm)
	e.Encode(rf.VoteFor)
	e.Encode(rf.Log)
	data := w.Bytes()
	rf.persister.SaveRaftState(data)

}

func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var currentTerm int
	var logs []LogEntry
	var voteFor int
	if d.Decode(&currentTerm) != nil ||
	   d.Decode(&voteFor) != nil ||
	   d.Decode(&logs) != nil {
		log.Fatal("[readPersist]: decode error\n")
	} else {
		rf.CurrentTerm = currentTerm
		rf.VoteFor = voteFor
		rf.Log = logs
	}
}
```

>现在你需要确定Raft协议中的哪些点需要你的服务器保持其状态，并在这些位置插入对`persist()`的调用。 在`Raft.Make()`中已经调用了`readPersist()`。 完成此操作后，您应该通过剩余的测试。 

readPersist只需在 Make 时进行调用即可，而对persist的调用需要仔细考虑。实际上，状态保持的核心在于：一旦以上提到的三个`Persistent state`变量发生改变时，我们就需要对其进行一次 `persist`。例如以下情形：
- VoteFor and  CurrentTerm
    - 开始选举时，VoteFor指向自身，并且currentTerm 自增    
    - Server接收到请求投票时，server的 currentTerm小于请求参数，需要切换为Follower，VoteFor置为-1, currentTerm = args.Term
    - Server接收到请求投票时，授予Candidate 的投票请求, VoteFor = CandidateID
    - handle请求投票回复，Server 未授予投票并且 currentTerm 较小，VoteFor 重置为-1， currentTerm = reply.Term
    - handle AppendEntried的回复，Server 回复append 不成功并且 currentTerm 较小，转为Follower，并且currentTerm = reply.Term
- Log 改变
    - Laeder接收 client的请求对`Start`的调用，修改了Log项
    - AppendEntries修改日志

除了以上情况，我们可能会认为在选举成功后（即由Candidate 转化为 Leader）也需要保持状态，但仔细考虑一番可以发现，这个过程并没有修改三个保持变量中的任何一个。故可略去。完整的实现代码，同样参考Github。
