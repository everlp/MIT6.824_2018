package raftkv

import "labrpc"
import "crypto/rand"
import "math/big"
import "sync/atomic"

type Clerk struct {
	servers []*labrpc.ClientEnd
	// You will have to modify this struct.
    leaderID int
    clientID int64
    opCount  int64       // record client operation's order(number)
}
func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	// You'll have to add code here.
	ck.leaderID = 0    // clerk don't konw who is leader
    ck.clientID = nrand()
    ck.opCount = 0
    return ck
}

//
// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.Get", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
func (ck *Clerk) Get(key string) string {
	// DPrintf("[Client] Start a Get\n")
	// You will have to modify this function.
	var args GetArgs
    args.ClientID = ck.clientID
    args.Key = key
    // keeps trying forever, so we can't use i < len() sentence
    args.ClientOpIndex = atomic.AddInt64(&ck.opCount, 1)
    for {
        var reply GetReply
        // fmt.Printf("client [%d] Send [Get request] to server %d\n", args.ClientID, ck.leaderID)
		ck.servers[ck.leaderID].Call("KVServer.Get", &args, &reply)
		DPrintf("[Get] reply :%v ", reply.Err)
        if reply.Err == OK {
            // fmt.Printf("client [Get] K/V:%v/%v\n", key, reply.Value)
            return reply.Value
        } else {
			if reply.WrongLeader == true {
				ck.leaderID = (ck.leaderID + 1) % len(ck.servers)
			} else if reply.Err == TIME_OUT_OR_OTHER {
				// 实际上如果是超时的话， 不需要更改 leaderID，
			} else {
				// RPC请求不成功会进入到这个分支
				// 这样写可能有点重复了，这个if else 完全可以进行简化。但更好理解一点。
				ck.leaderID = (ck.leaderID + 1) % len(ck.servers)
			}
        }
	}
}

//
// shared by Put and Append.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.PutAppend", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
//
func (ck *Clerk) PutAppend(key string, value string, op string) {
	// You will have to modify this function.
    args := PutAppendArgs{Key:key, Value:value, Op:op, ClientID:ck.clientID}
    args.ClientOpIndex = atomic.AddInt64(&ck.opCount, 1)
    for {
        var reply PutAppendReply

        ck.servers[ck.leaderID].Call("KVServer.PutAppend", &args, &reply)
        if reply.Err == OK  {
            // DPrintf("PUT APPEND K/V:[%s,%s]success",key, value)
            break
        } else {
			if reply.WrongLeader == true {
				// DPrintf("WrongLeader...%v\n", ck.leaderID)
				ck.leaderID = (ck.leaderID + 1) % len(ck.servers)
			} else if reply.Err == TIME_OUT_OR_OTHER {
				// 实际上如果是超时的话， 不需要更改 leaderID，
			} else {
				// RPC请求不成功会进入到这个分支
				// 这样写可能有点重复了，这个if else 完全可以进行简化。但更好理解一点。
				ck.leaderID = (ck.leaderID + 1) % len(ck.servers)
			}
        }
    }
}

func (ck *Clerk) Put(key string, value string) {
	ck.PutAppend(key, value, "Put")
}
func (ck *Clerk) Append(key string, value string) {
	ck.PutAppend(key, value, "Append")
}
