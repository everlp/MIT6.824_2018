# 1. 简介
在以下的实验过程记录中只给出关键代码，[本实验的详细代码请参考这里](https://github.com/SmallPond/MIT6.824_2018)。

在这个 lab 中，我们要使用 lab2 中实现的 Raft 库来建立一个容错键值存储服务。Key/Value 服务将是一个复制状态机，由几个使用 Raft 维护的复制 Key/Value 服务器组成。 只要大多数服务器处于活动状态并且可以进行通信，Key/Value 服务就应该继续处理客户端请求，尽管存在其他故障或网络分区。

服务支持以下三种操作：
- `Put(key, value)`, replaces the value for a particular key。
- `Append(key, arg)`, appends arg to key's value。向一个不存在的 key append arg 应该像 `Put`操作。
- `Get(key)`，获取当前键对应的值。

客户端通过`Clerk`使用Put / Append / Get方法与服务进行通信。 `Clerk`管理与服务器的RPC交互。一致性要求：
> Here's what we mean by strong consistency. If called one at a time, the Get/Put/Append methods should act as if the system had only one copy of its state, and each call should observe the modifications to the state implied by the preceding sequence of calls. For concurrent calls, the return values and final state must be the same as if the operations had executed one at a time in some order. Calls are concurrent if they overlap in time, for example if client X calls Clerk.Put(), then client Y calls Clerk.Append(), and then client X's call returns. Furthermore, a call must observe the effects of all calls that have completed before the call starts (so we are technically asking for linearizability).

这个 Lab 有两个部分。在PartA 中，我们要实现此服务，不用担心 Raft 的日志无限增长。在B部分，我们要实现`snapshots(Section 7 in the paper)`, 这能使得 Raft 对旧的日志项进行垃圾回收。

# 2. PartA: Key/value service without log compaction
每个键/值服务器都会关联一个 Raft peer。Clerks 发送`Put`,`Append`,`Get`RPCS 到关联着Raft leader的 kvserver。kvserver 提交Put,Get等操作到Raft中，Raft 的日志记录操作序列。所有的kvserver 按序从Raft log中去除操作执行，应用这些操作到他们的K/V数据库中，目的是让服务器维护K/V数据库的相同副本。

重点来了！基于 Raft 的应用。在Raft上构建服务时，服务和Raft日志之间的交互可能很难实现。你可能会对如何根据一个可复制的日志来实现你的应用感到困惑。你可能会让你的服务一收到客户端请求就向 leader 发送请求，然后等待 Raft 应用请求并做出客户端要求的操作，最后将结果发回到客户端。虽然这对单个客户端系统来说似乎是一个好方式，但其无法实现并发式客户端。

相反，服务应该以状态机的形式实现，客户端操作将 machine 从一种 state 转换为另一种 state。你应该在某个地方实现一个循环，每次 take 一个客户端操作（所有服务器都以同一种顺序），并且按序将这些操作应用到每个状态机。这个循环应该是你代码中唯一使用应用程序状态的部分。这就意味着面向客户端的RPC 方法应该只是向Raft提交客户端操作，然后等待这个操作被`applier loop`应用。只有在客户端命令出现时才应执行，并读取返回值。 请注意，这包括读取请求！

这可能又带来了另一个问题：你怎么知道操作何时完成？在没有 failures 的情况下很简单：you just wait for the thing you put into the log to come back out(i.e., be passed to `apply()`)。但是如果发生了错误呢？ For example, you may have been the leader when the client initially contacted you, but someone else has since been elected, and the client request you put in the log has been discarded. Clearly you need to have the client try again, but how do you know when to tell them about the error?

使用一个简单的方式就可以解决这个问题：记录Raft在哪个位置插入了客户端操作。一旦在那个索引下的操作被应用，你可以根据该索引的操作是否实际上是你放置的操作来判断客户端操作是否成功。 如果不是，则发生故障并且可以将错误返回给客户端。

## 2.1. Task
首先我们简单分析以下这个部分程序的实现流程。 Clerk 作为各个客户端的接口（可以形象理解为银行中的工作人员，代替客户进行请求），向Server发送请求。最开始Clerk 并不知道谁是Leader，只能尝试发送知道得到Leader的回应。Leader接收到Clerk通过RPC发送到自身的请求后，加入到log中，并向其他Server结点发送AppendEntries的请求。 在`StartServer`中，我们会开启一个loop，通过 applyChan检查 Raft 是否已经将log中的项应用到了 machine 上，若是则执行相应的操作。

### 2.1.1. 客户端实现
首先要填充`Clerk`结构体中的内容，记录客户端的ID，LeaderID，以及当前客户端请求的操作序号（用来确保服务器端接收操作的有效性等）。
```
type Clerk struct {
	servers []*labrpc.ClientEnd
	// You will have to modify this struct.
    leaderID int
    clientID int64
    opCount  int64       // record client operation's order(number)
}
```

>You'll need to add RPC-sending code to the Clerk Put/Append/Get methods in client.go。implement `PutAppend()` and `Get()` RPC handlers in server.go

首先我们需要实现客户端的`Put`、`Append`、`Get`等方法，函数注释中写到，`keeps trying forever in the face of all other errors.`，所以我们需要使用一个死循环来保证请求的通过。以`Get`为例，如果Reply不是成功，就一直保持对服务器的请求。
1. `func (ck *Clerk) Get(key string) string`
```
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
```
2. `func (ck *Clerk) PutAppend(key string, value string, op string)`

```
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
```

### 2.1.2. 服务端实现
1. 服务器端比较实现重要的一点就是我们需要将操作提交给Raft，等待Raft将此操作应用到State Machine后，我们才真正对K/V服务的内容进行相应的操作。所以我们首先需要开启一个goroutine去检查是否有操作被Raft应用。因此，在`StartServer`中我们需要写以下语句。applyCh我们会通过参数将其传递到Raft中，在Lab2中实现的Raft如果应用了command，就会向applyCh中写入操作信息。
```
    go func() {
        for {
            // wait Raft to apply
            applyMsg := <- kv.applyCh
            kv.Apply(&applyMsg)
        }
    }()
	  return kv

// Apply函数关键代码
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
```

2. 服务器每次接收到`Clerk`发送过来的请求，都需要调用Raft的Start函数，将其加入到Raft的Log中，然后等待这个操作被应用。操作真正被执行的同步过程我们使用一个与操作绑定的`isApply`channel来实现，其会在Apply函数中被修改。等待的过程我们使用一个timer进行超时检测，超时则向客户端返回超时错误，客户端即可再次发送操作请求。具体实现如下。

```
func (kv *KVServer) execOp(op Op) (bool, bool){
    opIndex, _, isLeader := kv.rf.Start(op)
    if !isLeader {
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
```

3. 其中还涉及到各类结构体以及常量的定义（集中在`common.go`以及`server.go`文件中），我都在代码中给出了详细的解释，大家可以稍微参考以下。

## 2.2. 错误
在实现过程中，我们可能会遇到各种不同的错误。当不知道代码在哪里出错的时候，就需要去阅读一下Test代码逻辑。以下是我遇到的错误以及相应的解决办法。
### 2.2.1. 测试程序卡住
`GenericTest`在for循环中只进行了一次迭代，因为 clients 没有退出。~~但是不明白程序为什么会卡住~~。手动 Printf 发现卡在了`Get`语句处（Get函数中调用了ck中的Get函数，但没能返回），若注释掉else 处的语句，程序是可以正常迭代的。**最后发现是Server端没有正确对 Clerk进行回应。以致于clinet 一直在向 Server 发送 Get 请求**
```
    for atomic.LoadInt32(&done_clients) == 0 {
				if (rand.Int() % 1000) < 500 {
					nv := "x " + strconv.Itoa(cli) + " " + strconv.Itoa(j) + " y"
					log.Printf("%d: client new append %v\n", cli, nv)
					Append(cfg, myck, key, nv)
					last = NextValue(last, nv)
					j++
				} else {
					log.Printf("%d: client new get %v\n", cli, key)
					v := Get(cfg, myck, key)
					if v != last {
						log.Fatalf("get wrong value, key %v, wanted:\n%v\n, got\n%v\n", key, last, v)
					}
				}
			}
```

### 2.2.2. 重复Append 其中一个 value
只会重复一个，大部分结果是重复了最后一个。如下所示。
```
2019/08/23 11:46:12 get wrong value, key 0, wanted:
x 0 0 yx 0 1 yx 0 2 yx 0 3 yx 0 4 yx 0 5 yx 0 6 yx 0 7 yx 0 8 yx 0 9 yx 0 10 yx 0 11 yx 0 12 yx 0 13 yx 0 14 yx 0 15 yx 0 16 yx 0 17 yx 0 18 yx 0 19 yx 0 20 yx 0 21 yx 0 22 yx 0 23 yx 0 24 yx 0 25 yx 0 26 yx 0 27 yx 0 28 yx 0 29 yx 0 30 yx 0 31 yx 0 32 yx 0 33 yx 0 34 yx 0 35 yx 0 36 yx 0 37 yx 0 38 yx 0 39 yx 0 40 yx 0 41 yx 0 42 yx 0 43 y
, got
x 0 0 yx 0 1 yx 0 2 yx 0 3 yx 0 4 yx 0 5 yx 0 6 yx 0 7 yx 0 8 yx 0 9 yx 0 10 yx 0 11 yx 0 12 yx 0 13 yx 0 14 yx 0 15 yx 0 16 yx 0 17 yx 0 18 yx 0 19 yx 0 20 yx 0 21 yx 0 22 yx 0 23 yx 0 24 yx 0 25 yx 0 26 yx 0 27 yx 0 28 yx 0 29 yx 0 30 yx 0 31 yx 0 32 yx 0 33 yx 0 34 yx 0 35 yx 0 36 yx 0 37 yx 0 38 yx 0 39 yx 0 40 yx 0 41 yx 0 42 yx 0 43 yx 0 43 y
```
**解决办法**：忘记在`apply`中更改Server记录各个客户端已经append 操作的数量，导致可能重复加入。修改后就可以通过第一个Test，但调试过程中又出现了一个与我理解中不一致的现象，在`apply`函数中的 append竟然会对同一个K/V append输出多次。喔，我傻了。这是因为有多个Server都会进行一次append.
```
2019/08/23 14:53:46 Append Key/Value 0/x 0 58 y
2019/08/23 14:53:46 Append Key/Value 0/x 0 58 y
2019/08/23 14:53:46 Append Key/Value 0/x 0 58 y
2019/08/23 14:53:46 Append Key/Value 0/x 0 58 y
2019/08/23 14:53:46 PUT APPEND K/V:[0,x 0 58 y]success

```

### 2.2.3. Test卡在 Test: progress in majority (3A)
手动Printf发现卡在了`func TestOnePartition3A(t *testing.T)`下的`Put`调用。

解决方案：是我Clerk中的`Put`函数的逻辑问题。我认为如果不成功，要么是此Server不是Leader，要么是发生了超时。而超时我是不对leader进行改变的，代码如下所示。但是在发生了分区之后情况就不一样了，分区后Leader发生了变化。但是为什么死循环呢？我们需要分析一下测试代码。
```
        ck.servers[ck.leaderID].Call("KVServer.PutAppend", &args, &reply)
       if reply.Err == OK  {
            DPrintf("PUT APPEND K/V:[%s,%s]success",key, value)
            break
       } else {
        		if reply.WrongLeader == true {
        			ck.leaderID = (ck.leaderID + 1) % len(ck.servers)
        		}        
       }
```

分区测试关键代码如下，`make_partition`函数`Partition servers into 2 groups and put current leader in minority`，会将LeaderID记录下来并且分配到p2中。
```
  ...
	Put(cfg, ck, "1", "13")
	cfg.begin("Test: progress in majority (3A)")
	p1, p2 := cfg.make_partition()
	cfg.partition(p1, p2)

	ckp1 := cfg.makeClient(p1)  // connect ckp1 to p1
	ckp2a := cfg.makeClient(p2) // connect ckp2a to p2
	ckp2b := cfg.makeClient(p2) // connect ckp2b to p2

	Put(cfg, ckp1, "1", "14")
	DPrintf("put done \n")
	check(cfg, t, ckp1, "1", "14")
```

我们看一下实际代码跑出来的结果。上面测试代码的第一行Put会不断寻找leader，最终找到leaderID 为2，进行`make_partition`后，`p1=[0, 1, 3]`， `p2=[2, 4]`,并且`partition`函数会相应断开Client与Server的连接。然后执行到`Put(cfg, ckp1, "1", "14")`时，若不修改LeaderID,其RPC请求会一直失败。就出现了死循环。
```
...
2019/08/23 16:54:13 WrongLeader...0
2019/08/23 16:54:13 WrongLeader...1
2019/08/23 16:54:13 WrongLeader...2
2019/08/23 16:54:13 WrongLeader...3
2019/08/23 16:54:13 WrongLeader...4
2019/08/23 16:54:13 Server[2],Put Key/Value 1/13
2019/08/23 16:54:13 PUT APPEND K/V:[1,13]success
```

所以在此，我们需要再添加一些一个修改leaderID的分支。
```
      if reply.WrongLeader == true {
				// DPrintf("WrongLeader...%v\n", ck.leaderID)
				ck.leaderID = (ck.leaderID + 1) % len(ck.servers)
			} else if reply.Err == TIME_OUT_OR_OTHER {
				// 实际上如果是超时的话， 不需要更改 leaderID，
			//
			} else {
				// RPC请求不成功会进入到这个分支
				// 这样写可能有点重复了，这个if else 完全可以进行简化。但更好理解一点。
				ck.leaderID = (ck.leaderID + 1) % len(ck.servers)
			}
```


### 2.2.4. fatal error: concurrent map writes
这种错误不会每次都出现，很难排查到底是哪两部分对map同时进行了写操作。直接加锁就好了。
```
src/kvraft/server.go:96

96:  delete(kv.pendingOps, opIndex)

	kv.mu.Lock()
	// 丢弃所有， 对未成功的op， 客户端会再次尝试
    delete(kv.pendingOps, opIndex)
	kv.mu.Unlock()
```

## 2.3. 结果
```
Test: one client (3A) ...
  ... Passed --  18.7  5 10029  650
Test: many clients (3A) ...
  ... Passed --  17.2  5 14992 1197
Test: unreliable net, many clients (3A) ...
  ... Passed --  16.3  5 11326 1130
Test: concurrent append to same key, unreliable (3A) ...
  ... Passed --   1.2  3   345   52
Test: progress in majority (3A) ...
  ... Passed --   0.7  5    95    2
Test: no progress in minority (3A) ...
  ... Passed --   1.0  5    96    3
Test: completion after heal (3A) ...
  ... Passed --   1.0  5    66    3
Test: partitions, one client (3A) ...
  ... Passed --  23.2  5  9137  582
Test: partitions, many clients (3A) ...
  ... Passed --  24.4  5 13922  989
Test: restarts, one client (3A) ...
  ... Passed --  20.1  5 21233  807
Test: restarts, many clients (3A) ...
  ... Passed --  20.4  5 35298 1303
Test: unreliable net, restarts, many clients (3A) ...
  ... Passed --  20.9  5 14339 1127
Test: restarts, partitions, many clients (3A) ...
  ... Passed --  27.7  5 25870  974
Test: unreliable net, restarts, partitions, many clients (3A) ...
  ... Passed --  29.0  5 12281  917
Test: unreliable net, restarts, partitions, many clients, linearizability checks (3A) ...
Iteration 0
  ... Passed --  25.3  7 19155  764
PASS
ok  	kvraft	247.788s
```









