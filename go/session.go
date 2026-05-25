package kimi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/rpc"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MoonshotAI/kimi-agent-sdk/go/wire"
	"github.com/MoonshotAI/kimi-agent-sdk/go/wire/jsonrpc2"
	"github.com/MoonshotAI/kimi-agent-sdk/go/wire/transport"
)

var (
	tpname = reflect.TypeOf((*transport.Transport)(nil)).Elem().Name()
)

// camelToSnake converts a Go-style PascalCase/camelCase method name to the
// snake_case names the kimi cli protocol expects (e.g. "SetPlanMode" →
// "set_plan_mode"). Single-word inputs lower-case to themselves
// ("Prompt" → "prompt"), so existing single-word methods stay wire-compatible.
func camelToSnake(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// snakeToPascal reverses camelToSnake for the server method renamer, so a
// snake_case method coming in from the cli (e.g. "event") maps back to the
// Go server method name ("Event").
func snakeToPascal(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	upper := true
	for _, r := range s {
		if r == '_' {
			upper = true
			continue
		}
		if upper && r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		upper = false
		b.WriteRune(r)
	}
	return b.String()
}

func NewSession(options ...Option) (*Session, error) {
	opt := &option{
		exec: "kimi",
		args: []string{"--wire"},
		envs: os.Environ(),
	}
	for _, f := range options {
		if f != nil {
			f(opt)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, opt.exec, opt.args...)
	cmd.Env = append(cmd.Env, opt.envs...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	watch := func() {
		cmd.Wait()
		stdin.Close()
		stdout.Close()
		cancel()
	}
	codec := jsonrpc2.NewCodec(&stdio{stdin, stdout},
		jsonrpc2.ClientMethodRenamer(jsonrpc2.RenamerFunc(func(method string) string {
			return camelToSnake(strings.TrimPrefix(method, tpname+"."))
		})),
		jsonrpc2.ServerMethodRenamer(jsonrpc2.RenamerFunc(func(method string) string {
			return tpname + "." + snakeToPascal(method)
		})),
	)
	tp := transport.NewTransportClient(rpc.NewClientWithCodec(codec))
	session := &Session{
		ctx:   ctx,
		cmd:   cmd,
		codec: codec,
		tp:    tp,
	}
	responder := &Responder{
		rwlock:                  &session.rwlock,
		pending:                 &session.pending,
		wireMessageBridge:       &session.wireMessageBridge,
		wireRequestResponseChan: &session.wireRequestResponseChan,
		hookHandlers:            opt.hookHandlers,
	}
	wireProtocolVersion, err := getWireProtocolVersion(opt.exec)
	if err != nil {
		cancel()
		return nil, err
	}
	if wireAtLeast(wireProtocolVersion, "1.1") {
		var toolDefs []wire.ExternalTool
		for _, tool := range opt.tools {
			toolDefs = append(toolDefs, tool.def)
		}
		capabilities := opt.capabilities
		if !capabilities.Valid {
			capabilities = wire.Optional[wire.ClientCapabilities]{
				Value: wire.ClientCapabilities{
					SupportsQuestion: wire.Optional[bool]{Value: true, Valid: true},
					SupportsPlanMode: wire.Optional[bool]{Value: true, Valid: true},
				},
				Valid: true,
			}
		}
		initResult, err := tp.Initialize(&wire.InitializeParams{
			ProtocolVersion: wire.SDKProtocolVersion,
			Client:          opt.clientInfo,
			ExternalTools:   toolDefs,
			Hooks:           opt.hooks,
			Capabilities:    capabilities,
		})
		if err != nil {
			cancel()
			return nil, err
		}
		if initResult.ExternalTools.Valid && len(initResult.ExternalTools.Value.Rejected) > 0 {
			cancel()
			return nil, fmt.Errorf("%q tool is rejected: %s",
				initResult.ExternalTools.Value.Rejected[0].Name,
				initResult.ExternalTools.Value.Rejected[0].Reason)
		}
		session.SlashCommands = initResult.SlashCommands
		responder.tools = opt.tools
	}
	session.wireProtocolVersion = wireProtocolVersion
	go session.serve(transport.NewTransportServer(responder))
	go watch()
	return session, nil
}

type Session struct {
	ctx                     context.Context
	cmd                     *exec.Cmd
	codec                   *jsonrpc2.Codec
	pending                 atomic.Int64
	rwlock                  sync.RWMutex
	seq                     uint64
	cancellers              []Canceller
	wireProtocolVersion     string
	wireMessageBridge       chan wire.Message
	wireRequestResponseChan chan wire.RequestResponse
	tp                      transport.Transport
	planMode                atomic.Bool

	SlashCommands []wire.SlashCommand
}

// SetPlanMode toggles Plan Mode on the cli (Wire 1.5+). The cli echoes the
// new state in the result and via subsequent StatusUpdate.plan_mode events;
// PlanMode() returns the latest observed value.
func (s *Session) SetPlanMode(enabled bool) (bool, error) {
	res, err := s.tp.SetPlanMode(&wire.SetPlanModeParams{Enabled: enabled})
	if err != nil {
		return s.planMode.Load(), err
	}
	s.planMode.Store(res.PlanMode)
	return res.PlanMode, nil
}

// PlanMode returns the last plan-mode state acknowledged by the cli (either
// via SetPlanMode's result or via a StatusUpdate.plan_mode event).
func (s *Session) PlanMode() bool {
	return s.planMode.Load()
}

func (s *Session) serve(responder *transport.TransportServer) {
	server := rpc.NewServer()
	server.RegisterName(tpname, responder)
	for {
		if err := server.ServeRequest(s.codec); err != nil {
			return
		}
	}
}

func (s *Session) waitForDataExchange() {
	for {
		pending := s.codec.PendingRequests()
		if pending == 0 {
			break
		}
		time.Sleep(time.Duration(pending) * time.Second)
	}
	for {
		pending := s.pending.Load()
		if pending == 0 {
			break
		}
		time.Sleep(time.Duration(pending) * time.Second)
	}
}

func (s *Session) Prompt(ctx context.Context, content wire.Content) (*Turn, error) {
	return roundtrip(ctx, s, &turnConstructor{s, content})
}

func roundtrip[T any, R any, I interface {
	Cargo[R]
	*T
}](ctx context.Context, s *Session, constructor Constructor[T, R]) (*T, error) {
	// Check if context is already cancelled before starting any work
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	var (
		bg                      sync.WaitGroup
		id                      = atomic.AddUint64(&s.seq, 1)
		wireMessageBridge       = make(chan wire.Message)
		wireRequestResponseChan = make(chan wire.RequestResponse)
		rpcErrorChan            = make(chan error)
		cargoAvailableChan      = make(chan struct{})
		errorPointer            = new(atomic.Pointer[error])
		resultPointer           = new(atomic.Pointer[R])
		wireMessageChan         = make(chan wire.Message)
	)
	s.rwlock.Lock()
	s.wireMessageBridge = wireMessageBridge
	s.wireRequestResponseChan = wireRequestResponseChan
	s.rwlock.Unlock()
	var rpcErrorSignal = make(chan struct{})
	bg.Go(func() {
		defer close(cargoAvailableChan)
		defer close(wireMessageChan)
		var once sync.Once
		for msg := range wireMessageBridge {
			once.Do(func() {
				select {
				case cargoAvailableChan <- struct{}{}:
				case <-rpcErrorSignal:
				case <-ctx.Done():
				}
			})
			select {
			case wireMessageChan <- msg:
			case <-rpcErrorSignal:
			case <-ctx.Done():
			}
		}
	})
	var deliveredSignal = make(chan struct{})
	bg.Go(func() {
		cleanup := func() {
			s.waitForDataExchange()
			s.rwlock.Lock()
			s.wireMessageBridge = nil
			s.wireRequestResponseChan = nil
			s.rwlock.Unlock()
			close(wireMessageBridge)
			close(rpcErrorChan)
		}
		defer cleanup()
		rpcresult, err := constructor.RPCRequest()
		if err != nil {
			select {
			case rpcErrorChan <- err:
				close(deliveredSignal)
				close(rpcErrorSignal)
			case <-deliveredSignal:
				errorPointer.Store(&err)
			case <-ctx.Done():
			}
			return
		}
		select {
		case <-deliveredSignal:
		case <-ctx.Done():
			return
		}
		resultPointer.Store(rpcresult)
	})
	exit := func(err error) error {
		for range wireMessageBridge {
		}
		bg.Wait()
		s.rwlock.Lock()
		for i, canceller := range s.cancellers {
			if lastidx := len(s.cancellers) - 1; canceller.ID() == id {
				s.cancellers[i] = s.cancellers[lastidx]
				s.cancellers = s.cancellers[:lastidx]
				break
			}
		}
		s.rwlock.Unlock()
		select {
		case <-s.ctx.Done():
			if state := s.cmd.ProcessState; state.ExitCode() > 0 {
				return errors.New(state.String())
			}
		default:
		}
		if err != nil {
			return err
		}
		return nil
	}
	select {
	case <-cargoAvailableChan:
		close(deliveredSignal)
		value := constructor.Construct(
			ctx,
			id,
			s.tp,
			errorPointer,
			resultPointer,
			s.wireProtocolVersion,
			wireMessageChan,
			wireRequestResponseChan,
			exit,
		)
		s.rwlock.Lock()
		s.cancellers = append(s.cancellers, I(value))
		s.rwlock.Unlock()
		return value, nil
	case err := <-rpcErrorChan:
		return nil, exit(err)
	case <-ctx.Done():
		return nil, exit(ctx.Err())
	}
}

type Responder struct {
	transport.Transport
	rwlock                  *sync.RWMutex
	pending                 *atomic.Int64
	wireMessageBridge       *chan wire.Message
	wireRequestResponseChan *chan wire.RequestResponse
	tools                   []Tool
	hookHandlers            map[string]HookHandler
}

func (r *Responder) Event(event *wire.EventParams) (*wire.EventResult, error) {
	r.pending.Add(1)
	defer r.pending.Add(-1)
	r.rwlock.RLock()
	defer r.rwlock.RUnlock()
	// 跳过未知 event type 留下的空 payload（见 wire/message.go 的 UnmarshalJSON 兼容逻辑）。
	if event.Payload == nil {
		return &wire.EventResult{}, nil
	}
	if *r.wireMessageBridge != nil {
		*r.wireMessageBridge <- event.Payload
	}
	return &wire.EventResult{}, nil
}

func (r *Responder) Request(request *wire.RequestParams) (wire.RequestResult, error) {
	r.pending.Add(1)
	defer r.pending.Add(-1)
	r.rwlock.RLock()
	defer r.rwlock.RUnlock()
	// HookRequest is dispatched on the registered handler if any, and falls
	// back to fail-open allow. It does NOT require an in-progress turn —
	// hooks fire during Initialize-time setup as well.
	if hookReq, ok := request.Payload.(wire.HookRequest); ok {
		return r.handleHookRequest(hookReq), nil
	}
	if *r.wireMessageBridge == nil || *r.wireRequestResponseChan == nil {
		return nil, jsonrpc2.Error{
			Code:    jsonrpc2.ErrorCodeInternalError,
			Message: "no roundtrip in progress",
		}
	}
	switch req := request.Payload.(type) {
	case wire.ApprovalRequest:
		req.Responder = ResponderFunc(func(rr wire.RequestResponse) error {
			if _, ok := rr.(wire.ApprovalRequestResponse); !ok {
				return fmt.Errorf("invalid approval request response type: %T", rr)
			}
			*r.wireRequestResponseChan <- rr
			return nil
		})
		*r.wireMessageBridge <- req
		return &wire.ApprovalResponse{
			RequestID: req.ID,
			Response:  (<-*r.wireRequestResponseChan).(wire.ApprovalRequestResponse),
		}, nil
	case wire.ToolCallRequest:
		for _, tool := range r.tools {
			if req.Name == tool.def.Name && req.Arguments.Valid {
				toolResult, err := tool.call(json.RawMessage(req.Arguments.Value))
				var output wire.Content
				if err != nil {
					output = wire.NewStringContent(err.Error())
				} else {
					output = wire.NewStringContent(toolResult)
				}
				return &wire.ToolResult{
					ToolCallID: req.ID,
					ReturnValue: wire.ToolResultReturnValue{
						IsError: err != nil,
						Output:  output,
						Message: "",
						Display: []wire.DisplayBlock{},
					},
				}, nil
			}
		}
		return nil, jsonrpc2.Error{
			Code:    jsonrpc2.ErrorCodeInvalidParams,
			Message: fmt.Sprintf("tool not found: %s", req.Name),
		}
	case wire.QuestionRequest:
		req.Responder = ResponderFunc(func(rr wire.RequestResponse) error {
			if _, ok := rr.(wire.QuestionResponse); !ok {
				return fmt.Errorf("invalid question response type: %T", rr)
			}
			*r.wireRequestResponseChan <- rr
			return nil
		})
		*r.wireMessageBridge <- req
		response := (<-*r.wireRequestResponseChan).(wire.QuestionResponse)
		response.RequestID = req.ID
		return &response, nil
	default:
		return nil, jsonrpc2.Error{
			Code:    jsonrpc2.ErrorCodeInvalidRequest,
			Message: fmt.Sprintf("unknown request type: %T", req),
		}
	}
}

// handleHookRequest invokes the user-registered handler for this hook
// subscription. Panics, missing handlers, and unknown actions all collapse
// to fail-open allow — matching the Node SDK contract — so a buggy hook
// can never block the cli indefinitely.
func (r *Responder) handleHookRequest(req wire.HookRequest) *wire.HookResponse {
	resp := &wire.HookResponse{RequestID: req.ID, Action: wire.HookActionAllow}
	handler, ok := r.hookHandlers[req.SubscriptionID]
	if !ok {
		// No handler — surface the event to the active turn (if any) so
		// observers can still react, then fail open.
		if r.wireMessageBridge != nil && *r.wireMessageBridge != nil {
			select {
			case *r.wireMessageBridge <- req:
			default:
			}
		}
		return resp
	}
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				resp.Action = wire.HookActionAllow
				resp.Reason = fmt.Sprintf("hook handler panic: %v", rec)
			}
		}()
		action, reason := handler(&req)
		if action == "" {
			action = wire.HookActionAllow
		}
		resp.Action = action
		resp.Reason = reason
	}()
	return resp
}

func (s *Session) Close() error {
	defer s.codec.Close()
	s.rwlock.Lock()
	cancels := make([]func() error, len(s.cancellers))
	for i, canceller := range s.cancellers {
		cancels[i] = canceller.Cancel
	}
	s.cancellers = nil
	s.rwlock.Unlock()
	for _, cancel := range cancels {
		cancel() //nolint:errcheck
	}
	return s.cmd.Cancel()
}

type stdio struct {
	io.WriteCloser
	io.ReadCloser
}

func (s *stdio) Close() error {
	return errors.Join(
		s.WriteCloser.Close(),
		s.ReadCloser.Close(),
	)
}

type ResponderFunc func(wire.RequestResponse) error

func (f ResponderFunc) Respond(r wire.RequestResponse) error {
	return f(r)
}

type Canceller interface {
	ID() uint64
	Cancel() error
}

type Cargo[R any] interface {
	Err() error
	Result() R
	Canceller
}

type Constructor[T any, R any] interface {
	RPCRequest() (*R, error)
	Construct(
		ctx context.Context,
		id uint64,
		stdioTransport transport.Transport,
		errorPointer *atomic.Pointer[error],
		resultPointer *atomic.Pointer[R],
		wireProtocolVersion string,
		wireMessageChan <-chan wire.Message,
		wireRequestResponseChan chan<- wire.RequestResponse,
		exit func(error) error,
	) *T
}

type turnConstructor struct {
	session *Session
	content wire.Content
}

func (tc *turnConstructor) RPCRequest() (*wire.PromptResult, error) {
	return tc.session.tp.Prompt(&wire.PromptParams{
		UserInput: tc.content,
	})
}

func (tc *turnConstructor) Construct(
	ctx context.Context,
	id uint64,
	stdioTransport transport.Transport,
	errorPointer *atomic.Pointer[error],
	resultPointer *atomic.Pointer[wire.PromptResult],
	wireProtocolVersion string,
	wireMessageChan <-chan wire.Message,
	wireRequestResponseChan chan<- wire.RequestResponse,
	exit func(error) error,
) *Turn {
	return turnBegin(
		ctx,
		id,
		tc.session.tp,
		errorPointer,
		resultPointer,
		wireProtocolVersion,
		wireMessageChan,
		wireRequestResponseChan,
		exit,
		tc.session,
	)
}

// wireAtLeast reports whether wire protocol version `have` is at least
// `want`. Both are dot-separated numeric strings (e.g. "1.2", "1.10").
//
// String comparison breaks at minor>=10 because "1.10" < "1.2" lexically.
// Compare numerically, segment by segment. Non-numeric or malformed parts
// fall back to string compare so unknown formats remain conservative.
func wireAtLeast(have, want string) bool {
	hp := strings.Split(have, ".")
	wp := strings.Split(want, ".")
	n := len(hp)
	if len(wp) > n {
		n = len(wp)
	}
	for i := 0; i < n; i++ {
		hs, ws := "0", "0"
		if i < len(hp) {
			hs = hp[i]
		}
		if i < len(wp) {
			ws = wp[i]
		}
		hn, herr := strconv.Atoi(hs)
		wn, werr := strconv.Atoi(ws)
		if herr != nil || werr != nil {
			if hs == ws {
				continue
			}
			return hs >= ws
		}
		if hn != wn {
			return hn > wn
		}
	}
	return true
}

func getWireProtocolVersion(executable string) (string, error) {
	cmd := exec.Command(executable, "info", "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	if !cmd.ProcessState.Success() {
		return "", errors.New(string(output))
	}
	var info struct {
		WireProtocolVersion string `json:"wire_protocol_version"`
	}
	if err := json.Unmarshal(output, &info); err != nil {
		return "", err
	}
	return info.WireProtocolVersion, nil
}
