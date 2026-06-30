package service

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/samber/hot"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	ginKeyChannelAffinityCacheKey     = "channel_affinity_cache_key"
	ginKeyChannelAffinityTTLSeconds   = "channel_affinity_ttl_seconds"
	ginKeyChannelAffinityMeta         = "channel_affinity_meta"
	ginKeyChannelAffinityLogInfo      = "channel_affinity_log_info"
	ginKeyChannelAffinitySkipRetry    = "channel_affinity_skip_retry_on_failure"
	ginKeyChannelAffinityPreferredKey = "channel_affinity_preferred_multi_key"
	ginKeyChannelAffinitySelectedKey  = "channel_affinity_selected_multi_key"
	ginKeyChannelAffinityCachedTarget = "channel_affinity_cached_target"

	channelAffinityCacheNamespace           = "new-api:channel_affinity:v1"
	channelAffinityUsageCacheStatsNamespace = "new-api:channel_affinity_usage_cache_stats:v1"
)

var (
	channelAffinityCacheOnce sync.Once
	channelAffinityCache     *cachex.HybridCache[ChannelAffinityCacheValue]

	channelAffinityUsageCacheStatsOnce  sync.Once
	channelAffinityUsageCacheStatsCache *cachex.HybridCache[ChannelAffinityUsageCacheCounters]

	channelAffinityRegexCache sync.Map // map[string]*regexp.Regexp
)

type ChannelAffinityCacheValue struct {
	ChannelID      int    `json:"channel_id"`
	Target         string `json:"target,omitempty"`
	MultiKeyIndex  int    `json:"multi_key_index,omitempty"`
	KeyFingerprint string `json:"key_fp,omitempty"`
}

type channelAffinityCacheValueCodec struct{}

func (c channelAffinityCacheValueCodec) Encode(v ChannelAffinityCacheValue) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c channelAffinityCacheValueCodec) Decode(s string) (ChannelAffinityCacheValue, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return ChannelAffinityCacheValue{}, fmt.Errorf("empty channel affinity value")
	}
	if !strings.HasPrefix(s, "{") {
		id, err := strconv.Atoi(s)
		if err != nil {
			return ChannelAffinityCacheValue{}, err
		}
		return ChannelAffinityCacheValue{ChannelID: id, Target: operation_setting.ChannelAffinityTargetChannel}, nil
	}
	var value ChannelAffinityCacheValue
	if err := json.Unmarshal([]byte(s), &value); err != nil {
		return ChannelAffinityCacheValue{}, err
	}
	if value.Target == "" {
		value.Target = operation_setting.ChannelAffinityTargetChannel
	}
	return value, nil
}

type ChannelAffinityPreferredMultiKey struct {
	Index          int
	KeyFingerprint string
}

type ChannelAffinitySelectedMultiKey struct {
	Index          int
	KeyFingerprint string
	Status         string
	FallbackReason string
}

type channelAffinityMeta struct {
	CacheKey       string
	TTLSeconds     int
	RuleName       string
	Target         string
	SkipRetry      bool
	ParamTemplate  map[string]interface{}
	KeySourceType  string
	KeySourceKey   string
	KeySourcePath  string
	KeyHint        string
	KeyFingerprint string
	AffinityValue  string
	Mapping        operation_setting.ChannelAffinityMapping
	UsingGroup     string
	ModelName      string
	RequestPath    string
}

type ChannelAffinityStatsContext struct {
	RuleName       string
	UsingGroup     string
	KeyFingerprint string
	TTLSeconds     int64
}

const (
	cacheTokenRateModeCachedOverPrompt           = "cached_over_prompt"
	cacheTokenRateModeCachedOverPromptPlusCached = "cached_over_prompt_plus_cached"
	cacheTokenRateModeMixed                      = "mixed"
)

type ChannelAffinityCacheStats struct {
	Enabled       bool           `json:"enabled"`
	Total         int            `json:"total"`
	Unknown       int            `json:"unknown"`
	ByRuleName    map[string]int `json:"by_rule_name"`
	CacheCapacity int            `json:"cache_capacity"`
	CacheAlgo     string         `json:"cache_algo"`
}

func getChannelAffinityCache() *cachex.HybridCache[ChannelAffinityCacheValue] {
	channelAffinityCacheOnce.Do(func() {
		setting := operation_setting.GetChannelAffinitySetting()
		capacity := setting.MaxEntries
		if capacity <= 0 {
			capacity = 100_000
		}
		defaultTTLSeconds := setting.DefaultTTLSeconds
		if defaultTTLSeconds <= 0 {
			defaultTTLSeconds = 3600
		}

		channelAffinityCache = cachex.NewHybridCache[ChannelAffinityCacheValue](cachex.HybridCacheConfig[ChannelAffinityCacheValue]{
			Namespace: cachex.Namespace(channelAffinityCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: channelAffinityCacheValueCodec{},
			Memory: func() *hot.HotCache[string, ChannelAffinityCacheValue] {
				return hot.NewHotCache[string, ChannelAffinityCacheValue](hot.LRU, capacity).
					WithTTL(time.Duration(defaultTTLSeconds) * time.Second).
					WithJanitor().
					Build()
			},
		})
	})
	return channelAffinityCache
}

func GetChannelAffinityCacheStats() ChannelAffinityCacheStats {
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil {
		return ChannelAffinityCacheStats{
			Enabled:    false,
			Total:      0,
			Unknown:    0,
			ByRuleName: map[string]int{},
		}
	}

	cache := getChannelAffinityCache()
	mainCap, _ := cache.Capacity()
	mainAlgo, _ := cache.Algorithm()

	rules := setting.Rules
	ruleByName := make(map[string]operation_setting.ChannelAffinityRule, len(rules))
	for _, r := range rules {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		if !r.IncludeRuleName {
			continue
		}
		ruleByName[name] = r
	}

	byRuleName := make(map[string]int, len(ruleByName))
	for name := range ruleByName {
		byRuleName[name] = 0
	}

	keys, err := cache.Keys()
	if err != nil {
		common.SysError(fmt.Sprintf("channel affinity cache list keys failed: err=%v", err))
		keys = nil
	}
	total := len(keys)
	unknown := 0
	for _, k := range keys {
		prefix := channelAffinityCacheNamespace + ":"
		if !strings.HasPrefix(k, prefix) {
			unknown++
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		parts := strings.Split(rest, ":")
		if len(parts) < 2 {
			unknown++
			continue
		}
		ruleName := parts[0]
		rule, ok := ruleByName[ruleName]
		if !ok {
			unknown++
			continue
		}
		if rule.IncludeModelName {
			if len(parts) < 3 {
				unknown++
				continue
			}
		}
		if rule.IncludeUsingGroup {
			minParts := 3
			if rule.IncludeModelName {
				minParts = 4
			}
			if len(parts) < minParts {
				unknown++
				continue
			}
		}
		byRuleName[ruleName]++
	}

	return ChannelAffinityCacheStats{
		Enabled:       setting.Enabled,
		Total:         total,
		Unknown:       unknown,
		ByRuleName:    byRuleName,
		CacheCapacity: mainCap,
		CacheAlgo:     mainAlgo,
	}
}

func ClearChannelAffinityCacheAll() int {
	cache := getChannelAffinityCache()
	keys, err := cache.Keys()
	if err != nil {
		common.SysError(fmt.Sprintf("channel affinity cache list keys failed: err=%v", err))
		keys = nil
	}
	if len(keys) > 0 {
		if _, err := cache.DeleteMany(keys); err != nil {
			common.SysError(fmt.Sprintf("channel affinity cache delete many failed: err=%v", err))
		}
	}
	return len(keys)
}

func ClearChannelAffinityCacheByRuleName(ruleName string) (int, error) {
	ruleName = strings.TrimSpace(ruleName)
	if ruleName == "" {
		return 0, fmt.Errorf("rule_name 不能为空")
	}

	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil {
		return 0, fmt.Errorf("channel_affinity_setting 未初始化")
	}

	var matchedRule *operation_setting.ChannelAffinityRule
	for i := range setting.Rules {
		r := &setting.Rules[i]
		if strings.TrimSpace(r.Name) != ruleName {
			continue
		}
		matchedRule = r
		break
	}
	if matchedRule == nil {
		return 0, fmt.Errorf("未知规则名称")
	}
	if !matchedRule.IncludeRuleName {
		return 0, fmt.Errorf("该规则未启用 include_rule_name，无法按规则清空缓存")
	}

	cache := getChannelAffinityCache()
	deleted, err := cache.DeleteByPrefix(ruleName)
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func matchAnyRegexCached(patterns []string, s string) bool {
	if len(patterns) == 0 || s == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		re, ok := channelAffinityRegexCache.Load(pattern)
		if !ok {
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				continue
			}
			re = compiled
			channelAffinityRegexCache.Store(pattern, re)
		}
		if re.(*regexp.Regexp).MatchString(s) {
			return true
		}
	}
	return false
}

func matchAnyIncludeFold(patterns []string, s string) bool {
	if len(patterns) == 0 || s == "" {
		return false
	}
	sLower := strings.ToLower(s)
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(sLower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func extractChannelAffinityValue(c *gin.Context, src operation_setting.ChannelAffinityKeySource) string {
	if c == nil {
		return ""
	}
	switch src.Type {
	case "context_int":
		if src.Key == "" {
			return ""
		}
		v := c.GetInt(src.Key)
		if v <= 0 {
			return ""
		}
		return strconv.Itoa(v)
	case "context_string":
		if src.Key == "" {
			return ""
		}
		return strings.TrimSpace(c.GetString(src.Key))
	case "request_header", "header":
		if c == nil || c.Request == nil || src.Key == "" {
			return ""
		}
		return strings.TrimSpace(c.Request.Header.Get(src.Key))
	case "gjson":
		if src.Path == "" {
			return ""
		}
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return ""
		}
		body, err := storage.Bytes()
		if err != nil || len(body) == 0 {
			return ""
		}
		res := gjson.GetBytes(body, src.Path)
		if !res.Exists() {
			return ""
		}
		switch res.Type {
		case gjson.String, gjson.Number, gjson.True, gjson.False:
			return strings.TrimSpace(res.String())
		default:
			return strings.TrimSpace(res.Raw)
		}
	default:
		return ""
	}
}

func normalizeChannelAffinityMappingTargets(mapping operation_setting.ChannelAffinityMapping) []operation_setting.ChannelAffinityMappingTarget {
	if !mapping.Enabled {
		return nil
	}
	targets := mapping.Targets
	if len(targets) == 0 && strings.TrimSpace(mapping.Type) != "" {
		targets = []operation_setting.ChannelAffinityMappingTarget{
			{
				Type:      mapping.Type,
				Key:       mapping.Key,
				Path:      mapping.Path,
				Overwrite: mapping.Overwrite,
			},
		}
	}
	normalized := make([]operation_setting.ChannelAffinityMappingTarget, 0, len(targets))
	for _, target := range targets {
		target.Type = strings.TrimSpace(target.Type)
		target.Key = strings.TrimSpace(target.Key)
		target.Path = strings.TrimSpace(target.Path)
		if target.Type == "" {
			continue
		}
		switch target.Type {
		case "header":
			if target.Key == "" {
				continue
			}
			target.Path = ""
		case "gjson":
			if target.Path == "" {
				continue
			}
			target.Key = ""
		default:
			continue
		}
		normalized = append(normalized, target)
	}
	return normalized
}

func getChannelAffinityMappingContext(c *gin.Context) (string, []operation_setting.ChannelAffinityMappingTarget, bool) {
	meta, ok := getChannelAffinityMeta(c)
	if !ok {
		return "", nil, false
	}
	affinityValue := strings.TrimSpace(meta.AffinityValue)
	if affinityValue == "" {
		return "", nil, false
	}
	targets := normalizeChannelAffinityMappingTargets(meta.Mapping)
	if len(targets) == 0 {
		return "", nil, false
	}
	return affinityValue, targets, true
}

func HasChannelAffinityJSONMapping(c *gin.Context) bool {
	_, targets, ok := getChannelAffinityMappingContext(c)
	if !ok {
		return false
	}
	for _, target := range targets {
		if target.Type == "gjson" {
			return true
		}
	}
	return false
}

func hasChannelAffinityMappingValue(jsonData []byte, path string) bool {
	result := gjson.GetBytes(jsonData, path)
	if !result.Exists() || result.Type == gjson.Null {
		return false
	}
	if result.Type == gjson.String && strings.TrimSpace(result.String()) == "" {
		return false
	}
	return true
}

func ApplyChannelAffinityJSONMapping(c *gin.Context, jsonData []byte) ([]byte, error) {
	affinityValue, targets, ok := getChannelAffinityMappingContext(c)
	if !ok || len(jsonData) == 0 {
		return jsonData, nil
	}
	var err error
	for _, target := range targets {
		if target.Type != "gjson" || target.Path == "" {
			continue
		}
		if !target.Overwrite && hasChannelAffinityMappingValue(jsonData, target.Path) {
			continue
		}
		jsonData, err = sjson.SetBytes(jsonData, target.Path, affinityValue)
		if err != nil {
			return nil, err
		}
	}
	return jsonData, nil
}

func ApplyChannelAffinityHeaderMapping(c *gin.Context, header *http.Header) {
	if header == nil {
		return
	}
	affinityValue, targets, ok := getChannelAffinityMappingContext(c)
	if !ok {
		return
	}
	for _, target := range targets {
		if target.Type != "header" || target.Key == "" {
			continue
		}
		if !target.Overwrite && strings.TrimSpace(header.Get(target.Key)) != "" {
			continue
		}
		header.Set(target.Key, affinityValue)
	}
}

func buildChannelAffinityCacheKeySuffix(rule operation_setting.ChannelAffinityRule, modelName string, usingGroup string, affinityValue string) string {
	parts := make([]string, 0, 4)
	if rule.IncludeRuleName && rule.Name != "" {
		parts = append(parts, rule.Name)
	}
	if rule.IncludeModelName && modelName != "" {
		parts = append(parts, modelName)
	}
	if rule.IncludeUsingGroup && usingGroup != "" {
		parts = append(parts, usingGroup)
	}
	if operation_setting.NormalizeChannelAffinityTarget(rule.Target) == operation_setting.ChannelAffinityTargetMultiKey {
		affinityValue = affinityFingerprint(affinityValue)
	}
	parts = append(parts, affinityValue)
	return strings.Join(parts, ":")
}

func setChannelAffinityContext(c *gin.Context, meta channelAffinityMeta) {
	c.Set(ginKeyChannelAffinityCacheKey, meta.CacheKey)
	c.Set(ginKeyChannelAffinityTTLSeconds, meta.TTLSeconds)
	c.Set(ginKeyChannelAffinityMeta, meta)
}

func getChannelAffinityContext(c *gin.Context) (string, int, bool) {
	keyAny, ok := c.Get(ginKeyChannelAffinityCacheKey)
	if !ok {
		return "", 0, false
	}
	key, ok := keyAny.(string)
	if !ok || key == "" {
		return "", 0, false
	}
	ttlAny, ok := c.Get(ginKeyChannelAffinityTTLSeconds)
	if !ok {
		return key, 0, true
	}
	ttlSeconds, _ := ttlAny.(int)
	return key, ttlSeconds, true
}

func getChannelAffinityMeta(c *gin.Context) (channelAffinityMeta, bool) {
	anyMeta, ok := c.Get(ginKeyChannelAffinityMeta)
	if !ok {
		return channelAffinityMeta{}, false
	}
	meta, ok := anyMeta.(channelAffinityMeta)
	if !ok {
		return channelAffinityMeta{}, false
	}
	return meta, true
}

func GetChannelAffinityStatsContext(c *gin.Context) (ChannelAffinityStatsContext, bool) {
	if c == nil {
		return ChannelAffinityStatsContext{}, false
	}
	meta, ok := getChannelAffinityMeta(c)
	if !ok {
		return ChannelAffinityStatsContext{}, false
	}
	ruleName := strings.TrimSpace(meta.RuleName)
	keyFp := strings.TrimSpace(meta.KeyFingerprint)
	usingGroup := strings.TrimSpace(meta.UsingGroup)
	if ruleName == "" || keyFp == "" {
		return ChannelAffinityStatsContext{}, false
	}
	ttlSeconds := int64(meta.TTLSeconds)
	if ttlSeconds <= 0 {
		return ChannelAffinityStatsContext{}, false
	}
	return ChannelAffinityStatsContext{
		RuleName:       ruleName,
		UsingGroup:     usingGroup,
		KeyFingerprint: keyFp,
		TTLSeconds:     ttlSeconds,
	}, true
}

func affinityFingerprint(s string) string {
	if s == "" {
		return ""
	}
	hex := common.Sha1([]byte(s))
	if len(hex) >= 8 {
		return hex[:8]
	}
	return hex
}

func ChannelAffinityFingerprint(s string) string {
	return affinityFingerprint(s)
}

func buildChannelAffinityKeyHint(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) <= 12 {
		return s
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func cloneStringAnyMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return map[string]interface{}{}
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mergeChannelOverride(base map[string]interface{}, tpl map[string]interface{}) map[string]interface{} {
	if len(base) == 0 && len(tpl) == 0 {
		return map[string]interface{}{}
	}
	if len(tpl) == 0 {
		return base
	}
	out := cloneStringAnyMap(base)
	for k, v := range tpl {
		if strings.EqualFold(strings.TrimSpace(k), "operations") {
			baseOps, hasBaseOps := extractParamOperations(out[k])
			tplOps, hasTplOps := extractParamOperations(v)
			if hasTplOps {
				if hasBaseOps {
					out[k] = append(tplOps, baseOps...)
				} else {
					out[k] = tplOps
				}
				continue
			}
		}
		if _, exists := out[k]; exists {
			continue
		}
		out[k] = v
	}
	return out
}

func extractParamOperations(value interface{}) ([]interface{}, bool) {
	switch ops := value.(type) {
	case []interface{}:
		if len(ops) == 0 {
			return []interface{}{}, true
		}
		cloned := make([]interface{}, 0, len(ops))
		cloned = append(cloned, ops...)
		return cloned, true
	case []map[string]interface{}:
		cloned := make([]interface{}, 0, len(ops))
		for _, op := range ops {
			cloned = append(cloned, op)
		}
		return cloned, true
	default:
		return nil, false
	}
}

func appendChannelAffinityTemplateAdminInfo(c *gin.Context, meta channelAffinityMeta) {
	if c == nil {
		return
	}
	if len(meta.ParamTemplate) == 0 {
		return
	}

	templateInfo := map[string]interface{}{
		"applied":             true,
		"rule_name":           meta.RuleName,
		"param_override_keys": len(meta.ParamTemplate),
	}
	if anyInfo, ok := c.Get(ginKeyChannelAffinityLogInfo); ok {
		if info, ok := anyInfo.(map[string]interface{}); ok {
			info["override_template"] = templateInfo
			c.Set(ginKeyChannelAffinityLogInfo, info)
			return
		}
	}
	c.Set(ginKeyChannelAffinityLogInfo, map[string]interface{}{
		"reason":            meta.RuleName,
		"rule_name":         meta.RuleName,
		"using_group":       meta.UsingGroup,
		"model":             meta.ModelName,
		"request_path":      meta.RequestPath,
		"key_source":        meta.KeySourceType,
		"key_key":           meta.KeySourceKey,
		"key_path":          meta.KeySourcePath,
		"key_hint":          meta.KeyHint,
		"key_fp":            meta.KeyFingerprint,
		"override_template": templateInfo,
	})
}

// ApplyChannelAffinityOverrideTemplate merges per-rule channel override templates onto the selected channel override config.
func ApplyChannelAffinityOverrideTemplate(c *gin.Context, paramOverride map[string]interface{}) (map[string]interface{}, bool) {
	if c == nil {
		return paramOverride, false
	}
	meta, ok := getChannelAffinityMeta(c)
	if !ok {
		return paramOverride, false
	}
	if len(meta.ParamTemplate) == 0 {
		return paramOverride, false
	}

	mergedParam := mergeChannelOverride(paramOverride, meta.ParamTemplate)
	appendChannelAffinityTemplateAdminInfo(c, meta)
	return mergedParam, true
}

func GetPreferredChannelByAffinity(c *gin.Context, modelName string, usingGroup string) (ChannelAffinityCacheValue, bool) {
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil || !setting.Enabled {
		return ChannelAffinityCacheValue{}, false
	}
	path := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	userAgent := ""
	if c != nil && c.Request != nil {
		userAgent = c.Request.UserAgent()
	}

	for _, rule := range setting.Rules {
		if !matchAnyRegexCached(rule.ModelRegex, modelName) {
			continue
		}
		if len(rule.PathRegex) > 0 && !matchAnyRegexCached(rule.PathRegex, path) {
			continue
		}
		if len(rule.UserAgentInclude) > 0 && !matchAnyIncludeFold(rule.UserAgentInclude, userAgent) {
			continue
		}
		var affinityValue string
		var usedSource operation_setting.ChannelAffinityKeySource
		for _, src := range rule.KeySources {
			affinityValue = extractChannelAffinityValue(c, src)
			if affinityValue != "" {
				usedSource = src
				break
			}
		}
		if affinityValue == "" {
			continue
		}
		if rule.ValueRegex != "" && !matchAnyRegexCached([]string{rule.ValueRegex}, affinityValue) {
			continue
		}

		ttlSeconds := rule.TTLSeconds
		if ttlSeconds <= 0 {
			ttlSeconds = setting.DefaultTTLSeconds
		}
		cacheKeySuffix := buildChannelAffinityCacheKeySuffix(rule, modelName, usingGroup, affinityValue)
		cacheKeyFull := channelAffinityCacheNamespace + ":" + cacheKeySuffix
		target := operation_setting.NormalizeChannelAffinityTarget(rule.Target)
		meta := channelAffinityMeta{
			CacheKey:       cacheKeyFull,
			TTLSeconds:     ttlSeconds,
			RuleName:       rule.Name,
			Target:         target,
			SkipRetry:      rule.SkipRetryOnFailure,
			ParamTemplate:  cloneStringAnyMap(rule.ParamOverrideTemplate),
			KeySourceType:  strings.TrimSpace(usedSource.Type),
			KeySourceKey:   strings.TrimSpace(usedSource.Key),
			KeySourcePath:  strings.TrimSpace(usedSource.Path),
			KeyHint:        buildChannelAffinityKeyHint(affinityValue),
			KeyFingerprint: affinityFingerprint(affinityValue),
			AffinityValue:  affinityValue,
			Mapping:        rule.Mapping,
			UsingGroup:     usingGroup,
			ModelName:      modelName,
			RequestPath:    path,
		}
		setChannelAffinityContext(c, meta)

		cache := getChannelAffinityCache()
		cacheValue, found, err := cache.Get(cacheKeySuffix)
		if err != nil {
			common.SysError(fmt.Sprintf("channel affinity cache get failed: key=%s, err=%v", cacheKeyFull, err))
			return ChannelAffinityCacheValue{}, false
		}
		if found {
			cacheValue.Target = operation_setting.NormalizeChannelAffinityTarget(cacheValue.Target)
			if target == operation_setting.ChannelAffinityTargetMultiKey &&
				cacheValue.Target == operation_setting.ChannelAffinityTargetMultiKey &&
				cacheValue.MultiKeyIndex >= 0 {
				setChannelAffinityPreferredMultiKey(c, cacheValue.MultiKeyIndex, cacheValue.KeyFingerprint)
			}
			setChannelAffinityCachedTarget(c, cacheValue.Target)
			return cacheValue, true
		}
		return ChannelAffinityCacheValue{}, false
	}
	return ChannelAffinityCacheValue{}, false
}

func ShouldSkipRetryAfterChannelAffinityFailure(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, ok := c.Get(ginKeyChannelAffinitySkipRetry)
	if ok {
		b, ok := v.(bool)
		if ok {
			return b
		}
	}
	meta, ok := getChannelAffinityMeta(c)
	if !ok {
		return false
	}
	return meta.SkipRetry
}

func ClearCurrentChannelAffinityCache(c *gin.Context) bool {
	if c == nil {
		return false
	}
	cacheKey, _, ok := getChannelAffinityContext(c)
	if !ok || cacheKey == "" {
		return false
	}

	cache := getChannelAffinityCache()
	deleted, err := cache.DeleteMany([]string{cacheKey})
	if err != nil {
		common.SysError(fmt.Sprintf("channel affinity cache delete current failed: err=%v", err))
		return false
	}
	c.Set(ginKeyChannelAffinitySkipRetry, false)
	for _, ok := range deleted {
		if ok {
			return true
		}
	}
	return false
}

func ShouldKeepChannelAffinityOnChannelDisabled() bool {
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil {
		return false
	}
	return setting.KeepOnChannelDisabled
}

func IsChannelAffinityMultiKeyTarget(c *gin.Context) bool {
	meta, ok := getChannelAffinityMeta(c)
	if !ok {
		return false
	}
	return meta.Target == operation_setting.ChannelAffinityTargetMultiKey
}

func setChannelAffinityPreferredMultiKey(c *gin.Context, index int, keyFingerprint string) {
	if c == nil || index < 0 {
		return
	}
	c.Set(ginKeyChannelAffinityPreferredKey, ChannelAffinityPreferredMultiKey{
		Index:          index,
		KeyFingerprint: strings.TrimSpace(keyFingerprint),
	})
}

func GetChannelAffinityPreferredMultiKey(c *gin.Context) (ChannelAffinityPreferredMultiKey, bool) {
	if c == nil {
		return ChannelAffinityPreferredMultiKey{}, false
	}
	v, ok := c.Get(ginKeyChannelAffinityPreferredKey)
	if !ok {
		return ChannelAffinityPreferredMultiKey{}, false
	}
	preferred, ok := v.(ChannelAffinityPreferredMultiKey)
	if !ok || preferred.Index < 0 {
		return ChannelAffinityPreferredMultiKey{}, false
	}
	return preferred, true
}

func setChannelAffinityCachedTarget(c *gin.Context, target string) {
	if c == nil {
		return
	}
	target = operation_setting.NormalizeChannelAffinityTarget(target)
	if target == "" {
		return
	}
	c.Set(ginKeyChannelAffinityCachedTarget, target)
}

func getChannelAffinityCachedTarget(c *gin.Context) string {
	if c == nil {
		return ""
	}
	target, _ := c.Get(ginKeyChannelAffinityCachedTarget)
	targetString, _ := target.(string)
	if strings.TrimSpace(targetString) == "" {
		return ""
	}
	return operation_setting.NormalizeChannelAffinityTarget(targetString)
}

func updateChannelAffinityLogInfo(c *gin.Context, update func(map[string]interface{})) {
	if c == nil || update == nil {
		return
	}
	anyInfo, ok := c.Get(ginKeyChannelAffinityLogInfo)
	if !ok || anyInfo == nil {
		return
	}
	info, ok := anyInfo.(map[string]interface{})
	if !ok {
		return
	}
	update(info)
	c.Set(ginKeyChannelAffinityLogInfo, info)
}

func MarkChannelAffinitySelectedMultiKey(c *gin.Context, index int, keyFingerprint string, status string, fallbackReason string) {
	if c == nil {
		return
	}
	effectiveTarget := operation_setting.ChannelAffinityTargetMultiKey
	if index < 0 {
		effectiveTarget = operation_setting.ChannelAffinityTargetChannel
	}
	c.Set(ginKeyChannelAffinitySelectedKey, ChannelAffinitySelectedMultiKey{
		Index:          index,
		KeyFingerprint: strings.TrimSpace(keyFingerprint),
		Status:         strings.TrimSpace(status),
		FallbackReason: strings.TrimSpace(fallbackReason),
	})
	updateChannelAffinityLogInfo(c, func(info map[string]interface{}) {
		if _, ok := info["configured_target"]; !ok {
			info["configured_target"] = operation_setting.ChannelAffinityTargetMultiKey
		}
		info["target"] = effectiveTarget
		info["key_affinity_status"] = strings.TrimSpace(status)
		if index >= 0 {
			info["selected_multi_key_index"] = index
		} else {
			delete(info, "selected_multi_key_index")
		}
		if keyFingerprint != "" {
			info["selected_key_fp"] = strings.TrimSpace(keyFingerprint)
		} else {
			delete(info, "selected_key_fp")
		}
		if fallbackReason != "" {
			info["fallback_reason"] = strings.TrimSpace(fallbackReason)
		} else {
			delete(info, "fallback_reason")
		}
	})
}

func getChannelAffinitySelectedMultiKey(c *gin.Context) (ChannelAffinitySelectedMultiKey, bool) {
	if c == nil {
		return ChannelAffinitySelectedMultiKey{}, false
	}
	v, ok := c.Get(ginKeyChannelAffinitySelectedKey)
	if !ok {
		return ChannelAffinitySelectedMultiKey{}, false
	}
	selected, ok := v.(ChannelAffinitySelectedMultiKey)
	if !ok || selected.Index < 0 {
		return ChannelAffinitySelectedMultiKey{}, false
	}
	return selected, true
}

func MarkChannelAffinityUsed(c *gin.Context, selectedGroup string, channelID int) {
	MarkChannelAffinitySelected(c, selectedGroup, channelID, true)
}

func MarkChannelAffinitySelected(c *gin.Context, selectedGroup string, channelID int, hit bool) {
	if c == nil || channelID <= 0 {
		return
	}
	meta, ok := getChannelAffinityMeta(c)
	if !ok {
		return
	}
	c.Set(ginKeyChannelAffinitySkipRetry, meta.SkipRetry)
	configuredTarget := operation_setting.NormalizeChannelAffinityTarget(meta.Target)
	target := configuredTarget
	if hit {
		if cachedTarget := getChannelAffinityCachedTarget(c); cachedTarget != "" {
			target = cachedTarget
		}
	}
	info := map[string]interface{}{
		"reason":         meta.RuleName,
		"rule_name":      meta.RuleName,
		"target":         target,
		"hit":            hit,
		"using_group":    meta.UsingGroup,
		"selected_group": selectedGroup,
		"model":          meta.ModelName,
		"request_path":   meta.RequestPath,
		"channel_id":     channelID,
		"key_source":     meta.KeySourceType,
		"key_key":        meta.KeySourceKey,
		"key_path":       meta.KeySourcePath,
		"key_fp":         meta.KeyFingerprint,
	}
	if configuredTarget == operation_setting.ChannelAffinityTargetMultiKey {
		info["configured_target"] = configuredTarget
		info["affinity_fp"] = meta.KeyFingerprint
		if meta.KeyHint != "" {
			info["key_hint"] = meta.KeyHint
		}
		if target == operation_setting.ChannelAffinityTargetMultiKey {
			if preferred, ok := GetChannelAffinityPreferredMultiKey(c); ok {
				info["preferred_multi_key_index"] = preferred.Index
				if preferred.KeyFingerprint != "" {
					info["preferred_key_fp"] = preferred.KeyFingerprint
				}
			}
		}
		if !hit && target == operation_setting.ChannelAffinityTargetMultiKey {
			info["key_affinity_status"] = "miss"
		}
	} else if meta.KeyHint != "" {
		info["key_hint"] = meta.KeyHint
	}
	c.Set(ginKeyChannelAffinityLogInfo, info)
}

func AppendChannelAffinityAdminInfo(c *gin.Context, adminInfo map[string]interface{}) {
	if c == nil || adminInfo == nil {
		return
	}
	anyInfo, ok := c.Get(ginKeyChannelAffinityLogInfo)
	if !ok || anyInfo == nil {
		return
	}
	adminInfo["channel_affinity"] = anyInfo
}

func RecordChannelAffinity(c *gin.Context, channelID int) {
	if channelID <= 0 {
		return
	}
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil || !setting.Enabled {
		return
	}
	if setting.SwitchOnSuccess && c != nil {
		if successChannelID := c.GetInt("channel_id"); successChannelID > 0 {
			channelID = successChannelID
		}
	}
	meta, hasMeta := getChannelAffinityMeta(c)
	cacheKey, ttlSeconds, ok := getChannelAffinityContext(c)
	if !ok {
		return
	}
	if ttlSeconds <= 0 {
		ttlSeconds = setting.DefaultTTLSeconds
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 3600
	}
	cache := getChannelAffinityCache()
	target := operation_setting.ChannelAffinityTargetChannel
	var selected ChannelAffinitySelectedMultiKey
	var hasSelected bool
	if hasMeta {
		target = operation_setting.NormalizeChannelAffinityTarget(meta.Target)
	}
	if target == operation_setting.ChannelAffinityTargetMultiKey {
		selected, hasSelected = getChannelAffinitySelectedMultiKey(c)
		if !hasSelected {
			target = operation_setting.ChannelAffinityTargetChannel
			updateChannelAffinityLogInfo(c, func(info map[string]interface{}) {
				info["target"] = operation_setting.ChannelAffinityTargetChannel
				info["configured_target"] = operation_setting.ChannelAffinityTargetMultiKey
				info["recorded"] = true
				delete(info, "key_affinity_status")
				delete(info, "preferred_multi_key_index")
				delete(info, "preferred_key_fp")
				delete(info, "selected_multi_key_index")
				delete(info, "selected_key_fp")
				delete(info, "fallback_reason")
			})
		}
	}
	cacheValue := ChannelAffinityCacheValue{
		ChannelID: channelID,
		Target:    target,
	}
	if target == operation_setting.ChannelAffinityTargetMultiKey {
		cacheValue.MultiKeyIndex = selected.Index
		cacheValue.KeyFingerprint = selected.KeyFingerprint
	}
	if err := cache.SetWithTTL(cacheKey, cacheValue, time.Duration(ttlSeconds)*time.Second); err != nil {
		common.SysError(fmt.Sprintf("channel affinity cache set failed: key=%s, err=%v", cacheKey, err))
	}
}

type ChannelAffinityUsageCacheStats struct {
	RuleName            string `json:"rule_name"`
	UsingGroup          string `json:"using_group"`
	KeyFingerprint      string `json:"key_fp"`
	CachedTokenRateMode string `json:"cached_token_rate_mode"`

	Hit           int64 `json:"hit"`
	Total         int64 `json:"total"`
	WindowSeconds int64 `json:"window_seconds"`

	PromptTokens         int64 `json:"prompt_tokens"`
	CompletionTokens     int64 `json:"completion_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
	CachedTokens         int64 `json:"cached_tokens"`
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
	LastSeenAt           int64 `json:"last_seen_at"`
}

type ChannelAffinityUsageCacheCounters struct {
	CachedTokenRateMode string `json:"cached_token_rate_mode"`

	Hit           int64 `json:"hit"`
	Total         int64 `json:"total"`
	WindowSeconds int64 `json:"window_seconds"`

	PromptTokens         int64 `json:"prompt_tokens"`
	CompletionTokens     int64 `json:"completion_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
	CachedTokens         int64 `json:"cached_tokens"`
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
	LastSeenAt           int64 `json:"last_seen_at"`
}

var channelAffinityUsageCacheStatsLocks [64]sync.Mutex

// ObserveChannelAffinityUsageCacheByRelayFormat records usage cache stats with a stable rate mode derived from relay format.
func ObserveChannelAffinityUsageCacheByRelayFormat(c *gin.Context, usage *dto.Usage, relayFormat types.RelayFormat) {
	ObserveChannelAffinityUsageCacheFromContext(c, usage, cachedTokenRateModeByRelayFormat(relayFormat))
}

func ObserveChannelAffinityUsageCacheFromContext(c *gin.Context, usage *dto.Usage, cachedTokenRateMode string) {
	statsCtx, ok := GetChannelAffinityStatsContext(c)
	if !ok {
		return
	}
	observeChannelAffinityUsageCache(statsCtx, usage, cachedTokenRateMode)
}

func GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFp string) ChannelAffinityUsageCacheStats {
	ruleName = strings.TrimSpace(ruleName)
	usingGroup = strings.TrimSpace(usingGroup)
	keyFp = strings.TrimSpace(keyFp)

	entryKey := channelAffinityUsageCacheEntryKey(ruleName, usingGroup, keyFp)
	if entryKey == "" {
		return ChannelAffinityUsageCacheStats{
			RuleName:       ruleName,
			UsingGroup:     usingGroup,
			KeyFingerprint: keyFp,
		}
	}

	cache := getChannelAffinityUsageCacheStatsCache()
	v, found, err := cache.Get(entryKey)
	if err != nil || !found {
		return ChannelAffinityUsageCacheStats{
			RuleName:       ruleName,
			UsingGroup:     usingGroup,
			KeyFingerprint: keyFp,
		}
	}
	return ChannelAffinityUsageCacheStats{
		CachedTokenRateMode:  v.CachedTokenRateMode,
		RuleName:             ruleName,
		UsingGroup:           usingGroup,
		KeyFingerprint:       keyFp,
		Hit:                  v.Hit,
		Total:                v.Total,
		WindowSeconds:        v.WindowSeconds,
		PromptTokens:         v.PromptTokens,
		CompletionTokens:     v.CompletionTokens,
		TotalTokens:          v.TotalTokens,
		CachedTokens:         v.CachedTokens,
		PromptCacheHitTokens: v.PromptCacheHitTokens,
		LastSeenAt:           v.LastSeenAt,
	}
}

func observeChannelAffinityUsageCache(statsCtx ChannelAffinityStatsContext, usage *dto.Usage, cachedTokenRateMode string) {
	entryKey := channelAffinityUsageCacheEntryKey(statsCtx.RuleName, statsCtx.UsingGroup, statsCtx.KeyFingerprint)
	if entryKey == "" {
		return
	}

	windowSeconds := statsCtx.TTLSeconds
	if windowSeconds <= 0 {
		return
	}

	cache := getChannelAffinityUsageCacheStatsCache()
	ttl := time.Duration(windowSeconds) * time.Second

	lock := channelAffinityUsageCacheStatsLock(entryKey)
	lock.Lock()
	defer lock.Unlock()

	prev, found, err := cache.Get(entryKey)
	if err != nil {
		return
	}
	next := prev
	if !found {
		next = ChannelAffinityUsageCacheCounters{}
	}
	currentMode := normalizeCachedTokenRateMode(cachedTokenRateMode)
	if currentMode != "" {
		if next.CachedTokenRateMode == "" {
			next.CachedTokenRateMode = currentMode
		} else if next.CachedTokenRateMode != currentMode && next.CachedTokenRateMode != cacheTokenRateModeMixed {
			next.CachedTokenRateMode = cacheTokenRateModeMixed
		}
	}
	next.Total++
	hit, cachedTokens, promptCacheHitTokens := usageCacheSignals(usage)
	if hit {
		next.Hit++
	}
	next.WindowSeconds = windowSeconds
	next.LastSeenAt = time.Now().Unix()
	next.CachedTokens += cachedTokens
	next.PromptCacheHitTokens += promptCacheHitTokens
	next.PromptTokens += int64(usagePromptTokens(usage))
	next.CompletionTokens += int64(usageCompletionTokens(usage))
	next.TotalTokens += int64(usageTotalTokens(usage))
	_ = cache.SetWithTTL(entryKey, next, ttl)
}

func normalizeCachedTokenRateMode(mode string) string {
	switch mode {
	case cacheTokenRateModeCachedOverPrompt:
		return cacheTokenRateModeCachedOverPrompt
	case cacheTokenRateModeCachedOverPromptPlusCached:
		return cacheTokenRateModeCachedOverPromptPlusCached
	case cacheTokenRateModeMixed:
		return cacheTokenRateModeMixed
	default:
		return ""
	}
}

func cachedTokenRateModeByRelayFormat(relayFormat types.RelayFormat) string {
	switch relayFormat {
	case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		return cacheTokenRateModeCachedOverPrompt
	case types.RelayFormatClaude:
		return cacheTokenRateModeCachedOverPromptPlusCached
	default:
		return ""
	}
}

func channelAffinityUsageCacheEntryKey(ruleName, usingGroup, keyFp string) string {
	ruleName = strings.TrimSpace(ruleName)
	usingGroup = strings.TrimSpace(usingGroup)
	keyFp = strings.TrimSpace(keyFp)
	if ruleName == "" || keyFp == "" {
		return ""
	}
	return ruleName + "\n" + usingGroup + "\n" + keyFp
}

func usageCacheSignals(usage *dto.Usage) (hit bool, cachedTokens int64, promptCacheHitTokens int64) {
	if usage == nil {
		return false, 0, 0
	}

	cached := int64(0)
	if usage.PromptTokensDetails.CachedTokens > 0 {
		cached = int64(usage.PromptTokensDetails.CachedTokens)
	} else if usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens > 0 {
		cached = int64(usage.InputTokensDetails.CachedTokens)
	}
	pcht := int64(0)
	if usage.PromptCacheHitTokens > 0 {
		pcht = int64(usage.PromptCacheHitTokens)
	}
	return cached > 0 || pcht > 0, cached, pcht
}

func usagePromptTokens(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.PromptTokens > 0 {
		return usage.PromptTokens
	}
	return usage.InputTokens
}

func usageCompletionTokens(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.CompletionTokens > 0 {
		return usage.CompletionTokens
	}
	return usage.OutputTokens
}

func usageTotalTokens(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	pt := usagePromptTokens(usage)
	ct := usageCompletionTokens(usage)
	if pt > 0 || ct > 0 {
		return pt + ct
	}
	return 0
}

func getChannelAffinityUsageCacheStatsCache() *cachex.HybridCache[ChannelAffinityUsageCacheCounters] {
	channelAffinityUsageCacheStatsOnce.Do(func() {
		setting := operation_setting.GetChannelAffinitySetting()
		capacity := 100_000
		defaultTTLSeconds := 3600
		if setting != nil {
			if setting.MaxEntries > 0 {
				capacity = setting.MaxEntries
			}
			if setting.DefaultTTLSeconds > 0 {
				defaultTTLSeconds = setting.DefaultTTLSeconds
			}
		}

		channelAffinityUsageCacheStatsCache = cachex.NewHybridCache[ChannelAffinityUsageCacheCounters](cachex.HybridCacheConfig[ChannelAffinityUsageCacheCounters]{
			Namespace: cachex.Namespace(channelAffinityUsageCacheStatsNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[ChannelAffinityUsageCacheCounters]{},
			Memory: func() *hot.HotCache[string, ChannelAffinityUsageCacheCounters] {
				return hot.NewHotCache[string, ChannelAffinityUsageCacheCounters](hot.LRU, capacity).
					WithTTL(time.Duration(defaultTTLSeconds) * time.Second).
					WithJanitor().
					Build()
			},
		})
	})
	return channelAffinityUsageCacheStatsCache
}

func channelAffinityUsageCacheStatsLock(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	idx := h.Sum32() % uint32(len(channelAffinityUsageCacheStatsLocks))
	return &channelAffinityUsageCacheStatsLocks[idx]
}
