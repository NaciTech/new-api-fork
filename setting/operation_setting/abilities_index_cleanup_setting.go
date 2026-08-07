package operation_setting

import (
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	defaultAbilitiesIndexCleanupIntervalHours = 24
	defaultAutoDisabledThresholdHours         = 24
	defaultAbilitiesIndexCleanupBatchSize     = 100
	maxAbilitiesIndexCleanupIntervalHours     = 24 * 30
	maxAutoDisabledThresholdHours             = 24 * 365
	maxAbilitiesIndexCleanupBatchSize         = 1000
)

type AbilitiesIndexCleanupSetting struct {
	Enabled                    bool `json:"enabled"`
	IntervalHours              int  `json:"interval_hours"`
	AutoDisabledThresholdHours int  `json:"auto_disabled_threshold_hours"`
	BatchSize                  int  `json:"batch_size"`
}

var abilitiesIndexCleanupSetting = AbilitiesIndexCleanupSetting{
	Enabled:                    false,
	IntervalHours:              defaultAbilitiesIndexCleanupIntervalHours,
	AutoDisabledThresholdHours: defaultAutoDisabledThresholdHours,
	BatchSize:                  defaultAbilitiesIndexCleanupBatchSize,
}

func init() {
	config.GlobalConfig.Register("abilities_index_cleanup_setting", &abilitiesIndexCleanupSetting)
}

func GetAbilitiesIndexCleanupSetting() AbilitiesIndexCleanupSetting {
	common.OptionMapRWMutex.RLock()
	setting := abilitiesIndexCleanupSetting
	common.OptionMapRWMutex.RUnlock()
	if setting.IntervalHours < 1 || setting.IntervalHours > maxAbilitiesIndexCleanupIntervalHours {
		setting.IntervalHours = defaultAbilitiesIndexCleanupIntervalHours
	}
	if setting.AutoDisabledThresholdHours < 1 || setting.AutoDisabledThresholdHours > maxAutoDisabledThresholdHours {
		setting.AutoDisabledThresholdHours = defaultAutoDisabledThresholdHours
	}
	if setting.BatchSize < 1 || setting.BatchSize > maxAbilitiesIndexCleanupBatchSize {
		setting.BatchSize = defaultAbilitiesIndexCleanupBatchSize
	}
	return setting
}

func ValidateAbilitiesIndexCleanupSetting(key string, value string) error {
	switch key {
	case "abilities_index_cleanup_setting.enabled":
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("invalid abilities index cleanup enabled value")
		}
		return nil
	case "abilities_index_cleanup_setting.interval_hours":
		return validateAbilitiesIndexCleanupRange(value, 1, maxAbilitiesIndexCleanupIntervalHours)
	case "abilities_index_cleanup_setting.auto_disabled_threshold_hours":
		return validateAbilitiesIndexCleanupRange(value, 1, maxAutoDisabledThresholdHours)
	case "abilities_index_cleanup_setting.batch_size":
		return validateAbilitiesIndexCleanupRange(value, 1, maxAbilitiesIndexCleanupBatchSize)
	default:
		return nil
	}
}

func validateAbilitiesIndexCleanupRange(value string, minValue int, maxValue int) error {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minValue || parsed > maxValue {
		return fmt.Errorf("value must be an integer between %d and %d", minValue, maxValue)
	}
	return nil
}
