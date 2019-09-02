package raftkv

import (
	"labgob"
	"labrpc"
	"log"
	"raft"
	"sync"
    "time"
	"bytes"
)

const TIME_OUT = time.Second * 3
const (
    OP_GET = iota
    OP_APPEND
    OP_PUT
)



const Debug = 0

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug > 0 {
		log.Printf(format, a...)
	}
	return
}


type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
    Type       int
    Key        string
    Value      string
    ClientID   int64
    ClientOpIndex int64    // client Operation's index
}

type PendingOps struct {
    isApplied  chan bool
    op *Op
}


type KVServer struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg

	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	persister  *raft.Persister
	// K/V数据域
    data map[string]string
	// 每个客户端的操作索引（计数）
    opIndex map[int64]int64
    // every client might put Op in same ClientOpIndex
    pendingOps map[int][]*PendingOps

	// snapshot
	opAppliedIndex   int
	opAppliedTerm int
}
/*
 *  操作是否成功执行, IsLeader
*/
func (kv *KVServer) execOp(op Op) (bool, bool){
    opIndex, _, isLeader := kv.rf.Start(op)

    if !isLeader {
		// 操作失败， 且不是leader
        return false, false
    }
	DPrintf("kvserver Leader[%d] Start exec PUT/APPEND clientIndex: %d in Index:%d\n", kv.me, op.ClientOpIndex, opIndex)

	kv.mu.Lock()
    wait := make(chan bool, 0)
    //DPrintf("Leader %v Append to pendingOps index[%v] op:[%v]",kv.me, opIndex, op)
    pOp := PendingOps{wait, &op}
    kv.pendingOps[opIndex] = append(kv.pendingOps[opIndex], &pOp)
	kv.mu.Unlock()


    var ok bool
    timer := time.NewTimer(TIME_OUT)
    select {
    // if threr is no default case, `select` will be blocked
    // until a case pass the evaluation
    //`wait` channel will be changed in apply func
    case ok = <- wait:
    case <- timer.C:
        ok = false
    }
	// 丢弃所有， 对未成功的op， 客户端会再次尝试
	if ok {
		DPrintf("kvserver[%d] [Donex exec client:%d, clientIndex: %d in Index:%d]\n", kv.me, op.ClientID, op.ClientOpIndex, opIndex)
	}

	// 此处一定会等所有 wait channal 写完后及进行 delete
	kv.mu.Lock()
	delete(kv.pendingOps, opIndex)
	kv.mu.Unlock()
	// 操作结果， isleader
    return ok, true
}

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
    var op Op
    op.Type = OP_GET
    op.Key  = args.Key
    op.ClientOpIndex = args.ClientOpIndex
    op.ClientID = args.ClientID
	success, isLeader := kv.execOp(op)
    reply.WrongLeader  = !isLeader
	// 论文中说明，server需要返回 LeaderID,但在这里我们就直接简化了，让客户端不断请求找Leader
	if !success {
		if reply.WrongLeader {
	        reply.Err = ERR_NOT_LEADER
		} else {
			reply.Err = TIME_OUT_OR_OTHER
		}
	} else {
        kv.mu.Lock()
        defer kv.mu.Unlock()

        if value, ok := kv.data[args.Key]; ok {
            // K/V is exist
            reply.Value = value
            reply.Err = OK
        } else {
            reply.Err = ERR_NO_KEY
        }
    }
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
    var op Op

    if args.Op == "Put" {
        op.Type = OP_PUT
    } else {
        op.Type = OP_APPEND
    }

    op.Key = args.Key
    op.Value = args.Value
    op.ClientID = args.ClientID
    op.ClientOpIndex = args.ClientOpIndex

	success, isLeader := kv.execOp(op)

    reply.WrongLeader  = !isLeader
	if !success {
		if reply.WrongLeader {
	        reply.Err = ERR_NOT_LEADER
		} else {

			reply.Err = TIME_OUT_OR_OTHER
		}
	} else {
        reply.Err = OK
    }
}

//
// the tester calls Kill() when a KVServer instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (kv *KVServer) Kill() {
	kv.rf.Kill()
	// Your code here, if desired.
}


func (kv *KVServer) Apply(applyMsg *raft.ApplyMsg ) {


	if applyMsg.CommandValid == false {
		// readSnapshot
		kv.mu.Lock()
		DPrintf(" Get a snapshot command\n")
		kv.readSnapshot(applyMsg.Snapshot)
		kv.mu.Unlock()
		DPrintf(" Done snapshot command\n")
		return
	}

	kv.mu.Lock()
	defer kv.mu.Unlock()
	var op Op
	// Type assertion
	op = applyMsg.Command.(Op)
    if  kv.opIndex[op.ClientID] >= op.ClientOpIndex {
        // current Op requested by client is duplicate
        DPrintf("Duplicate operation client %d\n", op.ClientID)
    } else {
		DPrintf("kvserver[%d], apply op.Type: %d, command:%d, commandTerm:%d, stateSize %d\n",
			kv.me, op.Type, applyMsg.CommandIndex, applyMsg.CommandTerm, kv.persister.RaftStateSize())


        switch op.Type {
            case OP_PUT:
				// DPrintf("Server[%v],Put Key/Value %v/%v\n", kv.me, op.Key, op.Value)
                kv.data[op.Key] = op.Value
            case OP_APPEND:
				// DPrintf("Append Key/Value %v/%v\n", op.Key, op.Value)
				if _, ok := kv.data[op.Key]; ok {
					val := kv.data[op.Key]
	                kv.data[op.Key] = val + op.Value
				} else {
					kv.data[op.Key] = op.Value
				}

            default:

        }
		kv.opIndex[op.ClientID] = op.ClientOpIndex

    }

	kv.isTakeSnapshot(applyMsg.CommandIndex, applyMsg.CommandTerm)


    // applyMsg's Index
    for _, pendingOp := range kv.pendingOps[applyMsg.CommandIndex]  {
        if pendingOp.op.ClientID == op.ClientID && pendingOp.op.ClientOpIndex == op.ClientOpIndex {
            //ONLY one that has been pended can be log
            pendingOp.isApplied <- true
         } else {
            pendingOp.isApplied <- false
         }
    }

}
func (kv *KVServer)isTakeSnapshot(index int, term int) {

	if kv.maxraftstate == -1 {
		return
	}
	portion := 2 / 3
	if kv.persister.RaftStateSize() > kv.maxraftstate * portion {
		DPrintf("kvserver[%d], Start Take Snapshot, size: %d, LastIndex:%d, LastTerm:%d",
			kv.me, kv.persister.RaftStateSize(), index, term)
		rawSnapshot := kv.encodeSnapshot()
		kv.rf.TakeSnapshot(index, term, rawSnapshot)
		DPrintf("kvserver[%d], Done Take Snapshot, log size: %d, LastIndex:%d, LastTerm:%d",
			kv.me, kv.persister.RaftStateSize(), kv.rf.LastIncludedIndex, kv.rf.LastIncludedTerm)
	}
}
//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(Op{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	// You may need initialization code here.
	kv.persister = persister
	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)
    kv.data = make(map[string]string)
    kv.pendingOps = make(map[int][]*PendingOps)
    kv.opIndex = make(map[int64]int64)
	kv.readSnapshot(kv.persister.ReadSnapshot())

	DPrintf("[Snapshot] server %d, lastIncludedIndex: %d, lastIncludedTerm: %d \n",me,  kv.rf.LastIncludedIndex, kv.rf.LastIncludedTerm)
	// You may need initialization code here.
    go func() {
        for {
            // wait Raft to apply
			applyMsg := <- kv.applyCh
            kv.Apply(&applyMsg)
        }
    }()
	return kv
}


/*
 * data 参数从 persister 中读取后传入
*/
func (kv*KVServer) readSnapshot(data []byte) {

	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// DPrintf("READ snapshot: %v\n",data)
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)
	var lastIndex int
	var lastTerm int
	//var kvdata map[string]string
	// 写在raft中，这个
	//
	 if d.Decode(&lastIndex) != nil {
		 log.Fatal("[readSnapshot]: lastIndex decode error\n")
	 }
	 if	d.Decode(&lastTerm) != nil {
		log.Fatal("[readSnapshot]:lastTerm decode error\n")
	}
	if d.Decode(&kv.data) != nil {
		log.Fatal("[readSnapshot]: kvdata decode error\n")
	}

	kv.rf.LastIncludedIndex = lastIndex
	kv.rf.LastIncludedTerm  = lastTerm
	kv.rf.CommitIndex = lastIndex
	kv.rf.LastApplied = lastIndex
	//kv.data = kvdata
	DPrintf("[Snapshot] kvserver: %d lastIncludedIndex: %d, lastIncludedTerm: %d \n",kv.me, kv.rf.LastIncludedIndex, kv.rf.LastIncludedTerm)
}

func (kv *KVServer) encodeSnapshot() []byte {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)
	e.Encode(kv.data)
	return w.Bytes()
}
