package controller

import dbmodel "github.com/songquanpeng/one-api/model"

// getRetrySkipPriorityLevels enforces one extra retry on the highest priority,
// then steps down one priority level per subsequent retry.
func getRetrySkipPriorityLevels(attempt int) int {
	if attempt <= 1 {
		return 0
	}
	return attempt - 1
}

func appendUniqueChannelID(channelIDs []int, channelID int) []int {
	if channelID <= 0 {
		return channelIDs
	}
	for _, existingChannelID := range channelIDs {
		if existingChannelID == channelID {
			return channelIDs
		}
	}
	return append(channelIDs, channelID)
}

func selectRetryChannel(group string, model string, attempt int, responseID string, failedChannelIds []int) (*dbmodel.Channel, error) {
	return dbmodel.CacheGetRandomSatisfiedChannel(
		group,
		model,
		getRetrySkipPriorityLevels(attempt),
		responseID,
		failedChannelIds,
	)
}

func getLastRetryFallbackChannel(channelID int) *dbmodel.Channel {
	if channelID <= 0 {
		return nil
	}
	if channel, err := dbmodel.CacheGetChannel(channelID); err == nil {
		return channel
	}
	if channel, err := dbmodel.GetChannelById(channelID, true); err == nil {
		return channel
	}
	return nil
}
