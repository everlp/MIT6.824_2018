# 1. 简介
本实验我们要构建一个容错键/值存储系统，其分为三个 Part：
- 实现 Raft，一种 replicated 状态机协议
- 在 Raft 上构建一个 key/value 服务
- 在多个 replicated 状态机上“共享”服务，以获得更高的性能

![Replicated_State_Machine_Architecture ](_v_images/_replicated_1563885636_26056.png)

> Figure 1: Replicated state machine architecture. The consensus algorithm manages a replicated log containing state machine commands from clients. The state machines process identical sequences of commands from the logs, so they produce the same outputs.

**以下资料对完成本实验有很大帮助**：

- Raft Raft protocol 图解 [Raft protocol](http://thesecretlivesofdata.com/raft/)
    - 结点具有三种状态，所有结点的初始状态都是 Follower。
        - Follower
        - Candidate
        - Leader

- 2016 年一位助教为MIT6.824 学生写的 Guide [a guide to Raft implementation](https://thesquareplanet.com/blog/students-guide-to-raft/)
- 最重要的还是 Raft 的论文 [In Search of an Understandable Consensus Algorithm](https://pdos.csail.mit.edu/6.824/papers/raft-extended.pdf)，与上一个资料结合起来看。
# 2. Part 2A
## 2.1. 基本结构
一开始可能不知道从何下手，首先根据课程文档给出的提示写出一部分框架。

> Add any state you need to the Raft struct in `raft.go`. You'll also need to define a struct to hold information about each log entry. Your code should follow Figure 2 in the paper as closely as possible.

```
type Log struct {
	Command interface{}
	Term    int32
}
//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]

	// counts    int

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.
	// Persistent state on all servers
	logs         []Log
	votedFor     int
	currentTerm  int32
	// State
	state        int32
	// Volatile state on all server
	commitIndex  int
	lastApplied  int

	VoteGrantedCount  int
	// Volatile state on leaders
	nextIndex[]  int  // 是一个集合，针对每个peer都有一个值，是leader要发送给其他peer的下一个日志索引。
	matchIndex[] int  // 是一个集合，针对每个peer都有一个值，是leader收到的其他peer已经确认一致的日志序号。

	// timer
	electionTimer *time.Timer
	voteCh     chan struct{} // 成功投票的信号
}

```

> Fill in the RequestVoteArgs and RequestVoteReply structs. 
```
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}
// field names must start with capital letters!
type RequestVoteReply struct {
	// Your data here (2A).
	Term         int
	VoteGranted  bool
}
```

> Modify `Make()` to create a background goroutine that will kick off leader election periodically by sending out `RequestVote` RPCs when it hasn't heard from another peer for a while. 

Make 的编写很重要，实际是Raft的初始化过程。最后启动状态机的循环。
```
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	// 指针    &
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me
	// Your initialization code here (2A, 2B, 2C).
	// 计时发送RequestVote, 怎么计时呢？ timer
	rf.currentTerm = 0;
	rf.votedFor = -1;
	rf.VoteGrantedCount = 0;
	rf.logs = make([]Log, 0)

	rf.commitIndex = -1
	rf.lastApplied = -1
	rf.state = FOLLOWER
	rf.nextIndex = make([]int, len(rf.peers))
	rf.matchIndex = make([]int, len(rf.peers))
	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())
	go rf.raftLoop()
	return rf
}
```


> This way a peer will learn who is the leader, if there is already a leader, or become the leader itself. Implement the `RequestVote()` RPC handler so that servers will vote for one another.

实现 `func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) `，在论文RequestVote RPC 部分说明，Recever implememtation，转化为代码如下：
```
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if args.Term < rf.currentTerm {
		reply.VoteGranted = false

	} else if args.Term == rf.currentTerm {
		if (rf.votedFor == -1 ||  rf.votedFor == args.CandidateId)  && args.LastLogIndex >= len(rf.logs){
			// 只有当 此结点未向候选人投票 或是 只向当前候选人投票时， 此票才是成功的。 确保了一个结点投票两次的情况
			reply.VoteGranted = true
			rf.votedFor = args.CandidateId;
			fmt.Printf("Server %d voted for Server %d\n", rf.me, args.CandidateId)
			// rf.resetTimer()
		} else {
			reply.VoteGranted = false
		}
	} else {
		// currentTerm is smaller than the candidate
		rf.switchTo(FOLLOWER)
		rf.currentTerm = args.Term
		rf.votedFor = args.CandidateId
		reply.VoteGranted = true
	}
	reply.Term = rf.currentTerm
	if reply.VoteGranted == true {
		go func() { rf.voteCh <- struct{}{} }()
	}

}
```

> To implement heartbeats, define an AppendEntries RPC struct (though you may not need all the arguments yet), and have the leader send them out periodically.
```
type AppendEntryArgs struct {
	term         int  // leader's term
	leaderId     int  // so follower can redirect clients
	prevLogIndex int  // index of log entry immediately preceding new ones
	prevLogTerm  int  // term of prevLogIndex entry
	entries      []Log  // (empty for heartbeat; may send more than one for efficiency)
	leaderCommit int    // leader's commitIndex
}
type AppendEntryReply struct {
	term    int     // currentTerm, for leader to update itself
	success bool    // true if follwer contained entry matching prevLogTerm and prevLogIndex
}

func (rf *Raft)sendAppendEntries(server int , args *AppendEntryArgs, reply *AppendEntryReply) {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}
```

>Make sure the election timeouts in different peers don't always fire at the same time, or else all peers will vote only for themselves and no one will become the leader.

在 `RequestVote ` 函数中，我们添加了一个判断: 只有当 此结点未向任何候选人投票 或是 只向当前候选人投票时， 此票才是成功的。 确保了一个结点投票两次的情况
```
if (rf.votedFor == -1 ||  rf.voteFor == args.CandidateId)  && args.LastLogIndex >= len(rf.logs){
				reply.VoteGranted = true
				rf.votedFor = args.CandidateId;
		}
```

## 2.2. 函数实现
**因为代码量过大，以下我们只提供实现的基本思路，[具体代码我将贴在 GitHub 上](https://github.com/SmallPond/MIT6.824_2018)。**

1. 首先实现状态获取、GetTerm以及状态判断的原子操作。在`conf`中进行 check 时也会用到以下函数（若运行 test 出错需要检查函数是否实现）。
```
// atomic operations
func (rf *Raft) getTerm() int32 {
	return atomic.LoadInt32(&rf.currentTerm)
}

func (rf *Raft) isState(state int32) bool {
	return atomic.LoadInt32(&rf.state) == state
}
// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	var term int
	var isleader bool
	// Your code here (2A).
	term = int(rf.getTerm())
	isleader = rf.isState(LEADER)
	return term, isleader
}
```

2. 实现请求投票，handle 请求以及 handle 请求投票的reply。
- `func (rf *Raft) broadcastVoteReq()`
- `func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) `
- `func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool`

3. 实现请求添加Log 项，handle append 请求以及 handle 请求append的reply。PartA的实现仅仅停留在利用 AppendEntries 维持 Leader的存在，并不实际进行 Log的更改。
- `func (rf *Raft) sendAppendEntries(server int , args *AppendEntryArgs, reply *AppendEntryReply) bool`
- `func (rf *Raft) broadcastAppendEntries()`
- `func (rf *Raft) AppendEntries(args *AppendEntryArgs, reply *AppendEntryReply)`

4. 要实现随机定时，需要使用Golang 的 timer+ rand。以下两个函数实现了随机时间的产生以及Timer 的重置。
```
func randElectionDuration() time.Duration {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return time.Millisecond * time.Duration(r.Int63n(ELEC_TIME_MAX-ELEC_TIME_MIN) + ELEC_TIME_MIN)
}

func (rf *Raft) resetTimer() {
	newTimeout := randElectionDuration()
	rf.electionTimer.Reset(newTimeout)
}
```

5. 最后最重要的函数是 Raft 各状态之间的切换逻辑，也就是 Raft 的主循环。
```
func (rf *Raft) raftLoop() {
	rf.electionTimer = time.NewTimer(randElectionDuration())
	for {
		switch atomic.LoadInt32(&rf.state) {
			case FOLLOWER:
				select {
				case  <-rf.voteCh:
					rf.resetTimer()

				case <- rf.electionTimer.C:
					rf.mu.Lock()
					rf.switchTo(CANDIDATE)
					rf.startElection()
					rf.mu.Unlock()
				}
			case CANDIDATE:
				rf.mu.Lock()
				select {

				case <-rf.electionTimer.C:
					rf.resetTimer()
					// election time out ， what we should do? do it again
					rf.startElection()
				default:
					// check if it has collected enough vote
					if rf.VoteGrantedCount > len(rf.peers)/2 {
						rf.switchTo(LEADER)
					}
				}
				rf.mu.Unlock()
			case LEADER:
				rf.broadcastAppendEntries()
				time.Sleep(HEART_BEAT)
				// fmt.Printf("Term: %d, Server: %d I'm a leader! \n", rf.currentTerm, rf.me)

		}
	}
}
```

## 2.3. 最终结果
```
Test (2A): initial election ...
Term: 0: Server 2 transfer from 0 to 1
Term: 1: Server 2 transfer from 1 to 2
Term: 0: Server 0 transfer from 0 to 1
  ... Passed --   3.0  3  118    0
Test (2A): election after network failure ...
Term: 1: Server 1 transfer from 0 to 1
Term: 0: Server 1 transfer from 0 to 1
Term: 1: Server 1 transfer from 1 to 2
Term: 1: Server 0 transfer from 0 to 1
Term: 2: Server 0 transfer from 1 to 2
Term: 2: Server 2 transfer from 0 to 1
  ... Passed --   4.6  3   53    0
PASS
ok  	raft	7.655s
```





