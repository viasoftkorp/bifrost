package governance

import (
	"context"
	"encoding/json"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/stretchr/testify/require"
)

// TestHTTPTransportPreHook_VirtualKeyReplicateRefinesNestedModel verifies that
// virtual-key provider pinning rewrites the request model to Replicate's nested provider slug.
func TestHTTPTransportPreHook_VirtualKeyReplicateRefinesNestedModel(t *testing.T) {
	logger := NewMockLogger()
	mc := modelcatalog.NewTestCatalog(map[string]string{
		"openai/gpt-5-nano": "gpt-5-nano",
	})
	mc.UpsertModelDataForProvider(schemas.Replicate, &schemas.BifrostListModelsResponse{
		Data: []schemas.Model{
			{ID: "replicate/openai/gpt-5-nano"},
		},
	}, nil)

	virtualKey := buildVirtualKeyWithProviders(
		"vk1",
		"sk-bf-test",
		"replicate-only",
		[]configstoreTables.TableVirtualKeyProviderConfig{
			buildProviderConfig("replicate", []string{"*"}),
		},
	)
	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys: []configstoreTables.TableVirtualKey{*virtualKey},
	}, mc)
	require.NoError(t, err)

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, mc, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "POST"
	req.Path = "/v1/chat/completions"
	req.Headers["Authorization"] = "Bearer sk-bf-test"
	req.Headers["Content-Type"] = "application/json"
	req.Body = []byte(`{"model":"gpt-5-nano","messages":[{"role":"user","content":"Hello!"}]}`)

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	var payload struct {
		Model string `json:"model"`
	}
	require.NoError(t, json.Unmarshal(req.Body, &payload))
	require.Equal(t, "replicate/openai/gpt-5-nano", payload.Model)
}

func TestHTTPTransportPreHook_ModelOnlyVirtualKeySetsAvailableProviders(t *testing.T) {
	logger := NewMockLogger()

	openAIConfig := buildProviderConfig("openai", []string{"gpt-4o"})
	openAIConfig.Weight = nil
	anthropicConfig := buildProviderConfig("anthropic", []string{"claude-3-5-sonnet"})
	anthropicConfig.Weight = nil

	virtualKey := buildVirtualKeyWithProviders(
		"vk-constraint",
		"sk-bf-constraint-test",
		"provider-constraint-vk",
		[]configstoreTables.TableVirtualKeyProviderConfig{
			openAIConfig,
			anthropicConfig,
		},
	)
	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys: []configstoreTables.TableVirtualKey{*virtualKey},
	}, nil)
	require.NoError(t, err)

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, nil, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "POST"
	req.Path = "/v1/chat/completions"
	req.Headers["Authorization"] = "Bearer sk-bf-constraint-test"
	req.Headers["Content-Type"] = "application/json"
	req.Body = []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hello!"}]}`)

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	allowedProviders, ok := bfCtx.Value(schemas.BifrostContextKeyAvailableProviders).([]schemas.ModelProvider)
	require.True(t, ok, "provider constraint should be set")
	require.Equal(t, []schemas.ModelProvider{schemas.OpenAI}, allowedProviders)
}

func TestHTTPTransportPreHook_ModelOnlyVirtualKeySetsEmptyAvailableProvidersWhenNoProviderAllowsModel(t *testing.T) {
	logger := NewMockLogger()

	virtualKey := buildVirtualKeyWithProviders(
		"vk-empty-constraint",
		"sk-bf-empty-constraint-test",
		"empty-provider-constraint-vk",
		[]configstoreTables.TableVirtualKeyProviderConfig{
			buildProviderConfig("openai", []string{"gpt-4o"}),
		},
	)
	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys: []configstoreTables.TableVirtualKey{*virtualKey},
	}, nil)
	require.NoError(t, err)

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, nil, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "POST"
	req.Path = "/v1/chat/completions"
	req.Headers["Authorization"] = "Bearer sk-bf-empty-constraint-test"
	req.Headers["Content-Type"] = "application/json"
	req.Body = []byte(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"Hello!"}]}`)

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	allowedProviders, ok := bfCtx.Value(schemas.BifrostContextKeyAvailableProviders).([]schemas.ModelProvider)
	require.True(t, ok, "provider constraint should be set")
	require.Empty(t, allowedProviders)
}

// TestHTTPTransportPreHook_ListAllModelsScopesProvidersToVirtualKey verifies that a
// bodyless GET /v1/models with a virtual key sets BifrostContextKeyAvailableProviders
// to the VK's configured providers, so core.ListAllModels only fans out to those.
func TestHTTPTransportPreHook_ListAllModelsScopesProvidersToVirtualKey(t *testing.T) {
	logger := NewMockLogger()

	virtualKey := buildVirtualKeyWithProviders(
		"vk-list-models",
		"sk-bf-list-models",
		"list-models-vk",
		[]configstoreTables.TableVirtualKeyProviderConfig{
			buildProviderConfig("openai", []string{"*"}),
			buildProviderConfig("anthropic", []string{"*"}),
		},
	)
	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys: []configstoreTables.TableVirtualKey{*virtualKey},
	}, nil)
	require.NoError(t, err)

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, nil, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "GET"
	req.Path = "/v1/models"
	req.Headers["Authorization"] = "Bearer sk-bf-list-models"

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bfCtx.SetValue(schemas.BifrostContextKeyHTTPRequestType, schemas.ListModelsRequest)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	allowedProviders, ok := bfCtx.Value(schemas.BifrostContextKeyAvailableProviders).([]schemas.ModelProvider)
	require.True(t, ok, "list models should scope available providers to the VK")
	require.Equal(t, []schemas.ModelProvider{schemas.OpenAI, schemas.Anthropic}, allowedProviders)
}

// TestHTTPTransportPreHook_ListAllModelsInactiveVirtualKeySkipsScoping verifies that an
// inactive VK is ignored: no provider scoping is applied so the call is not silently
// reduced to zero models by an unrelated deny-all.
func TestHTTPTransportPreHook_ListAllModelsInactiveVirtualKeySkipsScoping(t *testing.T) {
	logger := NewMockLogger()

	virtualKey := buildVirtualKey("vk-list-models-inactive", "sk-bf-list-models-inactive", "list-models-inactive-vk", false)
	virtualKey.ProviderConfigs = []configstoreTables.TableVirtualKeyProviderConfig{
		buildProviderConfig("openai", []string{"*"}),
	}
	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys: []configstoreTables.TableVirtualKey{*virtualKey},
	}, nil)
	require.NoError(t, err)

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, nil, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "GET"
	req.Path = "/v1/models"
	req.Headers["Authorization"] = "Bearer sk-bf-list-models-inactive"

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bfCtx.SetValue(schemas.BifrostContextKeyHTTPRequestType, schemas.ListModelsRequest)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	require.Nil(t, bfCtx.Value(schemas.BifrostContextKeyAvailableProviders),
		"inactive VK must not set available providers")
}

// TestHTTPTransportPreHook_ListAllModelsUnknownVirtualKeySkipsScoping verifies that a bearer
// token absent from the governance store does not set BifrostContextKeyAvailableProviders,
// so ListAllModels fans out to all configured providers (fail-open) rather than returning
// zero results.
func TestHTTPTransportPreHook_ListAllModelsUnknownVirtualKeySkipsScoping(t *testing.T) {
	logger := NewMockLogger()

	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{}, nil)
	require.NoError(t, err)

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, nil, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "GET"
	req.Path = "/v1/models"
	req.Headers["Authorization"] = "Bearer sk-bf-not-in-store"

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	bfCtx.SetValue(schemas.BifrostContextKeyHTTPRequestType, schemas.ListModelsRequest)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	require.Nil(t, bfCtx.Value(schemas.BifrostContextKeyAvailableProviders),
		"unknown VK must not set available providers — fan out to all providers")
}

// TestHTTPTransportPreHook_GenAIRoutingRulePreservesTarget verifies that when a routing rule
// matches on the /genai path, governance load balancing does not override the routing-rule target
// with a provider from the VK pool (regression test for issue #2516).
func TestHTTPTransportPreHook_GenAIRoutingRulePreservesTarget(t *testing.T) {
	logger := NewMockLogger()

	routingRule := configstoreTables.TableRoutingRule{
		ID:            "rule-genai-1",
		Name:          "genai-repro-rule",
		Enabled:       bifrost.Ptr(true),
		CelExpression: `model == "probe-genai-model" && provider == ""`,
		Targets: []configstoreTables.TableRoutingTarget{
			{
				RuleID:   "rule-genai-1",
				Provider: bifrost.Ptr("repro-openai-a"),
				Model:    bifrost.Ptr("error-test"),
				Weight:   1.0,
			},
		},
		Scope:    "global",
		Priority: 1,
	}

	// VK with repro-openai-b at weight=1 — this is what governance LB would wrongly select without the fix
	virtualKey := buildVirtualKeyWithProviders(
		"vk-genai",
		"sk-bf-genai-test",
		"genai-repro-vk",
		[]configstoreTables.TableVirtualKeyProviderConfig{
			buildProviderConfig("repro-openai-b", []string{"*"}),
		},
	)

	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys:  []configstoreTables.TableVirtualKey{*virtualKey},
		RoutingRules: []configstoreTables.TableRoutingRule{routingRule},
	}, nil)
	require.NoError(t, err)

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, nil, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "POST"
	req.Path = "/genai/v1beta/models/probe-genai-model:generateContent"
	req.PathParams["model"] = "probe-genai-model:generateContent"
	req.Headers["Authorization"] = "Bearer sk-bf-genai-test"
	req.Headers["Content-Type"] = "application/json"
	req.Body = []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	// Routing rule matched and set context model to "repro-openai-a/error-test:generateContent".
	// Governance LB must NOT override this with "repro-openai-b/probe-genai-model:generateContent".
	ctxModel, ok := bfCtx.Value("model").(string)
	require.True(t, ok, "context model should be set")
	require.Equal(t, "repro-openai-a/error-test:generateContent", ctxModel)
}

// TestHTTPTransportPreHook_GenAIRoutingRulePreservesTarget_WithStore is a production-like variant
// of TestHTTPTransportPreHook_GenAIRoutingRulePreservesTarget that passes a non-nil inMemoryStore
// containing the routing-rule provider, confirming the fix holds when p.inMemoryStore != nil
// and the provider IS present in GetConfiguredProviders (the normal production code path).
func TestHTTPTransportPreHook_GenAIRoutingRulePreservesTarget_WithStore(t *testing.T) {
	logger := NewMockLogger()

	routingRule := configstoreTables.TableRoutingRule{
		ID:            "rule-genai-ws-1",
		Name:          "genai-repro-rule-with-store",
		Enabled:       bifrost.Ptr(true),
		CelExpression: `model == "probe-genai-model" && provider == ""`,
		Targets: []configstoreTables.TableRoutingTarget{
			{
				RuleID:   "rule-genai-ws-1",
				Provider: bifrost.Ptr("repro-openai-a"),
				Model:    bifrost.Ptr("error-test"),
				Weight:   1.0,
			},
		},
		Scope:    "global",
		Priority: 1,
	}

	virtualKey := buildVirtualKeyWithProviders(
		"vk-genai-ws",
		"sk-bf-genai-ws-test",
		"genai-repro-vk-with-store",
		[]configstoreTables.TableVirtualKeyProviderConfig{
			buildProviderConfig("repro-openai-b", []string{"*"}),
		},
	)

	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys:  []configstoreTables.TableVirtualKey{*virtualKey},
		RoutingRules: []configstoreTables.TableRoutingRule{routingRule},
	}, nil)
	require.NoError(t, err)

	// Register the fake provider so ParseModelString can split "repro-openai-a/model"
	// the same way it would for a real provider in production.
	schemas.RegisterKnownProvider("repro-openai-a")
	t.Cleanup(func() { schemas.UnregisterKnownProvider("repro-openai-a") })

	// Use a non-nil inMemoryStore that recognises the routing-rule provider,
	// mirroring production where configured providers are always registered in the store.
	inMemStore := &mockInMemoryStore{
		configuredProviders: map[schemas.ModelProvider]configstore.ProviderConfig{
			"repro-openai-a": {},
		},
	}

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, nil, nil, inMemStore)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "POST"
	req.Path = "/genai/v1beta/models/probe-genai-model:generateContent"
	req.PathParams["model"] = "probe-genai-model:generateContent"
	req.Headers["Authorization"] = "Bearer sk-bf-genai-ws-test"
	req.Headers["Content-Type"] = "application/json"
	req.Body = []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	ctxModel, ok := bfCtx.Value("model").(string)
	require.True(t, ok, "context model should be set")
	require.Equal(t, "repro-openai-a/error-test:generateContent", ctxModel)
}

// TestHTTPTransportPreHook_GenAINoRoutingRuleStillLoadBalances verifies that when no routing rule
// matches on the /genai path, governance load balancing still selects a provider from the VK pool.
func TestHTTPTransportPreHook_GenAINoRoutingRuleStillLoadBalances(t *testing.T) {
	logger := NewMockLogger()

	// VK with repro-openai-b at weight=1 — LB should select this
	virtualKey := buildVirtualKeyWithProviders(
		"vk-genai-lb",
		"sk-bf-genai-lb-test",
		"genai-lb-vk",
		[]configstoreTables.TableVirtualKeyProviderConfig{
			buildProviderConfig("repro-openai-b", []string{"*"}),
		},
	)

	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys: []configstoreTables.TableVirtualKey{*virtualKey},
		// No routing rules — governance LB should run normally
	}, nil)
	require.NoError(t, err)

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, nil, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "POST"
	req.Path = "/genai/v1beta/models/probe-genai-model:generateContent"
	req.PathParams["model"] = "probe-genai-model:generateContent"
	req.Headers["Authorization"] = "Bearer sk-bf-genai-lb-test"
	req.Headers["Content-Type"] = "application/json"
	req.Body = []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	// No routing rule: governance LB must still run and select repro-openai-b from the VK pool
	ctxModel, ok := bfCtx.Value("model").(string)
	require.True(t, ok, "context model should be set by governance LB")
	require.Equal(t, "repro-openai-b/probe-genai-model:generateContent", ctxModel)
}

// TestHTTPTransportPreHook_BedrockRoutingRulePreservesTarget verifies that when a routing rule
// matches on the /bedrock path, governance load balancing does not override the routing-rule target
// (regression test mirroring the GenAI fix for the Bedrock integration).
func TestHTTPTransportPreHook_BedrockRoutingRulePreservesTarget(t *testing.T) {
	logger := NewMockLogger()

	routingRule := configstoreTables.TableRoutingRule{
		ID:            "rule-bedrock-1",
		Name:          "bedrock-repro-rule",
		Enabled:       bifrost.Ptr(true),
		CelExpression: `model == "probe-bedrock-model" && provider == ""`,
		Targets: []configstoreTables.TableRoutingTarget{
			{
				RuleID:   "rule-bedrock-1",
				Provider: bifrost.Ptr("repro-openai-a"),
				Model:    bifrost.Ptr("error-test"),
				Weight:   1.0,
			},
		},
		Scope:    "global",
		Priority: 1,
	}

	// VK with repro-openai-b at weight=1 — this is what governance LB would wrongly select without the fix
	virtualKey := buildVirtualKeyWithProviders(
		"vk-bedrock",
		"sk-bf-bedrock-test",
		"bedrock-repro-vk",
		[]configstoreTables.TableVirtualKeyProviderConfig{
			buildProviderConfig("repro-openai-b", []string{"*"}),
		},
	)

	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys:  []configstoreTables.TableVirtualKey{*virtualKey},
		RoutingRules: []configstoreTables.TableRoutingRule{routingRule},
	}, nil)
	require.NoError(t, err)

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, nil, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "POST"
	req.Path = "/bedrock/model/probe-bedrock-model/converse"
	req.PathParams["modelId"] = "probe-bedrock-model"
	req.Headers["Authorization"] = "Bearer sk-bf-bedrock-test"
	req.Headers["Content-Type"] = "application/json"
	req.Body = []byte(`{"messages":[{"role":"user","content":[{"text":"hi"}]}]}`)

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	// Routing rule matched and set context modelId to "repro-openai-a/error-test".
	// Governance LB must NOT override this with "repro-openai-b/probe-bedrock-model".
	ctxModelID, ok := bfCtx.Value("modelId").(string)
	require.True(t, ok, "context modelId should be set")
	require.Equal(t, "repro-openai-a/error-test", ctxModelID)
}

// TestHTTPTransportPreHook_BedrockNoRoutingRuleStillLoadBalances verifies that when no routing rule
// matches on the /bedrock path, governance load balancing still selects a provider from the VK pool.
func TestHTTPTransportPreHook_BedrockNoRoutingRuleStillLoadBalances(t *testing.T) {
	logger := NewMockLogger()

	// VK with repro-openai-b at weight=1 — LB should select this
	virtualKey := buildVirtualKeyWithProviders(
		"vk-bedrock-lb",
		"sk-bf-bedrock-lb-test",
		"bedrock-lb-vk",
		[]configstoreTables.TableVirtualKeyProviderConfig{
			buildProviderConfig("repro-openai-b", []string{"*"}),
		},
	)

	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys: []configstoreTables.TableVirtualKey{*virtualKey},
		// No routing rules — governance LB should run normally
	}, nil)
	require.NoError(t, err)

	plugin, err := InitFromStore(context.Background(), &Config{IsVkMandatory: boolPtr(false)}, logger, store, nil, nil, nil, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, plugin.Cleanup())
	}()

	req := schemas.AcquireHTTPRequest()
	defer schemas.ReleaseHTTPRequest(req)
	req.Method = "POST"
	req.Path = "/bedrock/model/probe-bedrock-model/converse"
	req.PathParams["modelId"] = "probe-bedrock-model"
	req.Headers["Authorization"] = "Bearer sk-bf-bedrock-lb-test"
	req.Headers["Content-Type"] = "application/json"
	req.Body = []byte(`{"messages":[{"role":"user","content":[{"text":"hi"}]}]}`)

	bfCtx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	resp, err := plugin.HTTPTransportPreHook(bfCtx, req)
	require.NoError(t, err)
	require.Nil(t, resp)

	// No routing rule: governance LB must still run and select repro-openai-b from the VK pool
	ctxModelID, ok := bfCtx.Value("modelId").(string)
	require.True(t, ok, "context modelId should be set by governance LB")
	require.Equal(t, "repro-openai-b/probe-bedrock-model", ctxModelID)
}
