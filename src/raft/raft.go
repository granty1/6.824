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
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)
import "labrpc"

// import "bytes"
// import "labgob"

const (
	Follower = iota
	Candidater
	Leader

	AppendEntriesInterval = 150 * time.Millisecond
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
	currentTerm int32
	votedFor    int
	votedCount  int
	log         []LogEntry
	state       int32

	timer *time.Timer

	voteChannel chan struct{}

	commitIndex int
	lastApplied int

	// leader
	// 每次选举后重新初始化
	nextIndex  []int
	matchIndex []int

	applyCh chan ApplyMsg
}

type LogEntry struct {
	Term    int
	Command interface{}
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	// Your code here (2A).
	return int(rf.currentTerm), rf.is(Leader)
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

//func (rf *Raft) preLog() (int, int) {
//	if len(rf.log) < 2 {
//		return 0, 0
//	}
//	preLog := rf.log[len(rf.log)-2]
//	return preLog.Index, preLog.Term
//}
//
//func (rf *Raft) logEntries(nextIndex int) []LogEntry {
//	var logs []LogEntry
//	for _, v := range rf.log {
//		if v.Index >= nextIndex {
//			logs = append(logs, v)
//		}
//	}
//	return logs
//}

func (rf *Raft) term() int32 {
	return atomic.LoadInt32(&rf.currentTerm)
}

func (rf *Raft) states() int32 {
	return atomic.LoadInt32(&rf.state)
}

func (rf *Raft) incrementTerm() int32 {
	return atomic.AddInt32(&rf.currentTerm, 1)
}

func (rf *Raft) is(state int32) bool {
	return rf.states() == state
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
	LastLogTerm  int
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term        int32
	VoteGranted bool
}

type AppendEntriesArgs struct {
	Term         int32
	Leader       int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term    int32
	Success bool
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if args.Term < rf.currentTerm ||
		(args.Term == rf.currentTerm && rf.votedFor != -1 && rf.votedFor != args.CandidateId) {
		reply.Term = rf.currentTerm
		reply.VoteGranted = false
		return
	}

	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.change(Follower)
	}

	lastLogIndex := len(rf.log) - 1
	if args.LastLogTerm < rf.log[lastLogIndex].Term ||
		(args.LastLogTerm == rf.log[lastLogIndex].Term && args.LastLogIndex < lastLogIndex) {
		reply.Term = rf.currentTerm
		reply.VoteGranted = false
		return
	}

	rf.votedFor = args.CandidateId
	reply.Term = rf.currentTerm
	reply.VoteGranted = true
	rf.timer.Reset(randDuration())

	if reply.VoteGranted {
		go func() {
			rf.voteChannel <- struct{}{}
		}()
	}
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if args.Term < rf.term() {
		reply.Success = false
		reply.Term = args.Term
		return
	}
	if args.Term > rf.term() {
		rf.currentTerm = args.Term
		rf.change(Follower)
	}
	if rf.is(Candidater) {
		rf.change(Follower)
	}
	rf.timer.Reset(randDuration())

	if len(rf.log) < args.PrevLogIndex+1 || rf.log[args.PrevLogIndex].Term != args.PrevLogTerm {
		reply.Success = false
		reply.Term = rf.term()
		return
	}

	unmatch_id := -1
	for id := range args.Entries {
		if len(rf.log) < args.PrevLogIndex+id+2 || rf.log[args.PrevLogIndex+1+id].Term != args.Entries[id].Term {
			unmatch_id = id
			break
		}
	}

	if unmatch_id != -1 {
		// preLogIndex and preLogTerm 匹配的上
		rf.log = rf.log[:args.PrevLogIndex+1+unmatch_id]
		rf.log = append(rf.log, args.Entries[unmatch_id:]...)
		fmt.Printf("[%d] append log: %+v\n", rf.me, args.Entries[unmatch_id:])
	}

	if args.LeaderCommit > rf.commitIndex {
		lastLogIndex := len(rf.log) - 1
		if args.LeaderCommit <= lastLogIndex {
			rf.commitIndex = args.LeaderCommit
		} else {
			rf.commitIndex = lastLogIndex
		}
		rf.apply()
		fmt.Printf("[%d] commitIndex:%d\n", rf.me, rf.commitIndex)
	}
	reply.Success = true
}

func (rf *Raft) apply() {
	if rf.commitIndex > rf.lastApplied {
		go func(start_id int, entries []LogEntry) {
			for i, v := range entries {
				msg := ApplyMsg{
					CommandValid: true,
					Command:      v.Command,
					CommandIndex: start_id + i,
				}

				rf.applyCh <- msg

				rf.mu.Lock()
				rf.lastApplied = msg.CommandIndex
				rf.mu.Unlock()
			}
		}(rf.lastApplied+1, rf.log[rf.lastApplied+1:rf.commitIndex+1])
	}
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

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
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
	isLeader := false

	// Your code here (2B).
	if term, isLeader = rf.GetState(); isLeader {
		rf.mu.Lock()
		defer rf.mu.Unlock()
		index = len(rf.log)
		rf.matchIndex[rf.me] = index
		rf.nextIndex[rf.me] = index + 1
		rf.log = append(rf.log, LogEntry{
			Term:    term,
			Command: command,
		})
		rf.broadcastAppendEntries()
	}

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

func (rf *Raft) broadcastVoteRequest() {
	lastLogIndex := len(rf.log) - 1
	args := RequestVoteArgs{
		Term:         rf.term(),
		CandidateId:  rf.me,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  rf.log[lastLogIndex].Term,
	}
	for i := range rf.peers {
		if i == rf.me {
			continue
		}
		go func(server int) {
			var reply RequestVoteReply
			if rf.is(Candidater) && rf.sendRequestVote(server, &args, &reply) {
				rf.mu.Lock()
				rf.mu.Unlock()
				if reply.VoteGranted && rf.is(Candidater) {
					rf.votedCount++
				} else {
					if reply.Term > rf.currentTerm {
						rf.currentTerm = reply.Term
						rf.change(Follower)
					}
				}
			} else {
				//TODO retry ?
			}
		}(i)
	}
}

func (rf *Raft) broadcastAppendEntries() {
	for i := range rf.peers {
		if i == rf.me {
			continue
		}
		go func(server int) {
			rf.mu.Lock()
			preLogIndex := rf.nextIndex[server] - 1
			entries := make([]LogEntry, len(rf.log[preLogIndex+1:]))
			copy(entries, rf.log[preLogIndex+1:])

			args := AppendEntriesArgs{
				Term:         rf.currentTerm,
				Leader:       rf.me,
				PrevLogIndex: preLogIndex,
				PrevLogTerm:  rf.log[preLogIndex].Term,
				Entries:      entries,
				LeaderCommit: rf.commitIndex,
			}
			rf.mu.Unlock()
			var reply AppendEntriesReply
			if rf.is(Leader) && rf.sendAppendEntries(server, &args, &reply) {
				rf.mu.Lock()
				defer rf.mu.Unlock()
				if reply.Success {
					rf.matchIndex[server] = preLogIndex + len(entries)
					rf.nextIndex[server] = rf.matchIndex[server] + 1

					for i := len(rf.log) - 1; i > rf.commitIndex; i-- {
						count := 0
						for _, matchIndex := range rf.matchIndex {
							if matchIndex >= i {
								count++
							}
						}
						if count > len(rf.peers)/2 {
							rf.commitIndex = i
							rf.apply()
							break
						}
					}
				} else {
					if reply.Term > rf.currentTerm {
						rf.currentTerm = reply.Term
						rf.change(Follower)
					} else {
						if rf.nextIndex[server] > 1 {
							rf.nextIndex[server] -= 1
						}
					}
				}
			}
		}(i)
	}
}

func (rf *Raft) election() {
	rf.timer.Reset(randDuration())
	rf.incrementTerm()
	rf.votedFor = rf.me

	rf.votedCount = 1
	rf.broadcastVoteRequest()
}

func (rf *Raft) start() {
	rf.timer = time.NewTimer(randDuration())
	for {
		switch atomic.LoadInt32(&rf.state) {
		case Follower:
			select {
			case <-rf.voteChannel:
				rf.timer.Reset(randDuration())
			case <-rf.timer.C:
				rf.mu.Lock()
				rf.change(Candidater)
				rf.mu.Unlock()
			}
		case Candidater:
			rf.mu.Lock()
			select {
			case <-rf.timer.C:
				rf.timer.Reset(randDuration())
				rf.election()
			default:
				if rf.votedCount >= (len(rf.peers)+1)/2 {
					rf.change(Leader)
				}
			}
			rf.mu.Unlock()
		case Leader:
			rf.broadcastAppendEntries()
			time.Sleep(AppendEntriesInterval)
		}
	}
}

func (rf *Raft) change(state int32) {
	if rf.is(state) {
		return
	}
	switch state {
	case Follower:
		rf.state = state
		rf.timer.Reset(randDuration())
		rf.votedFor = -1
	case Candidater:
		rf.state = state
		rf.election()
	case Leader:
		rf.state = state
		// init nextIndex[]
		for i := range rf.nextIndex {
			rf.nextIndex[i] = len(rf.log)
		}
		for i := range rf.matchIndex {
			rf.matchIndex[i] = 0
		}
		fmt.Printf("[%d] change to [Leader]\n", rf.me)
		rf.broadcastAppendEntries()
	}
}

func randDuration() time.Duration {
	return time.Duration(rand.Int()%50+50) * 10 * time.Millisecond
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

	rf.applyCh = applyCh
	rf.state = Follower
	rf.votedFor = -1
	rf.voteChannel = make(chan struct{})
	rf.log = make([]LogEntry, 1)
	rf.nextIndex = make([]int, len(rf.peers))
	for i := range rf.nextIndex {
		// initialized to leader last log index + 1
		rf.nextIndex[i] = len(rf.log)
	}
	rf.matchIndex = make([]int, len(rf.peers))
	// Your initialization code here (2A, 2B, 2C).
	go rf.start()
	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	return rf
}
