package operation_setting

import "testing"

func TestParamPreflightEvaluateMatchedRule(t *testing.T) {
	raw := `{
		"enabled": true,
		"default_status_code": 400,
		"groups": [{
			"name": "gpt-5.4-chat",
			"models": ["gpt-5.4"],
			"model_match": "prefix",
			"paths": ["/chat/completions"],
			"path_match": "contains",
			"rules": [{
				"name": "max-tokens",
				"status_code": 422,
				"message": "use max_completion_tokens",
				"conditions": [{"path": "max_tokens", "mode": "exists"}]
			}]
		}]
	}`
	if err := UpdateParamPreflightInterceptionRules(raw); err != nil {
		t.Fatalf("UpdateParamPreflightInterceptionRules() error = %v", err)
	}
	ctx := map[string]interface{}{
		"model":        "gpt-5.4-2026-03-05",
		"request_path": "/v1/chat/completions",
	}
	candidates := GetParamPreflightCandidateGroups(ctx)
	if len(candidates) != 1 {
		t.Fatalf("candidate groups = %v, want 1 group", candidates)
	}
	result, err := EvaluateParamPreflight([]byte(`{"model":"gpt-5.4-2026-03-05","max_tokens":100}`), ctx, candidates)
	if err != nil {
		t.Fatalf("EvaluateParamPreflight() error = %v", err)
	}
	if result == nil || result.Group != "gpt-5.4-chat" || result.Rule != "max-tokens" || result.StatusCode != 422 {
		t.Fatalf("result = %#v, want matched rule", result)
	}
}

func TestParamPreflightSkipsUnmatchedGroup(t *testing.T) {
	raw := `{"enabled":true,"groups":[{"name":"gpt","models":["gpt-"],"model_match":"prefix","rules":[{"name":"max","message":"blocked","conditions":[{"path":"max_tokens","mode":"exists"}]}]}]}`
	if err := UpdateParamPreflightInterceptionRules(raw); err != nil {
		t.Fatalf("UpdateParamPreflightInterceptionRules() error = %v", err)
	}
	ctx := map[string]interface{}{"model": "gemini-2.5-pro", "request_path": "/v1beta/models/gemini-2.5-pro:generateContent"}
	if candidates := GetParamPreflightCandidateGroups(ctx); len(candidates) != 0 {
		t.Fatalf("candidate groups = %v, want none", candidates)
	}
}

func TestParamPreflightExactAwsClaudeToolPairConfig(t *testing.T) {
	raw := `{
		"default_status_code": 400,
		"groups": [{
			"name": "claude-proxy-tool-pair",
			"models": [
				"vertex_ai.claude-opus-4-6",
				"vertex_ai.claude-opus-4-7",
				"aws.claude-opus-4-6",
				"aws.claude-opus-4-7"
			],
			"model_match": "prefix",
			"paths": ["/v1/messages"],
			"path_match": "contains",
			"rules": [{
				"name": "invalid-tool-pair",
				"message": "tool_use 必须在下一条消息中提供对应的 tool_result",
				"conditions": [{
					"path": "messages",
					"mode": "claude_tool_pair_invalid"
				}],
				"logic": "AND"
			}]
		}]
	}`
	if err := UpdateParamPreflightInterceptionRules(raw); err != nil {
		t.Fatalf("UpdateParamPreflightInterceptionRules() error = %v", err)
	}
	ctx := map[string]interface{}{
		"model":        "aws.claude-opus-4-7",
		"request_path": "/v1/messages",
	}
	candidates := GetParamPreflightCandidateGroups(ctx)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %v, want one group", candidates)
	}
	body := []byte(`{
		"anthropic_version": "2023-06-01",
		"model": "aws.claude-opus-4-7",
		"max_tokens": 128,
		"messages": [{
			"role": "assistant",
			"content": [{
				"type": "tool_use",
				"id": "toolu_1",
				"name": "read",
				"input": {}
			}]
		}]
	}`)
	result, err := EvaluateParamPreflight(body, ctx, candidates)
	if err != nil {
		t.Fatalf("EvaluateParamPreflight() error = %v", err)
	}
	if result == nil || result.Rule != "invalid-tool-pair" {
		t.Fatalf("result = %#v, want invalid-tool-pair", result)
	}
}

func TestParamPreflightMissingCondition(t *testing.T) {
	raw := `{"enabled":true,"groups":[{"name":"claude","models":["claude-"],"model_match":"prefix","rules":[{"name":"missing-max","message":"max_tokens required","conditions":[{"path":"max_tokens","mode":"missing"}]}]}]}`
	if err := UpdateParamPreflightInterceptionRules(raw); err != nil {
		t.Fatalf("UpdateParamPreflightInterceptionRules() error = %v", err)
	}
	ctx := map[string]interface{}{"model": "claude-haiku-4-5-20251001"}
	candidates := GetParamPreflightCandidateGroups(ctx)
	result, err := EvaluateParamPreflight([]byte(`{"model":"claude-haiku-4-5-20251001"}`), ctx, candidates)
	if err != nil {
		t.Fatalf("EvaluateParamPreflight() error = %v", err)
	}
	if result == nil || result.Rule != "missing-max" {
		t.Fatalf("result = %#v, want missing-max", result)
	}
}

func TestParamPreflightTrimEmptyConditionMatchesArrayValue(t *testing.T) {
	raw := `{"groups":[{"name":"claude-empty-text","models":["claude-"],"model_match":"prefix","rules":[{"name":"empty-text-content-block","message":"messages: text content blocks must be non-empty","conditions":[{"path":"messages.#.content.#(type==\"text\")#.text","mode":"trim_empty"}]}]}]}`
	if err := UpdateParamPreflightInterceptionRules(raw); err != nil {
		t.Fatalf("UpdateParamPreflightInterceptionRules() error = %v", err)
	}
	ctx := map[string]interface{}{"model": "claude-opus-4-7"}
	candidates := GetParamPreflightCandidateGroups(ctx)
	result, err := EvaluateParamPreflight([]byte(`{
		"model": "claude-opus-4-7",
		"messages": [{
			"role": "user",
			"content": [
				{"type": "text", "text": "你好"},
				{"type": "text", "text": "   "}
			]
		}]
	}`), ctx, candidates)
	if err != nil {
		t.Fatalf("EvaluateParamPreflight() error = %v", err)
	}
	if result == nil || result.Rule != "empty-text-content-block" {
		t.Fatalf("result = %#v, want empty-text-content-block", result)
	}
}

func TestParamPreflightValuePathComparison(t *testing.T) {
	raw := `{"groups":[{"name":"claude-thinking-budget","models":["claude-opus-4-6"],"model_match":"prefix","rules":[{"name":"max-tokens-must-exceed-thinking-budget","message":"max_tokens must be greater than thinking.budget_tokens","conditions":[{"path":"thinking.type","mode":"full","value":"enabled"},{"path":"max_tokens","mode":"lte","value_path":"thinking.budget_tokens"}],"logic":"AND"}]}]}`
	if err := UpdateParamPreflightInterceptionRules(raw); err != nil {
		t.Fatalf("UpdateParamPreflightInterceptionRules() error = %v", err)
	}
	ctx := map[string]interface{}{"model": "claude-opus-4-6"}
	candidates := GetParamPreflightCandidateGroups(ctx)
	result, err := EvaluateParamPreflight([]byte(`{
		"model": "claude-opus-4-6",
		"max_tokens": 1024,
		"thinking": {
			"type": "enabled",
			"budget_tokens": 1280
		}
	}`), ctx, candidates)
	if err != nil {
		t.Fatalf("EvaluateParamPreflight() error = %v", err)
	}
	if result == nil || result.Rule != "max-tokens-must-exceed-thinking-budget" {
		t.Fatalf("result = %#v, want max-tokens-must-exceed-thinking-budget", result)
	}

	result, err = EvaluateParamPreflight([]byte(`{
		"model": "claude-opus-4-6",
		"max_tokens": 2048,
		"thinking": {
			"type": "enabled",
			"budget_tokens": 1280
		}
	}`), ctx, candidates)
	if err != nil {
		t.Fatalf("EvaluateParamPreflight() error = %v", err)
	}
	if result != nil {
		t.Fatalf("result = %#v, want no match", result)
	}
}

func TestParamPreflightClaudeToolPairInvalid(t *testing.T) {
	raw := `{
		"groups": [{
			"name": "claude-tool-pairs",
			"models": ["claude-"],
			"model_match": "prefix",
			"rules": [{
				"name": "invalid-tool-pair",
				"message": "tool_use must be followed by matching tool_result blocks",
				"conditions": [{
					"path": "messages",
					"mode": "claude_tool_pair_invalid"
				}]
			}]
		}]
	}`
	if err := UpdateParamPreflightInterceptionRules(raw); err != nil {
		t.Fatalf("UpdateParamPreflightInterceptionRules() error = %v", err)
	}
	ctx := map[string]interface{}{"model": "claude-opus-4-7"}
	candidates := GetParamPreflightCandidateGroups(ctx)

	tests := []struct {
		name      string
		body      string
		wantMatch bool
	}{
		{
			name: "missing next result message",
			body: `{"messages":[
				{"role":"user","content":"read a.txt"},
				{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"read","input":{}}]}
			]}`,
			wantMatch: true,
		},
		{
			name: "mismatched result id",
			body: `{"messages":[
				{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"read","input":{}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"ok"}]}
			]}`,
			wantMatch: true,
		},
		{
			name: "text before result",
			body: `{"messages":[
				{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"read","input":{}}]},
				{"role":"user","content":[
					{"type":"text","text":"done"},
					{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}
				]}
			]}`,
			wantMatch: true,
		},
		{
			name: "parallel result missing",
			body: `{"messages":[
				{"role":"assistant","content":[
					{"type":"tool_use","id":"toolu_1","name":"read","input":{}},
					{"type":"tool_use","id":"toolu_2","name":"read","input":{}}
				]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}
			]}`,
			wantMatch: true,
		},
		{
			name: "consecutive user messages are merged",
			body: `{"messages":[
				{"role":"assistant","content":[
					{"type":"tool_use","id":"toolu_1","name":"read","input":{}},
					{"type":"tool_use","id":"toolu_2","name":"read","input":{}}
				]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"a"}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"b"}]}
			]}`,
			wantMatch: false,
		},
		{
			name: "valid pair with text after results",
			body: `{"messages":[
				{"role":"assistant","content":[
					{"type":"tool_use","id":"toolu_1","name":"read","input":{}},
					{"type":"tool_use","id":"toolu_2","name":"read","input":{}}
				]},
				{"role":"user","content":[
					{"type":"tool_result","tool_use_id":"toolu_2","content":"b"},
					{"type":"tool_result","tool_use_id":"toolu_1","content":"a"},
					{"type":"text","text":"continue"}
				]}
			]}`,
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EvaluateParamPreflight([]byte(tt.body), ctx, candidates)
			if err != nil {
				t.Fatalf("EvaluateParamPreflight() error = %v", err)
			}
			if (result != nil) != tt.wantMatch {
				t.Fatalf("result = %#v, wantMatch = %v", result, tt.wantMatch)
			}
		})
	}
}

func TestParamPreflightInAndNotIn(t *testing.T) {
	raw := `{
		"groups": [{
			"name": "in-not-in-test",
			"models": ["gpt-"],
			"model_match": "prefix",
			"rules": [
				{
					"name": "check-in",
					"message": "model must be in whitelist",
					"conditions": [{
						"path": "model",
						"mode": "not_in",
						"value": ["gpt-4", "gpt-4o"]
					}]
				}
			]
		}]
	}`
	if err := UpdateParamPreflightInterceptionRules(raw); err != nil {
		t.Fatalf("UpdateParamPreflightInterceptionRules() error = %v", err)
	}
	ctx := map[string]interface{}{"model": "gpt-4o-mini"}
	candidates := GetParamPreflightCandidateGroups(ctx)
	
	// Should match because gpt-4o-mini is not in [gpt-4, gpt-4o]
	result, err := EvaluateParamPreflight([]byte(`{"model":"gpt-4o-mini"}`), ctx, candidates)
	if err != nil {
		t.Fatalf("EvaluateParamPreflight() error = %v", err)
	}
	if result == nil || result.Rule != "check-in" {
		t.Fatalf("result = %#v, want match check-in", result)
	}

	// Should not match because gpt-4o is in [gpt-4, gpt-4o]
	result, err = EvaluateParamPreflight([]byte(`{"model":"gpt-4o"}`), ctx, candidates)
	if err != nil {
		t.Fatalf("EvaluateParamPreflight() error = %v", err)
	}
	if result != nil {
		t.Fatalf("result = %#v, want no match", result)
	}
}
