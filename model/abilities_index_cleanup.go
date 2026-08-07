package model

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

func channelDisabledLongEnough(channel *Channel, now time.Time, autoDisabledThreshold time.Duration) bool {
	if channel == nil {
		return false
	}
	switch channel.Status {
	case common.ChannelStatusManuallyDisabled:
		return true
	case common.ChannelStatusAutoDisabled:
		statusTime := channel.GetStatusTime()
		if statusTime <= 0 || statusTime > now.Unix() {
			return false
		}
		return now.Unix()-statusTime >= int64(autoDisabledThreshold/time.Second)
	default:
		return false
	}
}

func abilitiesIndexCleanupThreshold() (time.Duration, bool) {
	setting := operation_setting.GetAbilitiesIndexCleanupSetting()
	if !setting.Enabled {
		return 0, false
	}
	return time.Duration(setting.AutoDisabledThresholdHours) * time.Hour, true
}

func channelAbilitiesExpired(channel *Channel, now time.Time) bool {
	threshold, enabled := abilitiesIndexCleanupThreshold()
	return enabled && channelDisabledLongEnough(channel, now, threshold)
}

func withExistingChannelAbilities(query *gorm.DB) *gorm.DB {
	abilityExists := DB.Session(&gorm.Session{NewDB: true}).
		Model(&Ability{}).
		Select("1").
		Where("abilities.channel_id = channels.id")
	return query.Where("EXISTS (?)", abilityExists)
}

func pruneExpiredChannelAbilities(ctx context.Context, channelID int, now time.Time, autoDisabledThreshold time.Duration) (eligible bool, removed bool, err error) {
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		channel := &Channel{}
		if err := lockForUpdate(tx).
			Select("id", "status", "other_info").
			First(channel, "id = ?", channelID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if !channelDisabledLongEnough(channel, now, autoDisabledThreshold) {
			return nil
		}
		eligible = true
		result := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{})
		if result.Error != nil {
			return result.Error
		}
		removed = result.RowsAffected > 0
		return nil
	})
	return eligible, removed, err
}

func RunAbilitiesIndexCleanupBatch(ctx context.Context, afterID, batchSize int, now time.Time, autoDisabledThreshold time.Duration) (lastID int, scanned int, pruned int, hasMore bool, err error) {
	if batchSize <= 0 {
		return afterID, 0, 0, false, fmt.Errorf("abilities index cleanup batch size must be positive")
	}
	if autoDisabledThreshold <= 0 {
		return afterID, 0, 0, false, fmt.Errorf("abilities index cleanup threshold must be positive")
	}
	var channels []Channel
	err = withExistingChannelAbilities(DB.WithContext(ctx)).
		Select("id").
		Where("id > ? AND status IN ?", afterID, []int{common.ChannelStatusManuallyDisabled, common.ChannelStatusAutoDisabled}).
		Order("id ASC").
		Limit(batchSize).
		Find(&channels).Error
	if err != nil {
		return afterID, 0, 0, false, err
	}

	eligibleChannelIDs := make([]int, 0, len(channels))
	for i := range channels {
		lastID = channels[i].Id
		eligible, removed, pruneErr := pruneExpiredChannelAbilities(ctx, channels[i].Id, now, autoDisabledThreshold)
		if pruneErr != nil {
			return lastID, len(channels), pruned, false, fmt.Errorf("prune channel %d abilities: %w", channels[i].Id, pruneErr)
		}
		if eligible {
			eligibleChannelIDs = append(eligibleChannelIDs, channels[i].Id)
		}
		if removed {
			pruned++
		}
	}
	EvictDisabledChannelsFromCache(eligibleChannelIDs)
	return lastID, len(channels), pruned, len(channels) == batchSize, nil
}

func CountAbilitiesIndexCleanupCandidates(ctx context.Context) (int64, error) {
	var total int64
	err := withExistingChannelAbilities(DB.WithContext(ctx)).
		Model(&Channel{}).
		Where("status IN ?", []int{common.ChannelStatusManuallyDisabled, common.ChannelStatusAutoDisabled}).
		Count(&total).Error
	return total, err
}
