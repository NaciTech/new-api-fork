package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGetRandomSatisfiedChannelFilteredDoesNotMutateCachedChannels(t *testing.T) {
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldGroup2model2channels := group2model2channels
	oldChannelsIDM := channelsIDM
	t.Cleanup(func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		group2model2channels = oldGroup2model2channels
		channelsIDM = oldChannelsIDM
	})

	common.MemoryCacheEnabled = true
	group2model2channels = map[string]map[string][]int{
		"default": {
			"gpt-test": []int{1, 2, 3},
		},
	}
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Status: common.ChannelStatusEnabled},
		2: {Id: 2, Status: common.ChannelStatusEnabled},
		3: {Id: 3, Status: common.ChannelStatusEnabled},
	}

	channel, err := GetRandomSatisfiedChannelFiltered("default", "gpt-test", 0, func(channel *Channel) bool {
		return channel.Id != 1
	})

	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Contains(t, []int{2, 3}, channel.Id)
	require.Equal(t, []int{1, 2, 3}, group2model2channels["default"]["gpt-test"])
}
