package actor

import (
	"math/rand"
	"sync"
	"time"

	"robot/internal/foundation/lockhub"
)

type Actor struct {
	slotID     int
	uid        int
	generation uint64
	mode       Mode
	state      State
	runtime    RobotRuntime
	rand       *rand.Rand
	stateMu    lockhub.Locker
	cmds       chan request
	ctrls      chan control
	stop       chan struct{}
	done       chan struct{}
	once       sync.Once

	nextMove          time.Time
	nextShout         time.Time
	nextStore         time.Time
	nextAuction       time.Time
	nextMail          time.Time
	nextEmote         time.Time
	nextServerNotice  time.Time
	storeUntil        time.Time
	lastOnlineTry     time.Time
	nextRetryAt       time.Time
	firstFailureAt    time.Time
	failures          int
	failureClass      string
	onlineEstablished bool
	busy              bool
	busyKind          string
	releaseRequested  bool
	onlineDesired     bool
	quarantined       bool
}

func NewActor(slotID int, mode Mode, runtime RobotRuntime) *Actor {
	return &Actor{
		slotID:  slotID,
		mode:    mode,
		state:   StateIdle,
		runtime: runtime,
		rand:    rand.New(rand.NewSource(time.Now().UnixNano() + int64(slotID)*7919)),
		cmds:    make(chan request, 16),
		ctrls:   make(chan control, 4),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}
