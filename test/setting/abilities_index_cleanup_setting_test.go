package setting_test

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAbilitiesIndexCleanupSetting(t *testing.T) {
	validValues := map[string]string{
		"abilities_index_cleanup_setting.enabled":                       "true",
		"abilities_index_cleanup_setting.interval_hours":                "720",
		"abilities_index_cleanup_setting.auto_disabled_threshold_hours": "8760",
		"abilities_index_cleanup_setting.batch_size":                    "1000",
	}
	for key, value := range validValues {
		require.NoError(t, operation_setting.ValidateAbilitiesIndexCleanupSetting(key, value), key)
	}

	invalidValues := map[string]string{
		"abilities_index_cleanup_setting.enabled":                       "yes",
		"abilities_index_cleanup_setting.interval_hours":                "0",
		"abilities_index_cleanup_setting.auto_disabled_threshold_hours": "8761",
		"abilities_index_cleanup_setting.batch_size":                    "1.5",
	}
	for key, value := range invalidValues {
		assert.Error(t, operation_setting.ValidateAbilitiesIndexCleanupSetting(key, value), key)
	}
}
