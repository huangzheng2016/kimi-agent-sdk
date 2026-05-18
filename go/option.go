package kimi

import (
	"encoding/json"

	"github.com/MoonshotAI/kimi-agent-sdk/go/wire"
)

type Option func(*option)

// HookHandler is invoked when the kimi cli fires a wire-subscribed hook
// (Wire 1.7+). The returned action decides whether the underlying lifecycle
// event (e.g. PreToolUse) is allowed to proceed; reason is forwarded to the
// server for logging/UI surface. A handler that panics or returns an empty
// action falls back to allow (fail-open), mirroring the Node SDK behavior.
type HookHandler func(req *wire.HookRequest) (action wire.HookAction, reason string)

type option struct {
	exec         string
	args         []string
	envs         []string
	tools        []Tool
	hooks        []wire.HookSubscription
	hookHandlers map[string]HookHandler
	clientInfo   wire.Optional[wire.ClientInfo]
	capabilities wire.Optional[wire.ClientCapabilities]
}

func WithExecutable(executable string) Option {
	return func(opt *option) {
		opt.exec = executable
	}
}

func WithBaseURL(baseURL string) Option {
	return func(opt *option) {
		opt.envs = append(opt.envs, "KIMI_BASE_URL="+baseURL)
	}
}

func WithAPIKey(apiKey string) Option {
	return func(opt *option) {
		opt.envs = append(opt.envs, "KIMI_API_KEY="+apiKey)
	}
}

func WithConfig(config *Config) Option {
	return func(opt *option) {
		// SAFETY: we guaranteed that the config is valid to be marshalled to JSON
		cfg, _ := json.Marshal(config)
		opt.args = append(opt.args, "--config", string(cfg))
	}
}

func WithConfigFile(file string) Option {
	return func(opt *option) {
		opt.args = append(opt.args, "--config-file", file)
	}
}

func WithModel(model string) Option {
	return func(opt *option) {
		opt.args = append(opt.args, "--model", model)
	}
}

func WithWorkDir(dir string) Option {
	return func(opt *option) {
		opt.args = append(opt.args, "--work-dir", dir)
	}
}

func WithSession(session string) Option {
	return func(opt *option) {
		opt.args = append(opt.args, "--session", session)
	}
}

func WithMCPConfigFile(file string) Option {
	return func(opt *option) {
		opt.args = append(opt.args, "--mcp-config-file", file)
	}
}

func WithMCPConfig(config *MCPConfig) Option {
	return func(opt *option) {
		cfg, _ := json.Marshal(config)
		opt.args = append(opt.args, "--mcp-config", string(cfg))
	}
}

func WithAutoApprove() Option {
	return func(opt *option) {
		opt.args = append(opt.args, "--auto-approve")
	}
}

func WithThinking(thinking bool) Option {
	return func(opt *option) {
		if thinking {
			opt.args = append(opt.args, "--thinking")
		} else {
			opt.args = append(opt.args, "--no-thinking")
		}
	}
}

func WithSkillsDir(dir string) Option {
	return func(opt *option) {
		opt.args = append(opt.args, "--skills-dir", dir)
	}
}

// WithArgs appends custom command line arguments.
func WithArgs(args ...string) Option {
	return func(opt *option) {
		opt.args = append(opt.args, args...)
	}
}

func WithTools(tools ...Tool) Option {
	return func(opt *option) {
		opt.tools = append(opt.tools, tools...)
	}
}

// WithClientInfo identifies the SDK consumer to the kimi cli during
// Initialize. Name is required by the cli for telemetry/logging; version
// is free-form.
func WithClientInfo(name, version string) Option {
	return func(opt *option) {
		opt.clientInfo = wire.Optional[wire.ClientInfo]{
			Value: wire.ClientInfo{Name: name, Version: version},
			Valid: true,
		}
	}
}

// WithCapabilities overrides the default client capabilities advertised to
// the cli. By default the SDK sets supports_question=true and
// supports_plan_mode=true; use this Option when an embedder wants to disable
// one of them (e.g. a non-interactive runner that cannot answer
// QuestionRequests).
func WithCapabilities(c wire.ClientCapabilities) Option {
	return func(opt *option) {
		opt.capabilities = wire.Optional[wire.ClientCapabilities]{
			Value: c,
			Valid: true,
		}
	}
}

// WithHook registers a Wire 1.7 hook subscription together with the handler
// that should resolve it. The subscription is advertised to the cli during
// Initialize; whenever the cli later sends a HookRequest with the same
// SubscriptionID, the handler is invoked synchronously and its result is
// returned as the HookResponse.
//
// Handlers should be fast and non-blocking — the cli stalls the underlying
// lifecycle event (e.g. PreToolUse) until a response arrives. A handler that
// panics or returns an empty action is treated as allow (fail-open),
// mirroring the Node SDK behavior.
func WithHook(subscription wire.HookSubscription, handler HookHandler) Option {
	return func(opt *option) {
		opt.hooks = append(opt.hooks, subscription)
		if opt.hookHandlers == nil {
			opt.hookHandlers = map[string]HookHandler{}
		}
		opt.hookHandlers[subscription.ID] = handler
	}
}

// WithShareDir sets KIMI_SHARE_DIR for the spawned cli process. The cli
// uses this directory for cross-session artifacts (transcripts, attachments,
// etc.).
func WithShareDir(dir string) Option {
	return func(opt *option) {
		opt.envs = append(opt.envs, "KIMI_SHARE_DIR="+dir)
	}
}
