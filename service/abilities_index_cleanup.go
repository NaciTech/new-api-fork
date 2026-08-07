package service

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

type AbilitiesIndexCleanupPayload struct {
	AutoDisabledThresholdHours int `json:"auto_disabled_threshold_hours"`
	BatchSize                  int `json:"batch_size"`
}

type AbilitiesIndexCleanupResult struct {
	Scanned int `json:"scanned"`
	Pruned  int `json:"pruned"`
}

func NewAbilitiesIndexCleanupPayload() AbilitiesIndexCleanupPayload {
	setting := operation_setting.GetAbilitiesIndexCleanupSetting()
	return AbilitiesIndexCleanupPayload{
		AutoDisabledThresholdHours: setting.AutoDisabledThresholdHours,
		BatchSize:                  setting.BatchSize,
	}
}

func RunAbilitiesIndexCleanup(ctx context.Context, payload AbilitiesIndexCleanupPayload, progress func(processed, total int)) (AbilitiesIndexCleanupResult, error) {
	setting := operation_setting.GetAbilitiesIndexCleanupSetting()
	if payload.AutoDisabledThresholdHours <= 0 {
		payload.AutoDisabledThresholdHours = setting.AutoDisabledThresholdHours
	}
	if payload.BatchSize <= 0 || payload.BatchSize > 1000 {
		payload.BatchSize = setting.BatchSize
	}
	totalCandidates, err := model.CountAbilitiesIndexCleanupCandidates(ctx)
	if err != nil {
		return AbilitiesIndexCleanupResult{}, err
	}

	result := AbilitiesIndexCleanupResult{}
	afterID := 0
	now := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		lastID, scanned, pruned, hasMore, err := model.RunAbilitiesIndexCleanupBatch(
			ctx,
			afterID,
			payload.BatchSize,
			now,
			time.Duration(payload.AutoDisabledThresholdHours)*time.Hour,
		)
		if err != nil {
			return result, err
		}
		result.Scanned += scanned
		result.Pruned += pruned
		if progress != nil {
			progress(result.Scanned, int(totalCandidates))
		}
		if !hasMore {
			return result, nil
		}
		if lastID <= afterID {
			return result, fmt.Errorf("abilities index cleanup made no pagination progress")
		}
		afterID = lastID
	}
}
