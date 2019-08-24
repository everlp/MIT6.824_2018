package raftkv

const (
	OK       = "OK"
	ERR_NO_KEY = "ErrNoKey"
    ERR_NOT_LEADER = "ErrNotLeader"
	TIME_OUT_OR_OTHER = "ErrOther"
)

type Err string

// Put or Append
type PutAppendArgs struct {
	Key   string
	Value string
	Op    string // "Put" or "Append"
	// You'll have to add definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
    ClientID       int64
    ClientOpIndex  int64    // record operation's number
}

type PutAppendReply struct {
	WrongLeader bool
	Err         Err

}

type GetArgs struct {
	Key string
	// You'll have to add definitions here.
    ClientID int64
    ClientOpIndex int64   // record Operation's number(order)
}

type GetReply struct {
	WrongLeader bool
	Err         Err
	Value       string
}
