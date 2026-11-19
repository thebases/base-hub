package nodes

// File overview:
// - Responsibility: owns the node handler contract and registry — the type every node file
//   implements, the runtime hooks a node can call into, and the actionCode -> handler lookup
//   the engine uses to dispatch a step. Each node's actual logic lives in its own file
//   (crud_model.go, notify.go, ...); this file only wires them together.
// - Key symbols: `registerNode` (each node file's one registration), `RegisterWorkflowActionRuntimeHooks`, `ResolveWorkflowActionHandler`, `WorkflowActionHandler`, `WorkflowActionRequest`.

import (
	"context"
	"strconv"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

type WorkflowActionHandler func(ctx context.Context, req WorkflowActionRequest) ([]byte, error)

type WorkflowActionRequest struct {
	Topic   string
	Action  workflowmodels.WorkflowActionRecord
	Step    workflowmodels.Step
	Payload []byte
	// CredentialRefs maps each param resolved from a @cred:/@syscred: reference to that
	// reference (set by the engine), so outbound rows store the reference, not the secret.
	CredentialRefs map[string]string
}

type WorkflowActionRuntimeHooks struct {
	CreateModelRecord  func(context.Context, string, map[string]any) (map[string]any, error)
	SearchModelRecords func(context.Context, string, map[string]any, []string) ([]map[string]any, error)
	WriteModelRecords  func(context.Context, string, []string, map[string]any) (bool, error)
	DeleteModelRecords func(context.Context, string, []string) (bool, error)
	// QueueHTTPJSON(ctx, topic, url, headers, authorizationRef, body); authorizationRef is the
	// reference the Authorization header was resolved from, or "".
	QueueHTTPJSON     func(context.Context, string, string, map[string]string, string, map[string]any) error
	PostHTTPJSON      func(context.Context, string, map[string]string, map[string]any) (int, string, error)
	FlattenJSON       func(map[string]any, map[string]any)
	HMACAction        func(string, map[string]any, []string) (string, string, string, error)
	AESAction         func(string, map[string]any) (string, string, string, error)
	AESDecryptAction  func(string, string) (string, error)
	EvaluateCondition func(expression any, bindings map[string]any) (bool, error)
	// RunLuaAction runs the stored automation.action (action_type lua) with that id. globals are
	// bound next to `payload` (raw, data, topic, step_key). The workflow never runs inline Lua.
	// RunLuaActionByRef is RunLuaAction addressed by the action's seed external_id, for seeded
	// workflows that cannot know the row id.
	RunLuaActionByRef func(ctx context.Context, ref string, payload map[string]any, globals map[string]any) (map[string]any, error)
	RunLuaAction      func(ctx context.Context, actionID int64, payload map[string]any, globals map[string]any) (map[string]any, error)
	// CallWorkflow runs the published snapshot of workflow workflowID inline as a child run of the
	// calling run (plan T103) and returns its final payload. label tells sibling child runs of one
	// step apart in their run names (for_each item index).
	CallWorkflow func(ctx context.Context, workflowID int64, payload []byte, label string) ([]byte, error)
}

var workflowActionRuntimeHooks WorkflowActionRuntimeHooks

// RegisterWorkflowActionRuntimeHooks registers the workflow action runtime hooks handled by this package.
func RegisterWorkflowActionRuntimeHooks(hooks WorkflowActionRuntimeHooks) {
	workflowActionRuntimeHooks = hooks
}

// CurrentWorkflowActionRuntimeHooks returns the registered hooks, so a test that swaps them can
// restore the runtime's set afterwards.
func CurrentWorkflowActionRuntimeHooks() WorkflowActionRuntimeHooks {
	return workflowActionRuntimeHooks
}

// Short local names for the definition types, so a node file declares its NodeDef without
// importing the models package just for them.
type (
	nodeDef     = workflowmodels.NodeDef
	paramDef    = workflowmodels.ParamDef
	paramOption = workflowmodels.NodeParamOption
)

const (
	secretCredentialRef = workflowmodels.ParamSecretCredential
	valueTypeInt        = workflowmodels.ParamValueInt

	effectHTTP   = workflowmodels.NodeEffectHTTP
	effectOutbox = workflowmodels.NodeEffectOutbox
	effectWrite  = workflowmodels.NodeEffectWrite
	effectScript = workflowmodels.NodeEffectScript
)

// authorizationParam is the shared Authorization field of the outbound nodes (notify,
// post_output_object): an @syscred:/@cred: reference resolved when the step runs.
var authorizationParam = paramDef{
	Name:   "authorization",
	Label:  "Authorization",
	Type:   "string",
	Help:   "Select a sys.credential to send its access token as the Authorization header. Leave empty to send none.",
	Column: "authorization",
	Secret: workflowmodels.ParamSecretSysCredential,
}

// workflowActionHandlers is keyed by "<code>@<version>" and filled only from the node files'
// init functions (registerNode), before any lookup runs.
var workflowActionHandlers = map[string]WorkflowActionHandler{}

// registerNode is the one registration a node file makes: its definition goes to the models
// registry (canvas spec, catalog entry, validation) and its handler to the dispatch table.
func registerNode(def nodeDef, handler WorkflowActionHandler) {
	if handler == nil {
		panic("workflow: node " + def.Code + " registered without a handler")
	}
	workflowmodels.RegisterNodeDef(def)
	workflowActionHandlers[handlerKey(def.Code, def.Version)] = handler
}

func handlerKey(code string, version int) string {
	if version <= 0 {
		version = 1
	}
	return code + "@" + strconv.Itoa(version)
}

// ResolveWorkflowActionHandler resolves the workflow action handler handled by this package.
func ResolveWorkflowActionHandler(actionCode string, version int) (WorkflowActionHandler, bool) {
	entry, ok := workflowmodels.ResolveBuiltinWorkflowAction(actionCode, version)
	if !ok {
		return nil, false
	}
	handler, ok := workflowActionHandlers[handlerKey(entry.HandlerKey, entry.HandlerVersion)]
	return handler, ok
}
