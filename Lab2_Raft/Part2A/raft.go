package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"sync"
    "labrpc"
	"time"
	"fmt"
	"math/rand"
	"sync/atomic"
)

// import "bytes"
// import "labgob"

const (
	FOLLOWER = iota
	CANDIDATE
	LEADER
)
// heart beat 50ms
// Follower 150 ~ 300 ms
const (
	HEART_BEAT = 50 * time.Millisecond
	ELEC_TIME_MIN = 150
	ELEC_TIME_MAX = 300
)
//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in Lab 3 you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh; at that point you can add fields to
// ApplyMsg, but set CommandValid to false for these other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
}

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

// atomic operations
func (rf *Raft) getTerm() int32 {
	return atomic.LoadInt32(&rf.currentTerm)
}

func (rf *Raft) isState(state int32) bool {
	return atomic.LoadInt32(&rf.state) == state
}

func (rf *Raft) incrementTerm() {
	atomic.AddInt32(&rf.currentTerm, 1)
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


//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
}


//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}




//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term         int32
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int32
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term         int32
	VoteGranted  bool
}


type AppendEntryArgs struct {
	Term         int32  // leader's term
	LeaderId     int  // so follower can redirect clients
	PrevLogIndex int  // index of log entry immediately preceding new ones
	PrevLogTerm  int32  // term of prevLogIndex entry
	Entries      []Log  // (empty for heartbeat; may send more than one for efficiency)
	LeaderCommit int    // leader's commitIndex
}
type AppendEntryReply struct {
	Term    int32     // currentTerm, for leader to update itself
	Success bool    // true if follwer contained entry matching prevLogTerm and prevLogIndex
}
//
// example RequestVote RPC handler.
//
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

func (rf *Raft) AppendEntries(args *AppendEntryArgs, reply *AppendEntryReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// fmt.Printf("[AppendEntries]: Id %d Term %d State %d receive a AppendEntry req from %d\n",
	//	 rf.me, rf.currentTerm, rf.state, args.LeaderId)
	// 1. Reply false if term < currentTerm
	if args.Term < rf.currentTerm {
		reply.Success = false
		reply.Term = rf.currentTerm
	} else if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.switchTo(FOLLOWER)
		reply.Success = true
		rf.resetTimer()
	} else {
		// fmt.Printf("[AppendEntries]: Id %d Term %d State %d\t||\tcommitIndex %d while leaderCommit %d\n",
		//	 rf.me, rf.currentTerm, rf.state, rf.commitIndex, args.LeaderCommit)
		// rf.currentTerm = args.Term
		reply.Success = true
		rf.resetTimer()
	}
	/*
	go func() {
		rf.appendCh <- struct{}{}
	}()
	*/
		// 2. Reply false if log doesn't contain an entry at PrevLogIndex whose term matches PrevLogTerm
		// 3. if an existing entry conflicts with a new one (same index but different terms), delete the existing entry and all that follow it
		/*
		if args.PrevLogIndex >= 0 && ( len(rf.logs)-1 < args.PrevLogIndex || rf.logs[args.PrevLogIndex].Term != args.PrevLogTerm) {
			reply.Success = false
			reply.Term = rf.currentTer
		} else if args.Entries != nil {
			rf.logs = rf.logs[:args.PrevLogIndex]
			fr.logs = append(rf.logs, args.Entries...)
			if len(rf.logs)-1 >= args.LeaderCommit {
				rf.commitIndex = args.LeaderCommit
				go rf.commitLogs()
			}

			reply.success = true

		} else {

		}
		*/

}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

func (rf *Raft)sendAppendEntries(server int , args *AppendEntryArgs, reply *AppendEntryReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

/*
 * 向所有其他服务器发送请求投票
 */
func (rf *Raft) broadcastVoteReq() {
	args := RequestVoteArgs {
		Term:          rf.currentTerm,
		CandidateId:   rf.me ,
		LastLogIndex:  len(rf.logs) -1,
	}
	if len(rf.logs) > 0 {
		args.LastLogTerm = rf.logs[len(rf.logs)-1].Term
	}

	for i := 0; i < len(rf.peers); i++ {
		if (i == rf.me) {
			continue
		}
		go func(server int, args RequestVoteArgs) {

			var reply RequestVoteReply
			// 检查此时状态是否还为 CANDIDATE, 以防止在 Server 转换为其他状态后，其他线程再进行处理
			if rf.isState(CANDIDATE) && rf.sendRequestVote(server, &args, &reply) {
				rf.mu.Lock()
				defer rf.mu.Unlock()
				if reply.VoteGranted == true {
					rf.VoteGrantedCount += 1
				} else {
					if reply.Term > rf.currentTerm {
						rf.currentTerm = reply.Term
						rf.switchTo(FOLLOWER)
					}

				}
			} //else {
				// 若进入这个 else， 表明此时 server 的状态已经改变了，之后的投票也失去了意义。 或者发送请求失败
			//	fmt.Printf("Server %d send vote req failed.\n", rf.me)
			//}

		}(i, args)
	}
}

func (rf *Raft) broadcastAppendEntries() {

	var args AppendEntryArgs
	args.Term = atomic.LoadInt32(&rf.currentTerm)
	args.LeaderId = rf.me
	args.LeaderCommit = rf.commitIndex

	for i := 0; i < len(rf.peers); i++ {
		if i == rf.me {
			continue
		}
		/*
		// construct parameters
		args.PrevLogIndex = rf.nextIndex[i] - 1
		if args.PrevLogIndex >= 0 {
			args.PrevLogTerm = rf.logs[args.PrevLogIndex].term
		}
		if rf.nextIndex[i] < len(rf.logs) {
			// 存在待同步的 entry
			args.Entries = rf.logs[rf.nextIndex[i]:]
		}
		*/
		// start to broadcast
		go func(server int, args AppendEntryArgs) {
			var reply AppendEntryReply
			rf.mu.Lock()
			defer rf.mu.Unlock()
			if rf.isState(LEADER)  && rf.sendAppendEntries(server, &args, &reply) {
				if reply.Success == false {
					if reply.Term > rf.currentTerm {
						rf.currentTerm = reply.Term
						rf.switchTo(FOLLOWER)
						rf.resetTimer()
					}
				}
			}
		}(i, args)
	}
}


func (rf *Raft) commitLogs() {
	rf.mu.Lock()
	rf.mu.Unlock()
}
//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
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


	return index, term, isLeader
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

func (rf *Raft) switchTo(state int32) {
	if rf.isState(state) {
		return
	}
	preState := rf.state

	switch state {
	case FOLLOWER:
		rf.state = FOLLOWER
		// ready to next election
		rf.votedFor = -1
	case CANDIDATE:
		rf.state = CANDIDATE
	case LEADER:
		// fmt.Printf("Term: %d: Server %d to be a leader\n",rf.currentTerm, rf.me)
		rf.state = LEADER

	default:
		fmt.Printf("Warning: invaid state %d, do nothing\n", state)
	}


	fmt.Printf("Term: %d: Server %d transfer from %d to %d\n",
		rf.currentTerm, rf.me, preState, rf.state)


}



func (rf *Raft)startElection() {
	rf.incrementTerm()
	rf.votedFor = rf.me
	rf.VoteGrantedCount = 1
	rf.resetTimer()
	rf.broadcastVoteReq()
}

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
				// 这样循环会有Bug, 等待投票时会再一次阻塞到这个case中， 然后就会导致 currentTerm++
				/*
				<- rf.electionTimer.C
				rf.resetTimer()
				// election time out ， what we should do? do it again
				rf.startElection()
				*/
				// 这个 Lock 等待由follower to candidate 开始选举完成
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
				// ftm.Printf("I'm a Candidate\n")
			case LEADER:

				rf.broadcastAppendEntries()
				time.Sleep(HEART_BEAT)
				// fmt.Printf("Term: %d, Server: %d I'm a leader! \n", rf.currentTerm, rf.me)

		}
	}
}

func randElectionDuration() time.Duration {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return time.Millisecond * time.Duration(r.Int63n(ELEC_TIME_MAX-ELEC_TIME_MIN) + ELEC_TIME_MIN)
}

func (rf *Raft) resetTimer() {
	newTimeout := randElectionDuration()
	/*
	if rf.state != LEADER {
		newTimeout = randElectionDuration()
	}
	*/
	// fmt.Printf("Term: %d , Server %d,  Resettimer %d\n", rf.currentTerm, rf.me, newTimeout);
	rf.electionTimer.Reset(newTimeout)
}
