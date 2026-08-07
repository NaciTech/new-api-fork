package model_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAbilitiesIndexCleanupPrunesEligibleChannelsWithoutRebuildingCache(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))

	settingConfig := config.GlobalConfig.Get("abilities_index_cleanup_setting")
	require.NotNil(t, settingConfig)
	previousSetting, err := config.ConfigToMap(settingConfig)
	require.NoError(t, err)
	require.NoError(t, config.UpdateConfigFromMap(settingConfig, map[string]string{
		"enabled":                       "false",
		"auto_disabled_threshold_hours": "24",
		"batch_size":                    "100",
	}))

	previousDB := model.DB
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	model.DB = db
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(settingConfig, previousSetting))
		common.MemoryCacheEnabled = false
		model.DB = previousDB
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
	})

	now := time.Unix(1_800_000_000, 0)
	cleanupTag := "cleanup-tag"
	channels := []model.Channel{
		{Id: 1, Status: common.ChannelStatusAutoDisabled, Name: "expired", Key: "key", Group: "default", Models: "model-expired"},
		{Id: 2, Status: common.ChannelStatusAutoDisabled, Name: "recent", Key: "key", Group: "default", Models: "model-a"},
		{Id: 3, Status: common.ChannelStatusManuallyDisabled, Name: "manual", Key: "key", Group: "default", Models: "model-manual", Tag: &cleanupTag},
		{Id: 4, Status: common.ChannelStatusEnabled, Name: "enabled", Key: "key", Group: "default", Models: "model-a"},
		{Id: 5, Status: common.ChannelStatusAutoDisabled, Name: "missing-time", Key: "key", Group: "default", Models: "model-a"},
		{Id: 6, Status: common.ChannelStatusAutoDisabled, Name: "future-time", Key: "key", Group: "default", Models: "model-a"},
	}
	channels[0].SetOtherInfo(map[string]interface{}{"status_time": now.Add(-25 * time.Hour).Unix()})
	channels[1].SetOtherInfo(map[string]interface{}{"status_time": now.Add(-23 * time.Hour).Unix()})
	channels[5].SetOtherInfo(map[string]interface{}{"status_time": now.Add(time.Hour).Unix()})
	require.NoError(t, db.Create(&channels).Error)
	for i := range channels {
		require.NoError(t, db.Create(&model.Ability{
			Group:     "default",
			Model:     "model-" + strconv.Itoa(channels[i].Id),
			ChannelId: channels[i].Id,
			Enabled:   channels[i].Status == common.ChannelStatusEnabled,
		}).Error)
	}

	model.InitChannelCache()
	cachedExpired, err := model.CacheGetChannel(1)
	require.NoError(t, err)
	cachedEnabled, err := model.CacheGetChannel(4)
	require.NoError(t, err)
	require.NoError(t, config.UpdateConfigFromMap(settingConfig, map[string]string{
		"enabled": "true",
	}))
	totalCandidates, err := model.CountAbilitiesIndexCleanupCandidates(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 5, totalCandidates)

	lastID, scanned, pruned, hasMore, err := model.RunAbilitiesIndexCleanupBatch(context.Background(), 0, 100, now, 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 6, lastID)
	assert.Equal(t, 5, scanned)
	assert.Equal(t, 2, pruned)
	assert.False(t, hasMore)

	for _, channelID := range []int{1, 3} {
		var count int64
		require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", channelID).Count(&count).Error)
		assert.Zero(t, count)
	}
	for _, channelID := range []int{2, 4, 5, 6} {
		var count int64
		require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", channelID).Count(&count).Error)
		assert.EqualValues(t, 1, count)
	}

	secondLastID, secondScanned, secondPruned, secondHasMore, err := model.RunAbilitiesIndexCleanupBatch(context.Background(), 0, 100, now, 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 6, secondLastID)
	assert.Equal(t, 3, secondScanned, "channels whose abilities were already removed must not be rescanned")
	assert.Zero(t, secondPruned)
	assert.False(t, secondHasMore)
	totalCandidates, err = model.CountAbilitiesIndexCleanupCandidates(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 3, totalCandidates)

	stillCachedEnabled, err := model.CacheGetChannel(4)
	require.NoError(t, err)
	assert.Same(t, cachedEnabled, stillCachedEnabled)
	fallbackExpired, err := model.CacheGetChannel(1)
	require.NoError(t, err, "evicted disabled channels must remain available through database fallback")
	assert.NotSame(t, cachedExpired, fallbackExpired)

	assert.True(t, model.UpdateChannelStatus(1, "", common.ChannelStatusEnabled, ""))
	var restoredCount int64
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 1).Count(&restoredCount).Error)
	assert.EqualValues(t, 1, restoredCount)
	restored, err := model.CacheGetChannel(1)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, restored.Status)

	model.EvictDisabledChannelsFromCache([]int{1})
	stillRestored, err := model.CacheGetChannel(1)
	require.NoError(t, err)
	assert.Same(t, restored, stillRestored, "a delayed cleanup eviction must preserve a concurrently re-enabled channel")
	routed, err := model.GetRandomSatisfiedChannel("default", "model-expired", 0, "")
	require.NoError(t, err)
	require.NotNil(t, routed)
	assert.Equal(t, 1, routed.Id)

	require.NoError(t, model.EnableChannelByTag("cleanup-tag"))
	var tagRestoredCount int64
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 3).Count(&tagRestoredCount).Error)
	assert.EqualValues(t, 1, tagRestoredCount)
	tagRouted, err := model.GetRandomSatisfiedChannel("default", "model-manual", 0, "")
	require.NoError(t, err)
	require.NotNil(t, tagRouted)
	assert.Equal(t, 3, tagRouted.Id)
}

func TestAbilitiesIndexCleanupRejectsUnboundedBatch(t *testing.T) {
	_, _, _, _, err := model.RunAbilitiesIndexCleanupBatch(context.Background(), 0, 0, time.Now(), time.Hour)
	assert.Error(t, err)
}

func TestChannelReenableRollsBackWhenAbilityRebuildFails(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))

	previousDB := model.DB
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	model.DB = db
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
	})

	channel := model.Channel{
		Id:     1,
		Status: common.ChannelStatusAutoDisabled,
		Name:   "rollback-on-ability-failure",
		Key:    "key",
		Group:  "default",
		Models: "model-a",
	}
	require.NoError(t, db.Create(&channel).Error)

	assert.False(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusEnabled, ""))

	var persisted model.Channel
	require.NoError(t, db.First(&persisted, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, persisted.Status)
}
