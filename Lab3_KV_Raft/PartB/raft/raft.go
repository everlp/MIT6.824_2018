package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new Log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the Log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"bytes"
	"labrpc"
	"labgob"
	"log"
	"math/rand"
	"sync"
	"time"
	"fmt"
)
const RPC_CALL_TIMEOUT = time.Duration(500) * time.Millisecond//rpc超时时间

type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
	CommandTerm  int
	Snapshot []byte


}

// Log entry struct, because AppendEntriesArgs includs []LogEntry,
// so field names must start with capital letters!
type LogEntry struct {
	Command		interface{}		// each entry contains command for state machine,
	Term 		int				// and term when entry was received by leader(fisrt index is 1)
	Index 	    int             // snapshot 添加该项，表示该日志项在log中的索引， 以 1 起始
}

// the state of servers
const (
	Follower	int = 0
	Candidate		= 1
	Leader			= 2
)

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	applyCh		chan ApplyMsg

	state 		int				// state of server(Follower, Candidate and Leader)
	leaderId	int				// so follower can redirect clients

	applyCond	*sync.Cond		// signal for new committed entry when updating the CommitIndex

	leaderCond	*sync.Cond		// signal for heartbeatPeriodTick routine when the peer becomes the leader
	nonLeaderCond 	*sync.Cond	// signal for electionTimeoutTick routine when the peer abdicates the the leader

	electionTimeout	int			// election timout(heartbeat timeout)
	heartbeatPeriod	int			// the period to issue heartbeat RPCs

	latestIssueTime	int64		// 最新的leader发送心跳的时间
	latestHeardTime	int64		// 最新的收到leader的AppendEntries RPC(包括heartbeat)
	// 或给予candidate的RequestVote RPC投票的时间

	electionTimeoutChan	chan bool	// 写入electionTimeoutChan意味着可以发起一次选举
	heartbeatPeriodChan	chan bool	// 写入heartbeatPeriodChan意味leader需要向其他peers发送一次心跳

	// Persistent state on all server
	CurrentTerm int 			// latest term server has seen(initialized to 0 on fisrt boot,
	// increases monotonically)
	VoteFor int        			// candidateId that received vote in current term(or null if none)
	Log     []LogEntry 			// Log entries

	// Volatile state on all server
	CommitIndex int // index of highest Log entry known to be committed(initialized to 0,
	// increase monotonically)
	LastApplied int // index of highest Log entry applied to state machine(initialized to 0,
	// increase monotonically)

	// Volatile state on candidate
	nVotes		int				// total num votes that the peer has got

	// Volatile state on leaders
	nextIndex	[]int			// for each server, index of the next Log entry to send to that server
	// (initialized to leader last Log index + 1)
	matchIndex	[]int			// for each server, index of highest Log entry known to be replicated on
	// server(initialized to 0, increases monotonically)
	LastIncludedIndex 	int  //现存快照对应的最后一个日志下标
	LastIncludedTerm 	int  //现存快照对应的最后一个日志所属term
}

// return CurrentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	var term int
	var isleader bool
	// Your code here (2A).
	rf.mu.Lock()
	term = rf.CurrentTerm
	if rf.state == Leader {
		isleader = true
	} else {
		isleader = false
	}
	rf.mu.Unlock()

	return term, isleader
}


func (rf *Raft) TakeSnapshot(snapAppliedIndex int, term int, snapshotData []byte) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if snapAppliedIndex <= rf.LastIncludedIndex {
		// 忽略此次操作
		return
	}
	DPrintf("Take Snapshot in index[%v]", snapAppliedIndex)

	// 保存快照后修改 Log
	newLog := make([]LogEntry, 0)
	// 这样切片后， log[0]会存储快照中的最后一项，是无效的但可占位
	newLog = append(newLog, rf.Log[rf.subSnapshotIndex(snapAppliedIndex):]...)
	rf.Log = newLog
	rf.LastIncludedIndex = snapAppliedIndex
	rf.LastIncludedTerm = term


	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(snapAppliedIndex)
	e.Encode(term)
	// e.Encode(kvdata)
	// 将snapshot 加入到byte中
	// 类似这种操作 snapData += snapshotData
	w.Write(snapshotData)
	snapData := w.Bytes()


	w1 := new(bytes.Buffer)
	e1 := labgob.NewEncoder(w1)
	e1.Encode(rf.CurrentTerm)
	e1.Encode(rf.VoteFor)
	e1.Encode(rf.Log)
	stateData := w1.Bytes()
	// log 改变了，所以我们同时需要保存State
	rf.persister.SaveStateAndSnapshot(stateData, snapData)
}



//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {

	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(rf.CurrentTerm)
	e.Encode(rf.VoteFor)
	e.Encode(rf.Log)
	data := w.Bytes()
	rf.persister.SaveRaftState(data)

}


//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	//fmt.Printf("readPersist : %v",data)
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
type InstallSnapshotArgs struct {
	Term      int             // leader's term
	LeaderID  int             // so follower can redirect clients
	LastIncludedIndex	int
	LastIncludedTerm	int
	Data             	[]byte  //snapshot

}
type InstallSnapshotReply struct {
	// currentTerm, for leader to update itself
	Term int
}
func max(a int, b int)int {
	if a >= b {
		return a
	}
	return b
}
func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	DPrintf("[+] server [%d], start InstallSnapshot\n", rf.me)
	reply.Term = rf.CurrentTerm
	if args.Term < rf.CurrentTerm || args.LastIncludedIndex <= rf.LastIncludedIndex {
		return
	}

	rf.CurrentTerm = args.Term
	rf.switchTo(Follower)
	rf.VoteFor = args.LeaderID

	newLog := make([]LogEntry, 0)
	// 当前日志比快照的index长，截取后半部分
	if args.LastIncludedIndex <= rf.getLastLogIndex() {
		newLog = append(newLog, rf.Log[rf.subSnapshotIndex(args.LastIncludedIndex):]...)
	} else {
		// index 从 1 开始，占位一个
		newLog = append(newLog, LogEntry{Term: args.LastIncludedTerm, Index:args.LastIncludedIndex })
	}
	rf.Log = newLog

	rf.LastIncludedIndex = args.LastIncludedIndex
	rf.LastIncludedTerm = args.LastIncludedTerm
	rf.LastApplied = max(rf.LastIncludedIndex, rf.LastApplied)
	rf.CommitIndex = max(rf.LastIncludedIndex, rf.CommitIndex)


	w1 := new(bytes.Buffer)
	e1 := labgob.NewEncoder(w1)
	e1.Encode(rf.CurrentTerm)
	e1.Encode(rf.VoteFor)
	e1.Encode(rf.Log)
	stateData := w1.Bytes()
	// log 改变了，所以我们同时需要保存State
	rf.persister.SaveStateAndSnapshot(stateData, args.Data)
	// rf.mu.Unlock()
	// rf.resetElectionTimer()
	DPrintf("[-] server [%d], Done InstallSnapshot, newLog LEN %d\n", rf.me, len(rf.Log))
	// 通知 KVserver
	msg := ApplyMsg{
		CommandValid: false,
		Snapshot:args.Data,
	}
	rf.applyCh <- msg

	// fmt.Printf("[-] server [%d],InstallSnapshot msg applied\n", rf.me)
}

func (rf *Raft) sendInstallSnapshot (server int) {
	// 在 append entris 中持锁进入此函数
	DPrintf("[+] Leader %d, sendInstallSnapshot to nextIndex[%d]: %d, Leader LastIncludedInde:%d\n",
		rf.me,  server,  rf.nextIndex[server], rf.LastIncludedIndex)
	args := InstallSnapshotArgs{
		Term:rf.CurrentTerm,
		LeaderID: rf.me,
		LastIncludedIndex: rf.LastIncludedIndex,
		LastIncludedTerm: rf.LastIncludedTerm,
		Data:rf.persister.ReadSnapshot(),
	}
	rf.mu.Unlock()
	// 同步执行

	var reply InstallSnapshotReply

	ok:= rf.peers[server].Call("Raft.InstallSnapshot", &args, &reply)

	if ok {
		rf.mu.Lock()

		if reply.Term > rf.CurrentTerm {
			rf.CurrentTerm = reply.Term
			rf.switchTo(Follower)
			rf.persist()
			rf.mu.Unlock()
			return
		}
		rf.mu.Unlock()
		// 接收回复成功后， 检查此server是否还为Leader
		if _, isLeader := rf.GetState(); isLeader == false {
			return
		}
		rf.mu.Lock()
		rf.nextIndex[server] = args.LastIncludedIndex + 1
		rf.matchIndex[server] = args.LastIncludedIndex
		DPrintf("[-] Leader %d sendInstallSnapshot successful rf.nextIndex[%d]: %d\n", rf.me, server, rf.nextIndex[server])
		// fmt.Printf("Leader %d, send Done \n", rf.me)
		rf.mu.Unlock()
	}


}
func (rf *Raft)addSnapshotIndex(index int ) int {
	return index + rf.LastIncludedIndex
}

func (rf *Raft)subSnapshotIndex(index int) int {
	return index-rf.LastIncludedIndex
}

// field names must start with capital letters!
type AppendEntriesArgs struct {
	Term 			int			// leader's term
	LeaderId		int			// so follower can redirect clients
	PrevLogIndex	int			// index of Log entry immediately preceding new ones
	PrevLogTerm		int			// term of PrevLogIndex entry
	Entries			[]LogEntry	// Log entries to store(empty for heartbeat; may send
	// more than one for efficiency)
	LeaderCommit	int			// leader's CommitIndex
}

type AppendEntriesReply struct {
	Term 			int			// CurrentTerm, for leader to update itself
	ConflictTerm	int			// the term of conflicting entry
	ConflictFirstIndex	int		// the first index it stores for the term of the conflicting entry
	Success			bool		// true if follower contained entry matching
	// prevLogIndex and prevLogTerm
}

func (rf *Raft) getLastLogIndex() int {
	//logs下标从1开始，logs[0]是占位符
	return rf.addSnapshotIndex(len(rf.Log)-1)
}
func min(a int , b int) int{
	if a > b {
		return b
	}
	return a
}
/*
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	//follower收到leader的信息
	rf.mu.Lock()
	//defer后进先出
	defer rf.mu.Unlock()


	reply.Term = rf.CurrentTerm
	reply.Success = false
	reply.ConflictFirstIndex = -1
	reply.ConflictTerm = -1

	//ShardInfo.Printf("me:%2d term:%3d | receive %v\n", rf.me, rf.CurrentTerm, args)
	if args.Term < rf.CurrentTerm {
		return
	}

	if args.Term > rf.CurrentTerm{
		rf.CurrentTerm = args.Term
		reply.Term = rf.CurrentTerm
		rf.convertRoleTo(Follower)
	}
	//遇到心跳信号len(args.entries)==0不能直接返回
	//因为这时可能args.CommitIndex > rf.commintIndex
	//需要交付新的日志

	//通知follower，接收到来自leader的消息
	//即便日志不匹配，但是也算是接收到了来自leader的心跳信息。
	//args.Term >= rf.CurrentTerm
	//logs从下标1开始，log.Entries[0]是占位符
	//所以真实日志长度需要-1

	//如果reply.confictIndex == -1表示follower缺少日志或者leader发送的日志已经被快照
	//leader需要将nextIndex设置为conflicIndex
	if rf.getLastLogIndex() < args.PrevLogIndex{
		//如果follower日志较少
		reply.ConflictTerm = -1
		reply.ConflictFirstIndex = rf.getLastLogIndex() + 1
		InfoRaft.Printf("Raft:%2d term:%3d | receive leader:[%3d] message but lost any message! curLen:%4d prevLoglen:%4d len(Entries):%4d\n",
			rf.me, rf.CurrentTerm, args.LeaderId, rf.getLastLogIndex(), args.PrevLogIndex, len(args.Entries))
		return
		}

	if rf.LastIncludedIndex > args.PrevLogIndex{
		//已经快照了
		//让leader的nextIndex--，直到leader发送快照
		reply.ConflictFirstIndex = rf.LastIncludedIndex + 1
		reply.ConflictTerm = -1
		return
	}

	//接收者日志大于等于leader发来的日志  且 日志项不匹配
	if args.PrevLogTerm != rf.Log[rf.subSnapshotIndex(args.PrevLogIndex)].Term{
		//日志项不匹配，找到follower属于这个term的第一个日志，方便回滚。
		reply.ConflictTerm = rf.Log[rf.subSnapshotIndex(args.PrevLogIndex)].Term
		for i := args.PrevLogIndex; i > rf.LastIncludedIndex ; i--{
			if rf.Log[rf.subSnapshotIndex(i)].Term != reply.ConflictTerm{
				break
			}
			reply.ConflictFirstIndex = i
		}
		return
	}

	//接收者的日志大于等于prevlogindex，且在prevlogindex处日志匹配
	rf.CurrentTerm = args.Term
	reply.Success = true

	//修改日志长度
	//找到接收者和leader（如果有）第一个不相同的日志
	i := 0
	for  ; i < len(args.Entries); i++{
		ind := rf.subSnapshotIndex(i + args.PrevLogIndex + 1)
		if ind < len(rf.Log) && rf.Log[ind].Term != args.Entries[i].Term{
			//修改不同的日志, 截断+新增
			rf.Log = rf.Log[:ind]
			rf.Log = append(rf.Log, args.Entries[i:]...)
			break
		}else if ind >= len(rf.Log){
			//添加新日志
			rf.Log = append(rf.Log, args.Entries[i:]...)
			break
		}
	}


	if len(args.Entries) != 0{
		//心跳信号不输出
		//心跳信号可能会促使follower执行命令
		//心跳信号不改变currentTerm、votedFor、logs，改变角色的函数有persist
		rf.persist()
	}

	//修改CommitIndex
	if args.LeaderCommit > rf.CommitIndex {
		rf.CommitIndex = min(args.LeaderCommit, rf.addSnapshotIndex(len(rf.Log)-1))
		rf.applyCond.Broadcast()
	}
}

*/






func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}


//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term 			int			// candidate's term
	CandidateId		int			// candidate requesting vote
	LastLogIndex	int			// index of candidate's last Log entry($5.4)
	LastLogTerm		int			// term of candidate's last Log entry($5.4)
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term 			int			// CurrentTerm, for candidate to update itself
	VoteGranted		bool		// true means candidate received vote
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).

	rf.mu.Lock()
	defer rf.mu.Unlock()

	// Reply false if term < CurrentTerm, otherwise continue a "voting process"
	if rf.CurrentTerm <= args.Term {

		// if one server's current term is smaller than other's, then it updates
		// it current term to the larger value
		if rf.CurrentTerm < args.Term {

			DPrintf("[RequestVote]: Id %d Term %d State %s\t||\targs's term %d is larger\n",
				rf.me, rf.CurrentTerm, state2name(rf.state), args.Term)

			rf.CurrentTerm = args.Term

			// 如果不是follower，则重置voteFor为-1，以便可以重新投票
			rf.VoteFor = -1

			// 切换到follower状态
			rf.switchTo(Follower)

			// 任期过时，切换为follower，保存下持久状态
			rf.persist()

			// 继续往下，以便符合条件可以进行投票
		}

		// VoteFor is null or candidateId
		if rf.VoteFor == -1 || rf.VoteFor == args.CandidateId {

			// determine which of two Log is more "up-to-date" by comparing
			// the index and term of the last entries in the logs
			lastLogIndex := rf.getLastLogIndex()
			if lastLogIndex < 0 {
				DPrintf("[RequestVote]: Id %d Term %d State %s\t||\tinvalid lastLogIndex: %d\n",
					rf.me, rf.CurrentTerm, state2name(rf.state), lastLogIndex)
			}
			lastLogTerm := rf.Log[rf.subSnapshotIndex(lastLogIndex)].Term

			// If the logs have last entries with different terms, then the Log with the later term is more up-to-date;
			// otherwise, if the logs end with the same term, then whichever Log is longer is more up-to-date.
			// candidate is at least as up-to-date as receiver's Log
			if lastLogTerm < args.LastLogTerm ||
				(lastLogTerm == args.LastLogTerm && lastLogIndex <= args.LastLogIndex) {

				rf.VoteFor = args.CandidateId
				// reset election timeout
				rf.resetElectionTimer()

				rf.switchTo(Follower)
				DPrintf("[RequestVote]: Id %d Term %d State %s\t||\tgrant vote for candidate %d\n",
					rf.me, rf.CurrentTerm, state2name(rf.state), args.CandidateId)

				// 授予投票，更新下持久状态
				rf.persist()

				reply.Term = rf.CurrentTerm
				reply.VoteGranted = true
				return
			}
		}
	}
	reply.Term = rf.CurrentTerm
	reply.VoteGranted = false
}

func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}


/*
func (rf *Raft) broadcastEntries() {
	//leader广播日志
	rf.mu.Lock()
	curTerm := rf.CurrentTerm
	rf.mu.Unlock()

	commitFlag := true //超半数commit只修改一次leader的logs
	commitNum := 1     //记录commit某个日志的节点数量
	commitL := sync.Mutex{}

	for followerId, _ := range rf.peers {
		if followerId == rf.me {
			continue
		}

		//发送信息
		go func(server int) {
			for {

				if _, isLeader := rf.GetState(); isLeader == false {
					return
				}
				rf.mu.Lock()
				//脱离集群很久的follower回来，nextIndex已经被快照了
				//先判断nextIndex是否大于rf.LastIncludedIndex
				next := rf.nextIndex[server]
				if next <= rf.LastIncludedIndex{
					//注意，此时持有rf.mu锁
					rf.sendInstallSnapshot(server)
					return
				}

				appendArgs := &AppendEntriesArgs{curTerm,
					rf.me,
					rf.getPrevLogIndex(server),
					rf.getPrevLogTerm(server),
					rf.Log[rf.subSnapshotIndex(next):],
					rf.CommitIndex}

				rf.mu.Unlock()


				reply := &AppendEntriesReply{}
				//ShardInfo.Printf("me:%2d term:%3d | send %v to %2d\n", rf.me, rf.CurrentTerm, appendArgs, server)
				ok := rf.sendAppendEntries(server, appendArgs, reply);
				if !ok{
					return
				}

				rf.mu.Lock()
				if reply.Term > curTerm {
					//返回的term比发送信息时leader的term还要大
					rf.CurrentTerm = reply.Term
					rf.switchTo(Follower)
					rf.mu.Unlock()
					return
				}
				//发送信息可能很久，所以收到信息后需要确认状态
				//类似于发送一条信息才同步
				//一个新leader刚开始发送心跳信号，即便和follower不一样也不修改follower的日志
				//只有当新Leader接收新消息时，才会修改follower的值，防止figure8的情况发生
				if !rf.checkState(Leader, curTerm) || len(appendArgs.Entries) == 0{
					//如果server当前的term不等于发送信息时的term
					//表明这是一条过期的信息，不要了
					//或者是心跳信号且成功，也直接返回
					//如果是心跳信号，但是append失败，有可能是联系到脱离集群很久的节点，需要更新相应的nextIndex
					//比如一个新leader发送心跳信息，如果一个follower的日志比args.prevLogIndex小
					//那么此时reply失败，需要更新nextIndex
					rf.mu.Unlock()
					return
				}

				if reply.Success {
					//append成功
					//考虑一种情况
					//第一个日志长度为A，发出后，网络延迟，很久没有超半数commit
					//因此第二个日志长度为A+B，发出后，超半数commit，修改leader
					//这时第一次修改的commit来了，因为第二个日志已经把第一次的日志也commit了
					//所以需要忽略晚到的第一次commit
					curCommitLen := appendArgs.PrevLogIndex +  len(appendArgs.Entries)

					if curCommitLen < rf.CommitIndex{
						rf.mu.Unlock()
						return
					}

					//两者相等的时候
					//代表follower的日志长度==match的长度，所以nextIndex需要+1
					if curCommitLen >= rf.matchIndex[server]{
						rf.matchIndex[server] = curCommitLen
						rf.nextIndex[server] = rf.matchIndex[server] + 1
					}

					commitNum = commitNum + 1
					if commitFlag && commitNum > len(rf.peers)/2 {
						//第一次超半数commit
						commitFlag = false
						//leader提交日志，并且修改CommitIndex
						/**
						if there exists an N such that N >  CommitIndex, a majority
						of matchIndex[N] >= N, and log[N].term == currentTerm
						set leader's CommitIndex = N
						如果在当前任期内，某个日志已经同步到绝大多数的节点上，
						并且日志下标大于CommitIndex，就修改CommitIndex。

						rf.CommitIndex = curCommitLen
						rf.applyCond.Broadcast()
					}

					rf.mu.Unlock()
					return
				} else {
					//prevLogIndex or prevLogTerm不匹配
					//如果leader没有conflictTerm的日志，那么重新发送所有日志
					//新加入的节点可能没有日志，其conflitIndex是0

					if reply.ConflictTerm == -1{
						//follower缺日志 或者 上一次发送的日志follower已经快照了
						rf.nextIndex[server] = reply.ConflictFirstIndex
					}else{
						//日志不匹配
						rf.nextIndex[server] = rf.addIdx(1)
						i := reply.ConflictFirstIndex
						for ; i > rf.LastIncludedIndex; i--{
							if rf.Log[rf.subSnapshotIndex(i)].Term == reply.ConflictTerm{
								rf.nextIndex[server] = i + 1
								break
							}
						}
						if i <= rf.LastIncludedIndex && rf.LastIncludedIndex != 0{
							//leader拥有的日志都不能与follwer匹配
							//需要发送快照
							rf.nextIndex[server] = rf.LastIncludedIndex
						}
					}
					rf.mu.Unlock()
				}
			}

		}(followerId)

	}
}
*/
func (rf *Raft) getPrevLogIndex(server int) int{
	//只有leader调用
	return rf.nextIndex[server] - 1
}

func (rf *Raft) getPrevLogTerm(server int) int{
	//只有leader调用
	return rf.Log[rf.subSnapshotIndex(rf.getPrevLogIndex(server))].Term
}


func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()

	// Reply false if term < CurrentTerm, otherwise continue a "consistency check"
	if rf.CurrentTerm <= args.Term {
		// If RPC request or response contains term T > CurrentTerm:
		// set CurrentTerm = T, convert to follower
		if rf.CurrentTerm < args.Term {
			rf.CurrentTerm = args.Term
			rf.resetElectionTimer()
			rf.VoteFor = -1
			rf.switchTo(Follower)
			rf.persist()
			// 继续往下，以便一致性检查通过后进行日志复制
		}
		//
		if rf.LastIncludedIndex > args.PrevLogIndex{
			//已经快照了
			//让leader的nextIndex--，直到leader发送快照
			reply.ConflictFirstIndex = rf.LastIncludedIndex + 1
			reply.ConflictTerm = 0
			rf.mu.Unlock()
			return
		}
		//如果reply.confictIndex == 0 表示follower缺少日志或者leader发送的日志已经被快照
		//leader需要将nextIndex设置为conflicIndex
		if rf.getLastLogIndex() < args.PrevLogIndex{
			//如果follower日志较少
			reply.ConflictTerm = 0
			reply.ConflictFirstIndex = rf.getLastLogIndex() + 1
			rf.mu.Unlock()
			return
		}
		// if the consistency check pass
		// 日志长度大于待添加的日志项index
		if rf.addSnapshotIndex(len(rf.Log)) > args.PrevLogIndex &&
			rf.Log[rf.subSnapshotIndex(args.PrevLogIndex)].Term == args.PrevLogTerm {

			// 收到AppendEntries RPC(包括心跳)，说明存在leader，自己切换为follower状态
			rf.switchTo(Follower)

			// 1. 判断follower中log是否已经拥有args.Entries的所有条目，全部有则匹配！
			isMatch := true
			nextIndex := args.PrevLogIndex+1
			end := rf.getLastLogIndex()
			for i := 0; isMatch && i < len(args.Entries); i++ {
				// 如果args.Entries还有元素，而log已经达到结尾，则不匹配
				if nextIndex + i > end {
					isMatch = false
				} else if rf.Log[rf.subSnapshotIndex(nextIndex+i)].Term != args.Entries[i].Term {
					isMatch = false
				}
			}

			// 2. 如果存在冲突的条目，再进行日志复制
			if isMatch == false {
				// 2.1. 进行日志复制，并更新CommitIndex
				entries := make([]LogEntry, len(args.Entries))
				copy(entries, args.Entries)
				rf.Log = append(rf.Log[:rf.subSnapshotIndex(nextIndex)], entries...) // [0, nextIndex) + entries
			}

			// if leaderCommit > CommitIndex, set CommitIndex = min(leaderCommit, index of last new entry)

			if args.LeaderCommit > rf.CommitIndex {
				rf.CommitIndex = min(args.LeaderCommit, rf.addSnapshotIndex(len(rf.Log)-1))
				// 更新了CommitIndex之后给applyCond条件变量发信号，以应用新提交的entries到状态机
				rf.applyCond.Broadcast()
			}
			// Reset timeout when received leader's AppendEntries RPC
			rf.resetElectionTimer()

			rf.leaderId = args.LeaderId
			rf.persist()

			reply.Term = rf.CurrentTerm
			reply.Success = true
			rf.mu.Unlock()
			return

		} else {
			//如果peer的日志长度小于leader的nextIndex
			nextIndex := args.PrevLogIndex + 1
			// index := nextIndex + len(args.Entries) - 1
			// If a follower does not have prevLogIndex in its log, it should return with
			// conflictIndex = len(log), and conflictTerm = None
			if rf.addSnapshotIndex(len(rf.Log)) < nextIndex {
				reply.ConflictTerm = 0		// conflictTerm等于0，表示非法
				reply.ConflictFirstIndex = rf.addSnapshotIndex(len(rf.Log))
			} else {

				reply.ConflictTerm = rf.Log[rf.subSnapshotIndex(args.PrevLogIndex)].Term
				reply.ConflictFirstIndex = args.PrevLogIndex

				// 递减reply.ConflictFirstIndex直到index为log中第一个term为reply.ConflictTerm的entry
				for i := reply.ConflictFirstIndex - 1; i >= rf.LastIncludedIndex; i-- {
					if rf.Log[rf.subSnapshotIndex(i)].Term != reply.ConflictTerm {
						break
					} else {
						reply.ConflictFirstIndex -= 1
					}
				}
			}
		}
	}

	reply.Term = rf.CurrentTerm
	reply.Success = false
	rf.mu.Unlock()
}

// 并行给其他所有peers发送AppendEntries RPC(包括心跳)，在每个发送goroutine中实时统计
// 已发送RPC成功的个数，当达到多数者条件时，提升CommitIndex到index，并通过一次心跳
// 通知其他所有peers提升自己的CommitIndex。
func (rf *Raft) broadcastAppendEntries(index int, term int, CommitIndex int, nReplica int, name string) {
	var wg sync.WaitGroup
	majority := len(rf.peers)/2 + 1
	isAgree := false

	// 只有leader可以发送AppendEntries RPC(包括心跳)
	if _, isLeader := rf.GetState(); isLeader == false {
		return
	}

	rf.mu.Lock()
	if rf.CurrentTerm != term {
		rf.mu.Unlock()
		return
	}
	rf.mu.Unlock()

	for i, _ := range rf.peers {

		if i == rf.me {
			continue
		}
		wg.Add(1)

		go func(i int, rf *Raft) {
			defer wg.Done()
			// 在AppendEntries RPC一致性检查失败后，递减nextIndex，重试
		retry:
			// 因为涉及到retry操作，避免过时的leader继续执行
			if _, isLeader := rf.GetState(); isLeader == false {
				return
			}

			// 避免进入新任期，还发送过时的AppendEntries RPC
			rf.mu.Lock()
			if rf.CurrentTerm != term {
				rf.mu.Unlock()
				return
			}
			rf.mu.Unlock()

			rf.mu.Lock()
			nextIndex := rf.nextIndex[i]
			// 发送InstallSnapshot 请求
			if nextIndex <= rf.LastIncludedIndex {
				// 持锁进入sendSnapshot
				rf.sendInstallSnapshot(i)
				return
			}

			prevLogIndex := nextIndex - 1
			prevLogTerm := rf.Log[rf.subSnapshotIndex(prevLogIndex)].Term

			entries := make([]LogEntry, 0)
			if nextIndex < index+1 {
				entries = rf.Log[rf.subSnapshotIndex(nextIndex): rf.subSnapshotIndex(index+1)]		// [nextIndex, index+1)
			}

			args := AppendEntriesArgs{
				Term:term,
				LeaderId:rf.me,
				PrevLogIndex:prevLogIndex,
				PrevLogTerm:prevLogTerm,
				Entries:entries,
				LeaderCommit:CommitIndex,
			}

			rf.mu.Unlock()
			var reply AppendEntriesReply
			// fmt.Printf("[---]Leader %d  Append to  server %d entries PrevLogIndex:%d, PrevLogTerm:%d\n",rf.me, i ,prevLogIndex,prevLogTerm)
			ok := rf.sendAppendEntries(i, &args, &reply)

			if ok == false {
				return
			}
			rf.mu.Lock()
			if rf.CurrentTerm != args.Term {
				rf.mu.Unlock()
				return
			}
			rf.mu.Unlock()
			// AppendEntries RPC被拒绝，原因可能是leader任期过时，或者一致性检查未通过
			// 发送心跳也可能出现一致性检查不通过，因为一致性检查是查看leader的nextIndex之前
			// 即(prevLogIndex)的entry和指定peer的log中那个索引的日志是否匹配。即使心跳中
			// 不携带任何日志，但一致性检查仍会因为nextIndex而失败，这时需要递减nextIndex然后重试。
			if reply.Success == false {
				rf.mu.Lock()

				if args.Term < reply.Term {
					rf.CurrentTerm = reply.Term
					rf.VoteFor = -1
					rf.switchTo(Follower)
					// 任期过时，切换为follower，更新下持久状态
					rf.persist()
					rf.mu.Unlock()
					// 任期过时，直接返回
					return
				} else {
					//新加入的节点可能没有日志，其conflitIndex是0
					DPrintf("Leader %d conflitIndex %d, ",rf.me, reply.ConflictFirstIndex )
					nextIndex := rf.getNextIndex(reply, nextIndex)
					DPrintf("getNextIndex %d\n", nextIndex )
					// 更新下leader为该peer保存的nextIndex
					rf.nextIndex[i] = nextIndex
					rf.mu.Unlock()
					goto retry
				}
			} else {	// AppendEntries RPC发送成功
				rf.mu.Lock()
				// 如果当前index更大，则更新该peer对应的nextIndex和matchIndex
				if rf.nextIndex[i] < index + 1 {
					rf.nextIndex[i] = index + 1
					rf.matchIndex[i] = index
				}
				nReplica += 1
				// 如果已经将该entry复制到了大多数peers，接着检查index编号的这条entry的任期是否为当前任期，
				// 如果是则可以提交该条目
				if isAgree == false && rf.state == Leader && nReplica >= majority {
					isAgree = true
					// 如果index大于CommitIndex，而且index编号的entry的任期等于当前任期，提交该entry
					if rf.CommitIndex < index && rf.Log[rf.subSnapshotIndex(index)].Term == rf.CurrentTerm {
						rf.CommitIndex = index
						go rf.broadcastHeartbeat()
						rf.applyCond.Broadcast()
					}
				}
				rf.mu.Unlock()
			}
		}(i, rf)
	}
	// 等待所有发送AppendEntries RPC的goroutine退出
	wg.Wait()
}
func (rf *Raft) getNextIndex(reply AppendEntriesReply, nextIndex int) int {


	// reply's conflictTerm=0，表示None。说明peer:i的log长度小于nextIndex。
	if reply.ConflictTerm == 0 {
		// follower 缺日志 或者 上一次发送的日志 follower已经快照了
		nextIndex = reply.ConflictFirstIndex
	} else {
		// 日志不匹配
		/*
		nextIndex = rf.addSnapshotIndex(1)
		conflictIndex := reply.ConflictFirstIndex
		for ; conflictIndex > rf.LastIncludedIndex; conflictIndex-- {
			if rf.Log[rf.subSnapshotIndex(conflictIndex)].Term == reply.ConflictTerm {
				nextIndex = conflictIndex -1
				break
			}
		}

		if conflictIndex <= rf.LastIncludedIndex && rf.LastIncludedIndex != 0 {
			// Leader 拥有的日志都不能与 follower 匹配
			// 需要发送快照
			nextIndex = rf.LastIncludedIndex
		}
		*/

		DPrintf("leader %d[getNextIndex], conflictFirstIndex:%d\n", rf.me,reply.ConflictFirstIndex )
		conflictIndex := reply.ConflictFirstIndex
		conflictTerm := rf.Log[rf.subSnapshotIndex(conflictIndex)].Term
		// 只有conflictTerm大于或等于reply's conflictTerm，才有可能或一定找得到任期相等的entry
		if conflictTerm >= reply.ConflictTerm {
			// 从reply.ConflictFirstIndex处开始向搜索，寻找任期相等的entry
			for i := conflictIndex; i >= rf.LastIncludedIndex; i-- {
				if rf.Log[rf.subSnapshotIndex(i)].Term == reply.ConflictTerm {
					break
				}
				conflictIndex -= 1
			}
			// conflictIndex不为0，leader的log中存在同任期的entry
			if conflictIndex != rf.LastIncludedIndex {
				// 向后搜索，使得conflictIndex为最后一个任期等于reply.ConflictTerm的entry
				for i := conflictIndex+1; i < nextIndex; i++ {
					if rf.Log[rf.subSnapshotIndex(i)].Term != reply.ConflictTerm {
						break
					}
					conflictIndex += 1
				}
				nextIndex = conflictIndex + 1
			} else {	// conflictIndex等于0，说明不存在同任期的entry
				nextIndex = reply.ConflictFirstIndex
			}
		} else {	// conflictTerm < reply.ConflictTerm，并且必须往前搜索，所以一定找不到任期相等的entry
			nextIndex = reply.ConflictFirstIndex
		}

	}
	return nextIndex
}
//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's Log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft Log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).
	if term, isLeader = rf.GetState(); isLeader {

		// 1. leader将客户端command作为新的entry追加到自己的本地log
		rf.mu.Lock()
		index = len(rf.Log) + rf.LastIncludedIndex
		logEntry := LogEntry{Command:command, Term:rf.CurrentTerm, Index:index}
		rf.Log = append(rf.Log, logEntry)
		// lab3 log compasion 需要修改 index
		// index = len(rf.Log) - 1
		// log[0]为占位符， 所以当log长度为3时，log中有两个有效元素
		// 再添加一个元素其 index 应为 len(rf.log) = 3
		nReplica := 1
		// 发送AppendEntries RPC时也更新下最近发送时间
		rf.latestIssueTime = time.Now().UnixNano()
		// 接收到客户端命令，并写入log，保存下持久状态
		rf.persist()
		rf.mu.Unlock()
		// 2. 给其他peers并行发送AppendEntries RPC以复制该entry
		rf.mu.Lock()
		go rf.broadcastAppendEntries(index, rf.CurrentTerm, rf.CommitIndex, nReplica, "Start")
		rf.mu.Unlock()

	}
	return index, term, isLeader
}

// 按顺序(in order)发送已提交的(committed)日志条目到applyCh的goroutine。
// 该goroutine是单独的(separate)、长期运行的(long-running)，在没有新提交
// 的entries时会等待条件变量；当更新了CommitIndex之后会给条件变量发信号，
// 以唤醒该goroutine执行提交。
func (rf *Raft) applyEntries() {
	for {

		rf.mu.Lock()
		CommitIndex := rf.CommitIndex
		LastApplied := rf.LastApplied
		rf.mu.Unlock()

		if LastApplied == CommitIndex {
			rf.mu.Lock()
			rf.applyCond.Wait()
			rf.mu.Unlock()
		} else {

			for i := LastApplied+1; i <= CommitIndex; i++ {

				rf.mu.Lock()
				if rf.subSnapshotIndex(i) <= 0 {
					fmt.Printf("[applyEntries]: Id %d CommandIndex %d subLast %d\t||\tLastApplied %d and CommitIndex %d\n",
						rf.me, i,  rf.subSnapshotIndex(i) ,LastApplied, CommitIndex,)

					if rf.subSnapshotIndex(i)  < 0 {
						i -= (rf.subSnapshotIndex(i) + 1)
					}
					rf.mu.Unlock()
					continue
				}
				// 执行过程如果 rf.LastIncludedIndex 被改变
				// subSnapshotIndex 就会出现负数问题
				// 如果是负数，选择continue 是否可行？
				applyMsg := ApplyMsg{
					CommandValid:true,
					Command:rf.Log[rf.subSnapshotIndex(i)].Command,
					CommandIndex:i,
					CommandTerm: rf.Log[rf.subSnapshotIndex(i)].Term,
				}
				rf.LastApplied = i
				rf.mu.Unlock()
				rf.applyCh <- applyMsg
			}

		}
	}
}

//
// the tester calls Kill() when a Raft instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (rf *Raft) Kill() {
	// Your code here, if desired.
}


func state2name(state int) string {
	var name string
	if state == Follower {
		name = "Follower"
	} else if state == Candidate {
		name = "Candidate"
	} else if state == Leader {
		name = "Leader"
	}
	return name
}

// 统一处理Raft状态转换。这么做的目的是为了没有遗漏的处理nonLeader与leader状态之间转换时需要给对应
// 的条件变量发信号的工作。：
// 	Leader -> nonLeader(Follower): rf.nonLeaderCond.broadcast()
//	nonLeader(Candidate) -> Leader: rf.leaderCond.broadcast()
// 为了避免死锁，该操作不加锁，由外部加锁保护！
func (rf *Raft) switchTo(newState int) {
	oldState := rf.state
	rf.state = newState
	if oldState == Leader && newState == Follower {
		rf.nonLeaderCond.Broadcast()
	} else if oldState == Candidate && newState == Leader {
		DPrintf("server %d to be a Leader\n", rf.me)
		rf.leaderCond.Broadcast()
	}
}

// 选举超时(心跳超时)检查器，定期检查自最新一次从leader那里收到AppendEntries RPC(包括heartbeat)
// 或给予candidate的RequestVote RPC请求的投票的时间(latestHeardTIme)以来的时间差，是否超过了
// 选举超时时间(electionTimeout)。若超时，则往electionTimeoutChan写入数据，以表明可以发起选举。
func (rf *Raft) electionTimeoutTick() {
	for {
		// 如果peer是leader，则不需要选举超时检查器，所以等待nonLeaderCond条件变量
		if _, isLeader := rf.GetState(); isLeader {
			rf.mu.Lock()
			rf.nonLeaderCond.Wait()
			rf.mu.Unlock()
		} else {
			rf.mu.Lock()
			elapseTime := time.Now().UnixNano() - rf.latestHeardTime
			if int(elapseTime/int64(time.Millisecond)) >= rf.electionTimeout {
				// 选举超时，peer的状态只能是follower或candidate两种状态。
				// 若是follower需要转换为candidate发起选举； 若是candidate
				// 需要发起一次新的选举。---所以这里设置状态为Candidate---。
				// 这里不需要设置state为Candidate，因为总是要发起选举，在选举
				// 里面设置state比较合适，这样不分散。
				//rf.state = Candidate
				rf.electionTimeoutChan <- true
			}
			rf.mu.Unlock()
			// 休眠10ms，作为tick的时间间隔。如果休眠时间太短，比如1ms，将导致频繁检查选举超时，
			// 造成测量到的user时间，即CPU时间增长，可能超过5秒。
			time.Sleep(time.Millisecond*10)
		}
	}
}

// 心跳发送周期检查器。leader检查距离上次发送心跳的时间(latestIssueTime)是否超过了心跳周期(heartbeatPeriod)，
// 若超过则写入数据到heartbeatPeriodChan，以通知发送心跳
func (rf *Raft) heartbeatPeriodTick() {
	for {
		// 如果peer不是leader，则等待leaderCond条件变量
		if term, isLeader := rf.GetState(); isLeader == false {
			rf.mu.Lock()
			rf.leaderCond.Wait()
			rf.mu.Unlock()
		} else {
			rf.mu.Lock()
			elapseTime := time.Now().UnixNano() - rf.latestIssueTime
			if int(elapseTime/int64(time.Millisecond)) >= rf.heartbeatPeriod {
				DPrintf("[HeartbeatPeriodTick]: Id %d Term %d State %s\t||\theartbeat period elapsed," +
					" issue heartbeat\n", rf.me, term, state2name(rf.state))
				rf.heartbeatPeriodChan <- true
			}
			rf.mu.Unlock()
			// 休眠10ms，作为tick的时间间隔。如果休眠时间太短，比如1ms，将导致频繁检查选举超时，
			// 造成测量到的user时间，即CPU时间增长，可能超过5秒。
			time.Sleep(time.Millisecond*10)
		}
	}
}

// 消息处理主循环，处理两种互斥的时间驱动的时间到期：
// 1) 心跳周期到期； 2) 选举超时。
func (rf *Raft) eventLoop() {
	for {
		select {
		case <- rf.electionTimeoutChan:
			rf.mu.Lock()
			DPrintf("[EventLoop]: Id %d Term %d State %s\t||\telection timeout, start an election\n",
				rf.me, rf.CurrentTerm, state2name(rf.state))
			rf.mu.Unlock()
			go rf.startElection()
		case <- rf.heartbeatPeriodChan:
			rf.mu.Lock()
			DPrintf("[EventLoop]: Id %d Term %d State %s\t||\theartbeat period occurs, broadcast heartbeats\n",
				rf.me, rf.CurrentTerm, state2name(rf.state))
			rf.mu.Unlock()
			go rf.broadcastHeartbeat()
		}
	}
}

// leader给其他peers广播一次心跳。因为发送心跳也要进行一致性检查，
// 为了不因为初始时的日志不一致而使得心跳发送失败，而其他peers因为
// 接收不到心跳而心跳超时，进而发起不需要的(no-needed)选举，所以
// 发送心跳也需要在一致性检查失败时进行重试。
func (rf *Raft) broadcastHeartbeat() {

	// 非leader不能发送心跳
	if _, isLeader := rf.GetState(); isLeader == false {
		return
	}

	rf.mu.Lock()
	// 发送心跳时更新下发送时间
	rf.latestIssueTime = time.Now().UnixNano()
	rf.mu.Unlock()

	rf.mu.Lock()
	index := rf.getLastLogIndex()
	nReplica := 1
	go rf.broadcastAppendEntries(index, rf.CurrentTerm, rf.CommitIndex, nReplica, "Broadcast")
	rf.mu.Unlock()

}


// 重置election timer，不加锁
func (rf *Raft) resetElectionTimer() {
	// 随机化种子以产生不同的伪随机数序列
	rand.Seed(time.Now().UnixNano())
	// 重新选举随机的electionTimeout
	rf.electionTimeout = rf.heartbeatPeriod*5 + rand.Intn(300-150)
	// 因为重置了选举超时，所以也需要更新latestHeardTime
	rf.latestHeardTime = time.Now().UnixNano()
}

// 发起一次选举，在一个新的goroutine中并行给其他每个peers发送RequestVote RPC，并等待
// 所有发起RequestVote的goroutine结束。不能等所有发送RPC的goroutine结束后再统计投票，
// 选出leader，因为这样一个peer阻塞不回复RPC，就会造成无法选出leader。所以需要在发送RPC
// 的goroutine中及时统计投票结果，达到多数投票，就立即切换到leader状态。
func (rf *Raft) startElection() {
	rf.mu.Lock()
	// 再次设置下状态
	rf.switchTo(Candidate)
	// start election:
	// 	1. increase CurrentTerm
	rf.CurrentTerm += 1
	//  2. vote for self
	rf.VoteFor = rf.me
	nVotes := 1

	rf.resetElectionTimer()
	rf.persist()
	rf.mu.Unlock()

	// 	4. send RequestVote RPCs to all other servers in parallel
	// 创建一个goroutine来并行给其他peers发送RequestVote RPC，由其等待并行发送RPC的goroutine结束
	go func(nVotes *int, rf *Raft) {
		var wg sync.WaitGroup
		winThreshold := len(rf.peers)/2 + 1

		for i, _ := range rf.peers {
			// 跳过发起投票的candidate本身
			if i == rf.me {
				continue
			}

			rf.mu.Lock()
			wg.Add(1)
			lastLogIndex := rf.getLastLogIndex()
			if lastLogIndex < 0 {
				DPrintf("[StartElection]: Id %d Term %d State %s\t||\tinvalid lastLogIndex %d\n",
					rf.me, rf.CurrentTerm, state2name(rf.state), lastLogIndex)
			}
			args := RequestVoteArgs{Term: rf.CurrentTerm, CandidateId: rf.me,
				LastLogIndex: lastLogIndex, LastLogTerm: rf.Log[rf.subSnapshotIndex(lastLogIndex)].Term}
			rf.mu.Unlock()
			var reply RequestVoteReply

			// 使用goroutine单独给每个peer发起RequestVote RPC
			go func(i int, rf *Raft, args *RequestVoteArgs, reply *RequestVoteReply) {
				defer wg.Done()

				ok := rf.sendRequestVote(i, args, reply)
				// 发送RequestVote请求失败
				if ok == false {
					return
				}

				rf.mu.Lock()
				if rf.CurrentTerm != args.Term {
					rf.mu.Unlock()
					return
				}
				rf.mu.Unlock()

				// 请求发送成功，查看RequestVote投票结果
				// 拒绝投票的原因有很多，可能是任期较小，或者log不是"up-to-date"
				if reply.VoteGranted == false {

					rf.mu.Lock()
					defer rf.mu.Unlock()
					// If RPC request or response contains T > CurrentTerm, set CurrentTerm = T,
					// convert to follower
					if rf.CurrentTerm < reply.Term {
						rf.CurrentTerm = reply.Term
						// 作为candidate，之前投票给自己了，所以这里重置voteFor，以便可以再次投票
						rf.VoteFor = -1
						rf.switchTo(Follower)
						rf.persist()
					}

				} else {
					// 获得了peer的投票
					rf.mu.Lock()
					*nVotes += 1
					// 如果已经获得了多数投票，并且是Candidate状态，则切换到leader状态
					if rf.state == Candidate && *nVotes >= winThreshold {

						DPrintf("[StartElection]: Id %d Term %d State %s\t||\twin election with nVotes %d\n",
							rf.me, rf.CurrentTerm, state2name(rf.state), *nVotes)

						rf.switchTo(Leader)

						rf.leaderId = rf.me

						// leader启动时初始化所有的nextIndex为其log的尾后位置
						for i := 0; i < len(rf.peers); i++ {
							rf.nextIndex[i] = rf.getLastLogIndex() + 1
							rf.matchIndex[i] = 0
						}
						// 不能通过写入heartbeatPeriodChan的方式表明可以发送心跳，因为
						// 写入操作会阻塞直到eventLoop中读取该channel，而此时需要立即
						// 发送一次心跳，以避免其他peer因超时发起无用的选举。
						go rf.broadcastHeartbeat()

						// 赢得了选举，保存下持久状态
						// rf.persist()
					}
					rf.mu.Unlock()
				}

			}(i, rf, &args, &reply)
		}

		// 等待素有发送RPC的goroutine结束
		wg.Wait()

	}(&nVotes, rf)
}

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).

	// 调用Make()时是创建该Raft实例，此时该实例没有并发的goroutines，无需加锁
	// Part 2A
	rf.applyCh = applyCh
	rf.state = Follower
	rf.leaderId = -1
	rf.applyCond = sync.NewCond(&rf.mu)
	rf.leaderCond = sync.NewCond(&rf.mu)
	rf.nonLeaderCond = sync.NewCond(&rf.mu)
	rf.heartbeatPeriod = 120	// 因为要求leader每秒发送的心跳RPCs不能超过10次，
	// 这里心跳周期取最大值100ms
	rf.resetElectionTimer()
	rf.electionTimeoutChan = make(chan bool)
	rf.heartbeatPeriodChan = make(chan bool)

	// initialized to 0 on first boot, increases monotonically
	rf.CurrentTerm = 0
	rf.VoteFor = -1 // -1意味着没有给任何peer投票

	rf.CommitIndex = 0
	rf.LastApplied = 0

	rf.LastIncludedIndex = 0
	rf.LastIncludedTerm = -1
	// each entry of Log contains command for state machine, and term
	// when entry was received by leader(**first index is 1**)
	// 也就是说，log中第0个元素不算有效entry，合法entry从下标1计算。
	rf.Log = make([]LogEntry, 0)
	// 占位
	rf.Log = append(rf.Log, LogEntry{Term: 0})

	// 初始化nextIndex[]和matchIndex[]的大小
	size := len(rf.peers)
	rf.nextIndex = make([]int, size)
	// matchIndex元素的默认初始值即为0
	rf.matchIndex = make([]int, size)

	go rf.electionTimeoutTick()
	go rf.heartbeatPeriodTick()
	go rf.eventLoop()
	go rf.applyEntries()

	// initialize from state persisted before a crash
	rf.mu.Lock()
	rf.readPersist(persister.ReadRaftState())
	rf.mu.Unlock()
	return rf
}
