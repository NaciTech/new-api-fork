package operation_setting

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	DefaultParamPreflightInterceptionRules = ""
	ParamPreflightContextKey               = "param_preflight_intercept"
)

type ParamPreflightConfig struct {
	DefaultStatusCode int                   `json:"default_status_code,omitempty"`
	Groups            []ParamPreflightGroup `json:"groups,omitempty"`
}

type ParamPreflightGroup struct {
	Name         string               `json:"name,omitempty"`
	Models       []string             `json:"models,omitempty"`
	ModelMatch   string               `json:"model_match,omitempty"`
	Paths        []string             `json:"paths,omitempty"`
	PathMatch    string               `json:"path_match,omitempty"`
	TokenGroups  []string             `json:"token_groups,omitempty"`
	ChannelTypes []int                `json:"channel_types,omitempty"`
	ChannelIds   []int                `json:"channel_ids,omitempty"`
	ChannelTags  []string             `json:"channel_tags,omitempty"`
	StatusCode   int                  `json:"status_code,omitempty"`
	Rules        []ParamPreflightRule `json:"rules,omitempty"`
}

type ParamPreflightRule struct {
	Name       string                    `json:"name,omitempty"`
	StatusCode int                       `json:"status_code,omitempty"`
	Message    interface{}               `json:"message,omitempty"`
	Conditions []ParamPreflightCondition `json:"conditions,omitempty"`
	Logic      string                    `json:"logic,omitempty"`
}

type ParamPreflightCondition struct {
	Path           string      `json:"path"`
	Mode           string      `json:"mode"`
	Value          interface{} `json:"value,omitempty"`
	ValuePath      string      `json:"value_path,omitempty"`
	Invert         bool        `json:"invert,omitempty"`
	PassMissingKey bool        `json:"pass_missing_key,omitempty"`
}

type ParamPreflightResult struct {
	Group      string `json:"group"`
	Rule       string `json:"rule"`
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
}

var paramPreflightRaw atomic.Value
var paramPreflightParsed atomic.Value

func init() {
	_ = UpdateParamPreflightInterceptionRules(DefaultParamPreflightInterceptionRules)
}

func ParamPreflightInterceptionRulesToString() string {
	if value, ok := paramPreflightRaw.Load().(string); ok {
		return value
	}
	return DefaultParamPreflightInterceptionRules
}

func UpdateParamPreflightInterceptionRules(raw string) error {
	cfg, normalized, err := ParseParamPreflightInterceptionRules(raw)
	if err != nil {
		return err
	}
	paramPreflightRaw.Store(normalized)
	paramPreflightParsed.Store(cfg)
	return nil
}

func ParseParamPreflightInterceptionRules(raw string) (*ParamPreflightConfig, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return &ParamPreflightConfig{DefaultStatusCode: http.StatusBadRequest}, "", nil
	}
	var cfg ParamPreflightConfig
	if err := common.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, "", fmt.Errorf("参数前置拦截规则必须是合法 JSON: %w", err)
	}
	if cfg.DefaultStatusCode == 0 {
		cfg.DefaultStatusCode = http.StatusBadRequest
	}
	if !validPreflightStatusCode(cfg.DefaultStatusCode) {
		return nil, "", fmt.Errorf("default_status_code 必须是 400-599 之间的整数")
	}
	for groupIndex, group := range cfg.Groups {
		if group.StatusCode != 0 && !validPreflightStatusCode(group.StatusCode) {
			return nil, "", fmt.Errorf("groups[%d].status_code 必须是 400-599 之间的整数", groupIndex)
		}
		if err := validateMatchMode(group.ModelMatch, "model_match"); err != nil {
			return nil, "", err
		}
		if err := validateMatchMode(group.PathMatch, "path_match"); err != nil {
			return nil, "", err
		}
		for ruleIndex, rule := range group.Rules {
			if rule.StatusCode != 0 && !validPreflightStatusCode(rule.StatusCode) {
				return nil, "", fmt.Errorf("groups[%d].rules[%d].status_code 必须是 400-599 之间的整数", groupIndex, ruleIndex)
			}
			if !validParamPreflightMessage(rule.Message) {
				return nil, "", fmt.Errorf("groups[%d].rules[%d].message 不能为空", groupIndex, ruleIndex)
			}
			if len(rule.Conditions) == 0 {
				return nil, "", fmt.Errorf("groups[%d].rules[%d].conditions 不能为空", groupIndex, ruleIndex)
			}
			logic := strings.ToUpper(strings.TrimSpace(rule.Logic))
			if logic != "" && logic != "AND" && logic != "OR" {
				return nil, "", fmt.Errorf("groups[%d].rules[%d].logic 仅支持 AND 或 OR", groupIndex, ruleIndex)
			}
			for conditionIndex, condition := range rule.Conditions {
				if strings.TrimSpace(condition.Path) == "" {
					return nil, "", fmt.Errorf("groups[%d].rules[%d].conditions[%d].path 不能为空", groupIndex, ruleIndex, conditionIndex)
				}
				if err := validateConditionMode(condition.Mode); err != nil {
					return nil, "", fmt.Errorf("groups[%d].rules[%d].conditions[%d]: %w", groupIndex, ruleIndex, conditionIndex, err)
				}
				if err := validateConditionValue(condition); err != nil {
					return nil, "", fmt.Errorf("groups[%d].rules[%d].conditions[%d]: %w", groupIndex, ruleIndex, conditionIndex, err)
				}
			}
		}
	}
	bytes, err := common.Marshal(cfg)
	if err != nil {
		return nil, "", err
	}
	return &cfg, string(bytes), nil
}

func GetParamPreflightCandidateGroups(ctx map[string]interface{}) []int {
	cfg, _ := paramPreflightParsed.Load().(*ParamPreflightConfig)
	if cfg == nil || len(cfg.Groups) == 0 {
		return nil
	}
	candidates := make([]int, 0, len(cfg.Groups))
	for i := range cfg.Groups {
		if matchParamPreflightGroup(cfg.Groups[i], ctx) {
			candidates = append(candidates, i)
		}
	}
	return candidates
}

func EvaluateParamPreflight(body []byte, ctx map[string]interface{}, candidateGroups []int) (*ParamPreflightResult, error) {
	cfg, _ := paramPreflightParsed.Load().(*ParamPreflightConfig)
	if cfg == nil || len(candidateGroups) == 0 {
		return nil, nil
	}
	if !gjson.ValidBytes(body) {
		return nil, nil
	}
	contextJSON, err := common.Marshal(ctx)
	if err != nil {
		return nil, err
	}
	jsonStr := string(body)
	contextStr := string(contextJSON)
	for _, groupIndex := range candidateGroups {
		if groupIndex < 0 || groupIndex >= len(cfg.Groups) {
			continue
		}
		group := cfg.Groups[groupIndex]
		for _, rule := range group.Rules {
			matched, messageIndex, err := matchParamPreflightRule(jsonStr, contextStr, rule)
			if err != nil {
				return nil, err
			}
			if !matched {
				continue
			}
			statusCode := cfg.DefaultStatusCode
			if group.StatusCode != 0 {
				statusCode = group.StatusCode
			}
			if rule.StatusCode != 0 {
				statusCode = rule.StatusCode
			}
			return &ParamPreflightResult{
				Group:      group.Name,
				Rule:       rule.Name,
				StatusCode: statusCode,
				Message:    resolveParamPreflightMessage(rule.Message, messageIndex),
			}, nil
		}
	}
	return nil, nil
}

func SetParamPreflightResult(c *gin.Context, result *ParamPreflightResult) {
	if c != nil && result != nil {
		c.Set(ParamPreflightContextKey, result)
	}
}

func GetParamPreflightResult(c *gin.Context) (*ParamPreflightResult, bool) {
	if c == nil {
		return nil, false
	}
	result, ok := c.Get(ParamPreflightContextKey)
	if !ok {
		return nil, false
	}
	typed, ok := result.(*ParamPreflightResult)
	return typed, ok && typed != nil
}

func matchParamPreflightGroup(group ParamPreflightGroup, ctx map[string]interface{}) bool {
	model := getContextString(ctx, "upstream_model")
	if model == "" {
		model = getContextString(ctx, "model")
	}
	if !matchStringList(model, group.Models, group.ModelMatch) {
		return false
	}
	if !matchStringList(getContextString(ctx, "request_path"), group.Paths, group.PathMatch) {
		return false
	}
	if !matchStringList(getContextString(ctx, "token_group"), group.TokenGroups, "full") {
		return false
	}
	if !matchIntList(getContextInt(ctx, "channel_type"), group.ChannelTypes) {
		return false
	}
	if !matchIntList(getContextInt(ctx, "channel_id"), group.ChannelIds) {
		return false
	}
	if !matchStringList(getContextString(ctx, "channel_tag"), group.ChannelTags, "full") {
		return false
	}
	return true
}

func matchParamPreflightRule(jsonStr string, contextJSON string, rule ParamPreflightRule) (bool, int, error) {
	logic := strings.ToUpper(strings.TrimSpace(rule.Logic))
	if logic == "" {
		logic = "AND"
	}
	if logic == "OR" {
		for _, condition := range rule.Conditions {
			ok, messageIndex, err := matchParamPreflightCondition(jsonStr, contextJSON, condition)
			if err != nil {
				return false, -1, err
			}
			if ok {
				return true, messageIndex, nil
			}
		}
		return false, -1, nil
	}
	messageIndex := -1
	for _, condition := range rule.Conditions {
		ok, conditionMessageIndex, err := matchParamPreflightCondition(jsonStr, contextJSON, condition)
		if err != nil {
			return false, -1, err
		}
		if !ok {
			return false, -1, nil
		}
		if conditionMessageIndex >= 0 {
			messageIndex = conditionMessageIndex
		}
	}
	return true, messageIndex, nil
}

func matchParamPreflightCondition(jsonStr string, contextJSON string, condition ParamPreflightCondition) (bool, int, error) {
	path := strings.TrimSpace(condition.Path)
	mode := strings.ToLower(strings.TrimSpace(condition.Mode))
	if mode == "claude_tool_pair_invalid" {
		messageIndex := invalidClaudeToolPairMessageIndex(gjson.Get(jsonStr, path))
		result := messageIndex >= 0
		if condition.Invert {
			result = !result
			messageIndex = -1
		}
		return result, messageIndex, nil
	}
	value := gjson.Get(jsonStr, path)
	if !isParamPreflightValuePresent(path, value) && contextJSON != "" {
		value = gjson.Get(contextJSON, path)
	}
	valuePresent := isParamPreflightValuePresent(path, value)
	var result bool
	switch mode {
	case "exists":
		result = valuePresent
	case "missing":
		result = !valuePresent
	case "trim_empty":
		result = valuePresent && isTrimEmptyPreflightValue(value)
	default:
		if !valuePresent {
			result = condition.PassMissingKey
			break
		}
		target, err := getParamPreflightTargetValue(jsonStr, contextJSON, condition)
		if err != nil {
			return false, -1, err
		}
		if !target.Exists() {
			result = false
			break
		}
		if (mode == "in" || mode == "not_in") && !target.IsArray() {
			return false, -1, fmt.Errorf("mode 为 %s 时比较值必须是数组", mode)
		}
		result = comparePreflightValue(value, target, mode)
	}
	if condition.Invert {
		result = !result
	}
	return result, -1, nil
}

func getParamPreflightTargetValue(jsonStr string, contextJSON string, condition ParamPreflightCondition) (gjson.Result, error) {
	valuePath := strings.TrimSpace(condition.ValuePath)
	if valuePath != "" {
		target := gjson.Get(jsonStr, valuePath)
		if !target.Exists() && contextJSON != "" {
			target = gjson.Get(contextJSON, valuePath)
		}
		return target, nil
	}
	targetBytes, err := common.Marshal(condition.Value)
	if err != nil {
		return gjson.Result{}, err
	}
	return gjson.ParseBytes(targetBytes), nil
}

func isTrimEmptyPreflightValue(value gjson.Result) bool {
	if value.IsArray() {
		matched := false
		value.ForEach(func(_, item gjson.Result) bool {
			if isTrimEmptyPreflightValue(item) {
				matched = true
				return false
			}
			return true
		})
		return matched
	}
	return strings.TrimSpace(value.String()) == ""
}

func comparePreflightValue(value gjson.Result, target gjson.Result, mode string) bool {
	switch mode {
	case "full":
		return value.String() == target.String()
	case "prefix":
		return strings.HasPrefix(value.String(), target.String())
	case "suffix":
		return strings.HasSuffix(value.String(), target.String())
	case "contains":
		return strings.Contains(value.String(), target.String())
	case "in", "not_in":
		matched := false
		if target.IsArray() {
			target.ForEach(func(_, item gjson.Result) bool {
				if comparePreflightValue(value, item, "full") {
					matched = true
					return false
				}
				return true
			})
		}
		if mode == "not_in" {
			return !matched
		}
		return matched
	case "gt":
		return value.Type == gjson.Number && target.Type == gjson.Number && value.Num > target.Num
	case "gte":
		return value.Type == gjson.Number && target.Type == gjson.Number && value.Num >= target.Num
	case "lt":
		return value.Type == gjson.Number && target.Type == gjson.Number && value.Num < target.Num
	case "lte":
		return value.Type == gjson.Number && target.Type == gjson.Number && value.Num <= target.Num
	default:
		return false
	}
}

func validPreflightStatusCode(statusCode int) bool {
	return statusCode >= http.StatusBadRequest && statusCode <= 599
}

func validateMatchMode(mode string, field string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "full" || mode == "prefix" || mode == "suffix" || mode == "contains" {
		return nil
	}
	return fmt.Errorf("%s 仅支持 full、prefix、suffix、contains", field)
}

func validateConditionMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "full", "prefix", "suffix", "contains", "in", "not_in", "gt", "gte", "lt", "lte", "exists", "missing", "trim_empty", "claude_tool_pair_invalid":
		return nil
	default:
		return fmt.Errorf("mode 仅支持 full、prefix、suffix、contains、in、not_in、gt、gte、lt、lte、exists、missing、trim_empty、claude_tool_pair_invalid")
	}
}

func matchStringList(value string, patterns []string, mode string) bool {
	if len(patterns) == 0 {
		return true
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "full"
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		switch mode {
		case "prefix":
			if strings.HasPrefix(value, pattern) {
				return true
			}
		case "suffix":
			if strings.HasSuffix(value, pattern) {
				return true
			}
		case "contains":
			if strings.Contains(value, pattern) {
				return true
			}
		default:
			if value == pattern {
				return true
			}
		}
	}
	return false
}

func matchIntList(value int, values []int) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func getContextString(ctx map[string]interface{}, key string) string {
	if ctx == nil {
		return ""
	}
	switch value := ctx[key].(type) {
	case nil:
		return ""
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

func getContextInt(ctx map[string]interface{}, key string) int {
	if ctx == nil {
		return 0
	}
	switch value := ctx[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

type claudeToolMessageGroup struct {
	role    string
	content []gjson.Result
}

const (
	claudeToolPairMessageMissingResult = iota
	claudeToolPairMessageUnexpectedResult
	claudeToolPairMessageResultNotFirst
	claudeToolPairMessageIncompleteResults
	claudeToolPairMessageEmptyToolUseID
	claudeToolPairMessageEmptyToolResultID
	claudeToolPairMessageDuplicateToolUseID
	claudeToolPairMessageDuplicateToolResultID
)

func invalidClaudeToolPairMessageIndex(messages gjson.Result) int {
	if !messages.Exists() || !messages.IsArray() {
		return -1
	}

	groups := make([]claudeToolMessageGroup, 0)
	for _, message := range messages.Array() {
		role := message.Get("role").String()
		if role == "" {
			continue
		}
		content := claudeContentBlocks(message.Get("content"))
		if len(groups) > 0 && groups[len(groups)-1].role == role {
			groups[len(groups)-1].content = append(groups[len(groups)-1].content, content...)
			continue
		}
		groups = append(groups, claudeToolMessageGroup{role: role, content: content})
	}

	for i, group := range groups {
		if group.role != "assistant" {
			continue
		}
		toolUseIDs, invalidIndex := collectClaudeToolUseIDs(group.content)
		if invalidIndex >= 0 {
			return invalidIndex
		}
		if len(toolUseIDs) == 0 {
			continue
		}
		if i+1 >= len(groups) || groups[i+1].role != "user" {
			return claudeToolPairMessageMissingResult
		}
		toolResultIDs, invalidIndex := collectLeadingClaudeToolResultIDs(groups[i+1].content)
		if invalidIndex >= 0 {
			return invalidIndex
		}
		if len(toolResultIDs) < len(toolUseIDs) {
			return claudeToolPairMessageIncompleteResults
		}
		if !sameStringSet(toolUseIDs, toolResultIDs) {
			return claudeToolPairMessageUnexpectedResult
		}
	}
	return -1
}

func claudeContentBlocks(content gjson.Result) []gjson.Result {
	if content.IsArray() {
		return content.Array()
	}
	if content.Exists() {
		return []gjson.Result{content}
	}
	return nil
}

func collectClaudeToolUseIDs(content []gjson.Result) (map[string]struct{}, int) {
	ids := make(map[string]struct{})
	for _, block := range content {
		if block.Get("type").String() != "tool_use" {
			continue
		}
		id := strings.TrimSpace(block.Get("id").String())
		if id == "" {
			return nil, claudeToolPairMessageEmptyToolUseID
		}
		if _, exists := ids[id]; exists {
			return nil, claudeToolPairMessageDuplicateToolUseID
		}
		ids[id] = struct{}{}
	}
	return ids, -1
}

func collectLeadingClaudeToolResultIDs(content []gjson.Result) (map[string]struct{}, int) {
	ids := make(map[string]struct{})
	seenNonToolResult := false
	for _, block := range content {
		if block.Get("type").String() != "tool_result" {
			seenNonToolResult = true
			continue
		}
		if seenNonToolResult {
			return nil, claudeToolPairMessageResultNotFirst
		}
		id := strings.TrimSpace(block.Get("tool_use_id").String())
		if id == "" {
			return nil, claudeToolPairMessageEmptyToolResultID
		}
		if _, exists := ids[id]; exists {
			return nil, claudeToolPairMessageDuplicateToolResultID
		}
		ids[id] = struct{}{}
	}
	return ids, -1
}

func validParamPreflightMessage(message interface{}) bool {
	switch value := message.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case []interface{}:
		if len(value) == 0 {
			return false
		}
		for _, item := range value {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func resolveParamPreflightMessage(message interface{}, index int) string {
	switch value := message.(type) {
	case string:
		return strings.TrimSpace(value)
	case []interface{}:
		if index >= 0 && index < len(value) {
			if text, ok := value[index].(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
		if len(value) > 0 {
			if text, ok := value[0].(string); ok {
				return strings.TrimSpace(text)
			}
		}
	}
	return "request was rejected by parameter preflight validation"
}

func sameStringSet(left map[string]struct{}, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, exists := right[value]; !exists {
			return false
		}
	}
	return true
}

func isParamPreflightValuePresent(path string, value gjson.Result) bool {
	if !value.Exists() {
		return false
	}
	if !strings.Contains(path, "#") || !value.IsArray() {
		return true
	}
	present := false
	value.ForEach(func(_, item gjson.Result) bool {
		if item.IsArray() {
			if isParamPreflightValuePresent("#", item) {
				present = true
				return false
			}
			return true
		}
		// Projection results preserve explicit null and empty-string values.
		present = true
		return false
	})
	return present
}

func validateConditionValue(condition ParamPreflightCondition) error {
	mode := strings.ToLower(strings.TrimSpace(condition.Mode))
	if mode != "in" && mode != "not_in" {
		return nil
	}
	if strings.TrimSpace(condition.ValuePath) != "" {
		return nil
	}
	values, ok := condition.Value.([]interface{})
	if !ok || len(values) == 0 {
		return fmt.Errorf("mode 为 %s 时 value 必须是非空数组，或设置 value_path", mode)
	}
	return nil
}
