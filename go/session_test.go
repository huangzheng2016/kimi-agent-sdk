package kimi

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MoonshotAI/kimi-agent-sdk/go/wire"
)

func TestResponder_Event(t *testing.T) {
	msgs := make(chan wire.Message, 1)
	usrc := make(chan wire.RequestResponse, 1)

	var rwlock sync.RWMutex
	responder := &Responder{rwlock: &rwlock, pending: new(atomic.Int64), wireMessageBridge: &msgs, wireRequestResponseChan: &usrc}

	event := &wire.EventParams{
		Type:    wire.EventTypeContentPart,
		Payload: wire.NewTextContentPart("hello"),
	}

	result, err := responder.Event(event)
	if err != nil {
		t.Fatalf("Event: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	select {
	case msg := <-msgs:
		cp, ok := msg.(wire.ContentPart)
		if !ok {
			t.Fatalf("expected ContentPart, got %T", msg)
		}
		if cp.Text.Value != "hello" {
			t.Errorf("expected text 'hello', got %s", cp.Text.Value)
		}
	default:
		t.Fatal("expected message in channel")
	}
}

func TestResponder_Event_NilMsgs(t *testing.T) {
	var msgs chan wire.Message
	usrc := make(chan wire.RequestResponse, 1)
	var rwlock sync.RWMutex
	responder := &Responder{rwlock: &rwlock, pending: new(atomic.Int64), wireMessageBridge: &msgs, wireRequestResponseChan: &usrc}

	event := &wire.EventParams{
		Type:    wire.EventTypeContentPart,
		Payload: wire.NewTextContentPart("hello"),
	}

	result, err := responder.Event(event)
	if err != nil {
		t.Fatalf("Event: %v", err)
	}
	// Should return empty result when msgs is nil
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestResponder_Request_ApprovalRequest(t *testing.T) {
	msgs := make(chan wire.Message, 1)
	usrc := make(chan wire.RequestResponse, 1)

	var rwlock sync.RWMutex
	responder := &Responder{rwlock: &rwlock, pending: new(atomic.Int64), wireMessageBridge: &msgs, wireRequestResponseChan: &usrc}

	approvalRequest := wire.ApprovalRequest{
		ID:          "req-123",
		ToolCallID:  "tool-456",
		Sender:      "agent",
		Action:      "execute",
		Description: "Run command",
	}

	request := &wire.RequestParams{
		Type:    wire.RequestTypeApprovalRequest,
		Payload: approvalRequest,
	}

	// Run in goroutine since it blocks waiting for response
	done := make(chan struct{})
	var result wire.RequestResult
	var err error
	go func() {
		result, err = responder.Request(request)
		close(done)
	}()

	// Receive the message and respond (with timeout)
	select {
	case msg := <-msgs:
		ar, ok := msg.(wire.ApprovalRequest)
		if !ok {
			t.Fatalf("expected ApprovalRequest, got %T", msg)
		}
		if ar.ID != "req-123" {
			t.Errorf("expected ID 'req-123', got %s", ar.ID)
		}
		// Respond with approve
		ar.Respond(wire.ApprovalRequestResponseApprove)
	case <-done:
		t.Fatal("request completed before message was received")
	}

	// Wait for result
	<-done

	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	resp, ok := result.(*wire.ApprovalResponse)
	if !ok {
		t.Fatalf("expected *wire.ApprovalResponse, got %T", result)
	}
	if resp.RequestID != "req-123" {
		t.Errorf("expected request_id 'req-123', got %s", resp.RequestID)
	}
	if resp.Response != wire.ApprovalRequestResponseApprove {
		t.Errorf("expected response 'approve', got %s", resp.Response)
	}
}

func TestResponder_Request_NilMsgs(t *testing.T) {
	var msgs chan wire.Message
	usrc := make(chan wire.RequestResponse, 1)
	var rwlock sync.RWMutex
	responder := &Responder{rwlock: &rwlock, pending: new(atomic.Int64), wireMessageBridge: &msgs, wireRequestResponseChan: &usrc}

	approvalRequest := wire.ApprovalRequest{
		ID:          "req-123",
		ToolCallID:  "tool-456",
		Sender:      "agent",
		Action:      "execute",
		Description: "Run command",
	}

	request := &wire.RequestParams{
		Type:    wire.RequestTypeApprovalRequest,
		Payload: approvalRequest,
	}

	_, err := responder.Request(request)
	// Should return error when wireMessageBridge is nil
	if err == nil {
		t.Fatal("expected error when wireMessageBridge is nil, got nil")
	}
}

func TestResponderFunc(t *testing.T) {
	var called bool
	var receivedResponse wire.RequestResponse

	f := ResponderFunc(func(rr wire.RequestResponse) error {
		called = true
		receivedResponse = rr
		return nil
	})

	err := f.Respond(wire.ApprovalRequestResponseApprove)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if !called {
		t.Error("ResponderFunc should have been called")
	}
	if receivedResponse != wire.ApprovalRequestResponseApprove {
		t.Errorf("expected response 'approve', got %s", receivedResponse)
	}
}

func TestCamelToSnake(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Prompt", "prompt"},
		{"Initialize", "initialize"},
		{"Cancel", "cancel"},
		{"Event", "event"},
		{"Request", "request"},
		{"Steer", "steer"},
		{"SetPlanMode", "set_plan_mode"},
		{"HTTPServer", "h_t_t_p_server"}, // documents naive behavior; not used by wire.
	}
	for _, tc := range cases {
		if got := camelToSnake(tc.in); got != tc.want {
			t.Errorf("camelToSnake(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSnakeToPascal(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"event", "Event"},
		{"request", "Request"},
		{"set_plan_mode", "SetPlanMode"},
		{"initialize", "Initialize"},
	}
	for _, tc := range cases {
		if got := snakeToPascal(tc.in); got != tc.want {
			t.Errorf("snakeToPascal(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResponder_HookRequest_DispatchesHandler(t *testing.T) {
	var msgs chan wire.Message
	var usrc chan wire.RequestResponse
	var rwlock sync.RWMutex
	var called bool
	handler := func(req *wire.HookRequest) (wire.HookAction, string) {
		called = true
		if req.SubscriptionID != "sub-1" {
			t.Errorf("SubscriptionID=%q, want sub-1", req.SubscriptionID)
		}
		return wire.HookActionBlock, "denied"
	}
	responder := &Responder{
		rwlock:                  &rwlock,
		pending:                 new(atomic.Int64),
		wireMessageBridge:       &msgs,
		wireRequestResponseChan: &usrc,
		hookHandlers:            map[string]HookHandler{"sub-1": handler},
	}
	res, err := responder.Request(&wire.RequestParams{
		Type: wire.RequestTypeHookRequest,
		Payload: wire.HookRequest{
			ID:             "hook-1",
			SubscriptionID: "sub-1",
			Event:          "PreToolUse",
			Target:         "Shell",
		},
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if !called {
		t.Fatal("handler not invoked")
	}
	hr, ok := res.(*wire.HookResponse)
	if !ok {
		t.Fatalf("unexpected response type: %T", res)
	}
	if hr.Action != wire.HookActionBlock || hr.Reason != "denied" || hr.RequestID != "hook-1" {
		t.Fatalf("unexpected response: %+v", hr)
	}
}

func TestResponder_HookRequest_FailOpenOnPanic(t *testing.T) {
	var msgs chan wire.Message
	var usrc chan wire.RequestResponse
	var rwlock sync.RWMutex
	responder := &Responder{
		rwlock:                  &rwlock,
		pending:                 new(atomic.Int64),
		wireMessageBridge:       &msgs,
		wireRequestResponseChan: &usrc,
		hookHandlers: map[string]HookHandler{
			"sub-1": func(req *wire.HookRequest) (wire.HookAction, string) {
				panic("kaboom")
			},
		},
	}
	res, err := responder.Request(&wire.RequestParams{
		Type:    wire.RequestTypeHookRequest,
		Payload: wire.HookRequest{ID: "hook-1", SubscriptionID: "sub-1"},
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	hr := res.(*wire.HookResponse)
	if hr.Action != wire.HookActionAllow {
		t.Fatalf("Action=%q, want allow (fail-open)", hr.Action)
	}
}

func TestResponder_HookRequest_NoHandlerFailsOpen(t *testing.T) {
	var msgs chan wire.Message
	var usrc chan wire.RequestResponse
	var rwlock sync.RWMutex
	responder := &Responder{
		rwlock:                  &rwlock,
		pending:                 new(atomic.Int64),
		wireMessageBridge:       &msgs,
		wireRequestResponseChan: &usrc,
	}
	res, err := responder.Request(&wire.RequestParams{
		Type:    wire.RequestTypeHookRequest,
		Payload: wire.HookRequest{ID: "hook-1", SubscriptionID: "unknown"},
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	hr := res.(*wire.HookResponse)
	if hr.Action != wire.HookActionAllow {
		t.Fatalf("Action=%q, want allow", hr.Action)
	}
}

func TestStdio_Close(t *testing.T) {
	// Create mock readers/writers
	r, w := io.Pipe()

	s := &stdio{
		WriteCloser: w,
		ReadCloser:  r,
	}

	err := s.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify both are closed by checking that writes/reads fail
	_, writeErr := w.Write([]byte("test"))
	if writeErr == nil {
		t.Error("expected write to fail after close")
	}

	_, readErr := r.Read(make([]byte, 1))
	if readErr == nil {
		t.Error("expected read to fail after close")
	}
}
