package kimi

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/MoonshotAI/kimi-agent-sdk/go/wire"
	"github.com/MoonshotAI/kimi-agent-sdk/go/wire/transport"
)

var (
	ErrTurnNotFound = errors.New("turn not found")
)

func turnBegin(
	ctx context.Context,
	id uint64,
	tp transport.Transport,
	errorPointer *atomic.Pointer[error],
	resultPointer *atomic.Pointer[wire.PromptResult],
	wireProtocolVersion string,
	wireMessageChan <-chan wire.Message,
	wireRequestResponseChan chan<- wire.RequestResponse,
	exit func(error) error,
	session *Session,
) *Turn {
	parent, cancel := context.WithCancel(ctx)
	current, stop := context.WithCancel(context.Background())
	resultPointer.CompareAndSwap(nil, &wire.PromptResult{Status: wire.PromptResultStatusPending})
	steps := make(chan *Step)
	turn := &Turn{
		id:                      id,
		tp:                      tp,
		errorPointer:            errorPointer,
		resultPointer:           resultPointer,
		current:                 current,
		stop:                    stop,
		cancel:                  cancel,
		exit:                    exit,
		wireProtocolVersion:     wireProtocolVersion,
		wireRequestResponseChan: wireRequestResponseChan,
		session:                 session,
		Steps:                   steps,
	}
	turn.usage.Store(&Usage{})
	go turn.traverse(wireMessageChan, steps)
	go turn.watch(parent)
	return turn
}

type Turn struct {
	id            uint64
	tp            transport.Transport
	errorPointer  *atomic.Pointer[error]
	resultPointer *atomic.Pointer[wire.PromptResult]

	current context.Context
	stop    context.CancelFunc
	cancel  context.CancelFunc
	exit    func(error) error

	Steps <-chan *Step
	usage atomic.Pointer[Usage]

	wireProtocolVersion     string
	wireRequestResponseChan chan<- wire.RequestResponse
	session                 *Session
}

// Steer injects user input mid-turn (Wire 1.5+). The cli echoes the input
// back as a SteerInput event so observers see it in the message stream.
func (t *Turn) Steer(content wire.Content) error {
	_, err := t.tp.Steer(&wire.SteerParams{UserInput: content})
	return err
}

func (t *Turn) watch(parent context.Context) {
	defer t.stop()
	select {
	case <-t.current.Done():
		return
	case <-parent.Done():
	}
	t.tp.Cancel(&wire.CancelParams{})
}

func (t *Turn) traverse(incoming <-chan wire.Message, steps chan<- *Step) {
	defer close(steps)
	defer close(t.wireRequestResponseChan)
	defer t.Cancel()
	var (
		outgoing chan wire.Message
		turnEnd  bool
	)
	defer func() {
		if outgoing != nil {
			close(outgoing)
		}
		if wireAtLeast(t.wireProtocolVersion, "1.2") && !turnEnd {
			t.resultPointer.Store(&wire.PromptResult{Status: wire.PromptResultStatusUnexpectedEOF})
		}
	}()
	// Wire 1.7+: the cli may emit pre-turn informational events (notably
	// HookTriggered for UserPromptSubmit hooks configured on the cli side)
	// before TurnBegin. Drain those until TurnBegin arrives, otherwise we
	// blow up with ErrTurnNotFound on the first hook-fire notification.
drainPreTurn:
	for {
		select {
		case msg, ok := <-incoming:
			if !ok {
				return
			}
			if _, is := msg.(wire.TurnBegin); is {
				break drainPreTurn
			}
			// Non-TurnBegin event before the turn opens. We have no Step
			// channel yet to forward it on, so silently drop it — matches
			// what the upstream loop would do for unknown messages.
		case <-t.current.Done():
			return
		}
	}
	for msg := range incoming {
		switch x := msg.(type) {
		case wire.TurnEnd:
			turnEnd = true
			return
		case wire.Request:
			if outgoing != nil {
				select {
				case outgoing <- x:
				case <-t.current.Done():
					return
				}
			}
		case wire.Event:
			switch x.EventType() {
			case wire.EventTypeTurnBegin:
				panic("wire.TurnBegin event should not be received")
			case wire.EventTypeStepBegin:
				if outgoing != nil {
					close(outgoing)
				}
				outgoing = make(chan wire.Message)
				select {
				case steps <- &Step{n: x.(wire.StepBegin).N, Messages: outgoing}:
				case <-t.current.Done():
					return
				}
			case wire.EventTypeStatusUpdate:
				update := x.(wire.StatusUpdate)
				if update.PlanMode.Valid && t.session != nil {
					t.session.planMode.Store(update.PlanMode.Value)
				}
			CAS:
				for {
					oldUsage := t.usage.Load()
					newUsage := &Usage{Tokens: oldUsage.Tokens}
					if update.ContextUsage.Valid {
						newUsage.Context = update.ContextUsage.Value
					}
					if update.TokenUsage.Valid {
						tokens := update.TokenUsage.Value
						newUsage.Tokens.InputOther += tokens.InputOther
						newUsage.Tokens.Output += tokens.Output
						newUsage.Tokens.InputCacheRead += tokens.InputCacheRead
						newUsage.Tokens.InputCacheCreation += tokens.InputCacheCreation
					}
					if t.usage.CompareAndSwap(oldUsage, newUsage) {
						break CAS
					}
				}
			default:
				if outgoing != nil {
					select {
					case outgoing <- x:
					case <-t.current.Done():
						return
					}
				}
			}
		default:
			// Defensive: the wire layer already silently skips unknown
			// event types (yielding a nil-payload Event ignored by the
			// Responder), so this branch should be unreachable. If a
			// future change ever surfaces an exotic Message subtype, drop
			// it instead of panicking — losing one message is preferable
			// to tearing down the whole turn.
			_ = x
		}
	}
}

func (t *Turn) ID() uint64 {
	return t.id
}

func (t *Turn) Err() error {
	if err := t.errorPointer.Load(); err != nil && *err != nil {
		return *err
	}
	return nil
}

func (t *Turn) Result() wire.PromptResult {
	return *t.resultPointer.Load()
}

func (t *Turn) Usage() *Usage {
	return t.usage.Load()
}

func (t *Turn) Cancel() error {
	t.cancel()
	<-t.current.Done()
	return t.exit(nil)
}

type Step struct {
	n        int
	Messages <-chan wire.Message
}

type Usage struct {
	Context float64
	Tokens  wire.TokenUsage
}
