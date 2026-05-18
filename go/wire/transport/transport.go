package transport

import (
	"github.com/MoonshotAI/kimi-agent-sdk/go/wire"
)

//go:generate go tool defc generate -T Transport -o transport_impl.go
//go:generate go tool mockgen -source=transport.go -destination=transport_mock.go -package=transport
type Transport interface {
	Initialize(params *wire.InitializeParams) (*wire.InitializeResult, error)
	Prompt(params *wire.PromptParams) (*wire.PromptResult, error)
	Cancel(params *wire.CancelParams) (*wire.CancelResult, error)
	SetPlanMode(params *wire.SetPlanModeParams) (*wire.SetPlanModeResult, error)
	Steer(params *wire.SteerParams) (*wire.SteerResult, error)
	Event(event *wire.EventParams) (*wire.EventResult, error)
	Request(request *wire.RequestParams) (wire.RequestResult, error)
}
