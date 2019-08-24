package raftkv

import (
	"labgob"
	"labrpc"
	"log"
	"raft"
	"sync"
    "time"
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
}
/*
 *  操作是否成功执行, IsLeader
*/
func (kv *KVServer) execOp(op Op) (bool, bool){
    opIndex, _, isLeader := kv.rf.Start(op)

    if !isLeader {
        // DPrintf("The Server:%d is not Leader\n", kv.me)
		// 操作失败， 且不是leader
        return false, false
    }
    // after Start, kvservers will need to wait for Raft to complete agreement
    wait := make(chan bool, 0)
    //DPrintf("Leader %v Append to pendingOps index[%v] op:[%v]",kv.me, opIndex, op)
    kv.mu.Lock()
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
        DPrintf("Apply op to SM is timeout.\n")
        ok = false
    }
	kv.mu.Lock()
	// 丢弃所有， 对未成功的op， 客户端会再次尝试
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
    kv.mu.Lock()
    defer kv.mu.Unlock()
    var op Op
    // Type assertion
    op = applyMsg.Command.(Op)
    if kv.opIndex[op.ClientID] >= op.ClientOpIndex {
        // current Op requested by client is duplicate
        DPrintf("Duplicate operation\n")
    } else {

        switch op.Type {
            case OP_PUT:
				// DPrintf("Server[%v],Put Key/Value %v/%v\n", kv.me, op.Key, op.Value)
                kv.data[op.Key] = op.Value
            case OP_APPEND:
				// DPrintf("Append Key/Value %v/%v\n", op.Key, op.Value)
				val := kv.data[op.Key]
                kv.data[op.Key] = val + op.Value
            default:

        }
		kv.opIndex[op.ClientID] = op.ClientOpIndex
    }
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
