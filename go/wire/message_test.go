package wire

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Compile-time interface checks.
var (
	_ Message = TurnBegin{}
	_ Message = TurnEnd{}
	_ Message = StepBegin{}
	_ Message = StepInterrupted{}
	_ Message = CompactionBegin{}
	_ Message = CompactionEnd{}
	_ Message = StatusUpdate{}
	_ Message = ContentPart{}
	_ Message = ToolCallRequest{}
	_ Message = ToolCallPart{}
	_ Message = ToolResult{}
	_ Message = SubagentEvent{}
	_ Message = ApprovalRequestResolved{}
	_ Message = ApprovalResponse{}
	_ Message = ApprovalRequest{}
	_ Message = HookTriggered{}
	_ Message = HookResolved{}
	_ Message = SteerInput{}
	_ Message = ParseError{}
	_ Message = QuestionRequest{}
	_ Message = HookRequest{}

	_ Event = TurnBegin{}
	_ Event = TurnEnd{}
	_ Event = StepBegin{}
	_ Event = StepInterrupted{}
	_ Event = CompactionBegin{}
	_ Event = CompactionEnd{}
	_ Event = StatusUpdate{}
	_ Event = ContentPart{}
	_ Event = ToolCall{}
	_ Event = ToolCallPart{}
	_ Event = ToolResult{}
	_ Event = SubagentEvent{}
	_ Event = ApprovalRequestResolved{}
	_ Event = ApprovalResponse{}
	_ Event = HookTriggered{}
	_ Event = HookResolved{}
	_ Event = SteerInput{}
	_ Event = ParseError{}

	_ Request = ApprovalRequest{}
	_ Request = ToolCallRequest{}
	_ Request = QuestionRequest{}
	_ Request = HookRequest{}
)

func TestEvent_EventTypeConstants(t *testing.T) {
	cases := []struct {
		name string
		evt  Event
		want EventType
	}{
		{"TurnBegin", TurnBegin{}, EventTypeTurnBegin},
		{"TurnEnd", TurnEnd{}, EventTypeTurnEnd},
		{"StepBegin", StepBegin{}, EventTypeStepBegin},
		{"StepInterrupted", StepInterrupted{}, EventTypeStepInterrupted},
		{"CompactionBegin", CompactionBegin{}, EventTypeCompactionBegin},
		{"CompactionEnd", CompactionEnd{}, EventTypeCompactionEnd},
		{"StatusUpdate", StatusUpdate{}, EventTypeStatusUpdate},
		{"ContentPart", ContentPart{}, EventTypeContentPart},
		{"ToolCall", ToolCall{}, EventTypeToolCall},
		{"ToolCallPart", ToolCallPart{}, EventTypeToolCallPart},
		{"ToolResult", ToolResult{}, EventTypeToolResult},
		{"SubagentEvent", SubagentEvent{}, EventTypeSubagentEvent},
		{"ApprovalRequestResolved", ApprovalRequestResolved{}, EventTypeApprovalRequestResolved},
		{"ApprovalResponse", ApprovalResponse{}, EventTypeApprovalResponse},
		{"HookTriggered", HookTriggered{}, EventTypeHookTriggered},
		{"HookResolved", HookResolved{}, EventTypeHookResolved},
		{"SteerInput", SteerInput{}, EventTypeSteerInput},
		{"ParseError", ParseError{}, EventTypeParseError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.evt.EventType(); got != tc.want {
				t.Fatalf("EventType()=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestContent_JSONRoundTrip_Text(t *testing.T) {
	in := NewStringContent("hello")
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != "\"hello\"" {
		t.Fatalf("unexpected JSON: %s", string(b))
	}

	var out Content
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Type != ContentTypeText {
		t.Fatalf("Type=%q, want %q", out.Type, ContentTypeText)
	}
	if out.Text.Value != "hello" {
		t.Fatalf("Text=%q, want %q", out.Text.Value, "hello")
	}
}

func TestContent_JSONRoundTrip_ContentParts(t *testing.T) {
	in := NewContent(NewTextContentPart("hi"))
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(b) == 0 || b[0] != '[' {
		t.Fatalf("expected JSON array, got: %s", string(b))
	}

	var out Content
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Type != ContentTypeContentParts {
		t.Fatalf("Type=%q, want %q", out.Type, ContentTypeContentParts)
	}
	if len(out.ContentParts.Value) != 1 || out.ContentParts.Value[0].Text.Value != "hi" || out.ContentParts.Value[0].Type != ContentPartTypeText {
		t.Fatalf("unexpected ContentParts: %+v", out.ContentParts)
	}
}

func TestContent_MarshalJSON_InvalidType(t *testing.T) {
	in := Content{Type: ContentType("bad")}
	_, err := json.Marshal(in)
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestContent_UnmarshalJSON_InvalidToken(t *testing.T) {
	var c Content
	if err := json.Unmarshal([]byte(`{"k":1}`), &c); err == nil {
		t.Fatalf("expected error")
	}
}

func TestOptional_JSON(t *testing.T) {
	o := Optional[int]{}
	b, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != "null" {
		t.Fatalf("expected null, got %s", string(b))
	}

	var o2 Optional[int]
	if err := json.Unmarshal([]byte("123"), &o2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !o2.Valid || o2.Value != 123 {
		t.Fatalf("unexpected Optional: %+v", o2)
	}

	var o3 Optional[int]
	if err := json.Unmarshal([]byte(" null "), &o3); err != nil {
		t.Fatalf("Unmarshal null: %v", err)
	}
	if o3.Valid {
		t.Fatalf("expected Valid=false")
	}
}

type badResponderFunc func(RequestResponse) error

func (f badResponderFunc) Respond(r RequestResponse) error {
	return f(r)
}

func TestPromptResult_UnmarshalJSON_WithSteps(t *testing.T) {
	var pr PromptResult
	if err := json.Unmarshal([]byte(`{"status":"finished","steps":3}`), &pr); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if pr.Status != PromptResultStatusFinished {
		t.Fatalf("Status=%q, want %q", pr.Status, PromptResultStatusFinished)
	}
	if !pr.Steps.Valid || pr.Steps.Value != 3 {
		t.Fatalf("unexpected Steps: %+v", pr.Steps)
	}
}

func TestPromptResult_UnmarshalJSON_NullSteps(t *testing.T) {
	var pr PromptResult
	if err := json.Unmarshal([]byte(`{"status":"pending","steps":null}`), &pr); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if pr.Status != PromptResultStatusPending {
		t.Fatalf("Status=%q, want %q", pr.Status, PromptResultStatusPending)
	}
	if pr.Steps.Valid {
		t.Fatalf("expected Steps.Valid=false, got %+v", pr.Steps)
	}
}

func TestApprovalRequest_MarshalJSON_IgnoresResponder(t *testing.T) {
	ar := ApprovalRequest{
		Responder:   badResponderFunc(func(RequestResponse) error { return nil }),
		ID:          "rid",
		ToolCallID:  "tcid",
		Sender:      "sender",
		Action:      "action",
		Description: "desc",
	}
	b, err := json.Marshal(ar)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := m["Responder"]; ok {
		t.Fatalf("expected Responder to be omitted")
	}
	if _, ok := m["responder"]; ok {
		t.Fatalf("expected responder to be omitted")
	}
	if got := m["id"]; got != "rid" {
		t.Fatalf("id=%v, want %q", got, "rid")
	}
}

func TestEventParams_UnmarshalJSON_AllEventTypes(t *testing.T) {
	turn := TurnBegin{UserInput: NewStringContent("hi")}
	sub := SubagentEvent{
		ParentToolCallID: "ttc",
		Event: EventParams{
			Type:    EventTypeTurnBegin,
			Payload: turn,
		},
	}

	cases := []struct {
		name    string
		typeVal EventType
		payload Event
	}{
		{"TurnBegin", EventTypeTurnBegin, turn},
		{"TurnEnd", EventTypeTurnEnd, TurnEnd{}},
		{"StepBegin", EventTypeStepBegin, StepBegin{N: 1}},
		{"StepInterrupted", EventTypeStepInterrupted, StepInterrupted{}},
		{"CompactionBegin", EventTypeCompactionBegin, CompactionBegin{}},
		{"CompactionEnd", EventTypeCompactionEnd, CompactionEnd{}},
		{"StatusUpdate", EventTypeStatusUpdate, StatusUpdate{ContextUsage: Optional[float64]{Value: 0.5, Valid: true}}},
		{"StatusUpdate_PlanMode", EventTypeStatusUpdate, StatusUpdate{PlanMode: Optional[bool]{Value: true, Valid: true}}},
		{"ContentPart", EventTypeContentPart, NewTextContentPart("hello")},
		{"ToolCall", EventTypeToolCall, ToolCall{Type: "function", ID: "1", Function: ToolCallFunction{Name: "f"}}},
		{"ToolCallPart", EventTypeToolCallPart, ToolCallPart{ArgumentsPart: Optional[string]{Value: "x", Valid: true}}},
		{"ToolResult", EventTypeToolResult, ToolResult{ToolCallID: "1", ReturnValue: ToolResultReturnValue{IsError: false, Output: NewStringContent("ok"), Message: "m"}}},
		{"SubagentEvent", EventTypeSubagentEvent, sub},
		{"ApprovalRequestResolved", EventTypeApprovalRequestResolved, ApprovalRequestResolved{RequestID: "rid", Response: ApprovalRequestResponseApprove}},
		{"ApprovalResponse", EventTypeApprovalResponse, ApprovalResponse{RequestID: "rid", Response: ApprovalRequestResponseApprove}},
		{"HookTriggered", EventTypeHookTriggered, HookTriggered{Event: "PreToolUse", Target: "Shell", HookCount: 2}},
		{"HookResolved", EventTypeHookResolved, HookResolved{Event: "PreToolUse", Target: "Shell", Action: HookActionAllow, Reason: "", DurationMs: 42}},
		{"SteerInput", EventTypeSteerInput, SteerInput{UserInput: NewStringContent("steer")}},
		{"ParseError", EventTypeParseError, ParseError{Code: "SCHEMA_MISMATCH", Message: "bad payload"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(map[string]any{
				"type":    tc.typeVal,
				"payload": tc.payload,
			})
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			var got EventParams
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got.Type != tc.typeVal {
				t.Fatalf("Type=%q, want %q", got.Type, tc.typeVal)
			}
			if got.Payload == nil {
				t.Fatalf("Payload is nil")
			}
			if got.Payload.EventType() != tc.typeVal {
				t.Fatalf("Payload.EventType()=%q, want %q", got.Payload.EventType(), tc.typeVal)
			}
			if !reflect.DeepEqual(got.Payload, tc.payload) {
				t.Fatalf("payload mismatch\n got: %#v\nwant: %#v", got.Payload, tc.payload)
			}
		})
	}
}

// TestEventParams_UnmarshalJSON_UnknownTypeTolerated documents the wire
// resilience contract introduced in 88b05b3: unknown event types do NOT
// raise — they record the Type but leave Payload nil so the net/rpc loop
// keeps serving. Downstream (Responder.Event) must guard on nil Payload.
func TestEventParams_UnmarshalJSON_UnknownTypeTolerated(t *testing.T) {
	var p EventParams
	if err := json.Unmarshal([]byte(`{"type":"DoesNotExist","payload":{}}`), &p); err != nil {
		t.Fatalf("expected no error for unknown event type, got %v", err)
	}
	if p.Type != "DoesNotExist" {
		t.Fatalf("Type=%q, want %q", p.Type, "DoesNotExist")
	}
	if p.Payload != nil {
		t.Fatalf("Payload should be nil for unknown event type, got %T", p.Payload)
	}
}

func TestSubagentEvent_JSONFieldRename(t *testing.T) {
	sub := SubagentEvent{ParentToolCallID: "tc-1"}
	b, err := json.Marshal(sub)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := raw["parent_tool_call_id"]; !ok {
		t.Fatalf("expected parent_tool_call_id field; got %v", raw)
	}
	if _, ok := raw["task_tool_call_id"]; ok {
		t.Fatalf("old task_tool_call_id field must be gone; got %v", raw)
	}

	// Decoding the new wire field must populate ParentToolCallID.
	var parsed SubagentEvent
	if err := json.Unmarshal([]byte(`{"parent_tool_call_id":"tc-2","event":{"type":"TurnEnd","payload":{}}}`), &parsed); err != nil {
		t.Fatalf("decode parent_tool_call_id: %v", err)
	}
	if parsed.ParentToolCallID != "tc-2" {
		t.Fatalf("ParentToolCallID=%q, want %q", parsed.ParentToolCallID, "tc-2")
	}
}

func TestRequestParams_UnmarshalJSON_ApprovalRequest(t *testing.T) {
	payload := ApprovalRequest{
		ID:          "rid",
		ToolCallID:  "tcid",
		Sender:      "sender",
		Action:      "action",
		Description: "desc",
	}
	b, err := json.Marshal(map[string]any{
		"type":    RequestTypeApprovalRequest,
		"payload": payload,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got RequestParams
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Type != RequestTypeApprovalRequest {
		t.Fatalf("Type=%q, want %q", got.Type, RequestTypeApprovalRequest)
	}
	ar, ok := got.Payload.(ApprovalRequest)
	if !ok {
		t.Fatalf("unexpected payload type: %T", got.Payload)
	}
	if ar.ID != "rid" {
		t.Fatalf("ID=%q, want %q", ar.ID, "rid")
	}
	if got.Payload.RequestType() != RequestTypeApprovalRequest {
		t.Fatalf("RequestType()=%q, want %q", got.Payload.RequestType(), RequestTypeApprovalRequest)
	}
}

func TestRequestParams_UnmarshalJSON_UnknownTypeReturnsError(t *testing.T) {
	var p RequestParams
	err := json.Unmarshal([]byte(`{"type":"DoesNotExist","payload":{}}`), &p)
	if err == nil {
		t.Fatalf("expected error for unknown request type")
	}
}

func TestRequestParams_UnmarshalJSON_QuestionRequest(t *testing.T) {
	raw := `{
		"type":"QuestionRequest",
		"payload":{
			"id":"q1",
			"tool_call_id":"tc1",
			"questions":[{"question":"continue?","options":[{"label":"yes"},{"label":"no"}]}]
		}
	}`
	var got RequestParams
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Type != RequestTypeQuestionRequest {
		t.Fatalf("Type=%q, want %q", got.Type, RequestTypeQuestionRequest)
	}
	qr, ok := got.Payload.(QuestionRequest)
	if !ok {
		t.Fatalf("unexpected payload type: %T", got.Payload)
	}
	if qr.ID != "q1" || qr.ToolCallID != "tc1" || len(qr.Questions) != 1 {
		t.Fatalf("unexpected QuestionRequest: %+v", qr)
	}
	if qr.Questions[0].Question != "continue?" {
		t.Fatalf("Question=%q", qr.Questions[0].Question)
	}
}

func TestRequestParams_UnmarshalJSON_HookRequest(t *testing.T) {
	raw := `{
		"type":"HookRequest",
		"payload":{
			"id":"h1",
			"subscription_id":"sub1",
			"event":"PreToolUse",
			"target":"Shell",
			"input_data":{"command":"ls"}
		}
	}`
	var got RequestParams
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Type != RequestTypeHookRequest {
		t.Fatalf("Type=%q, want %q", got.Type, RequestTypeHookRequest)
	}
	hr, ok := got.Payload.(HookRequest)
	if !ok {
		t.Fatalf("unexpected payload type: %T", got.Payload)
	}
	if hr.ID != "h1" || hr.SubscriptionID != "sub1" || hr.Event != "PreToolUse" || hr.Target != "Shell" {
		t.Fatalf("unexpected HookRequest: %+v", hr)
	}
	if cmd, _ := hr.InputData["command"].(string); cmd != "ls" {
		t.Fatalf("InputData[command]=%v, want %q", hr.InputData["command"], "ls")
	}
}
