package model

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/songquanpeng/one-api/common"
)

type responseOriginContextKey struct{}
type previousResponseContextKey struct{}
type responseOriginFallbackContextKey struct{}

// ResponseOrigin is the immutable provenance of a response or replayable item.
type ResponseOrigin = ResponseIdRegionInfo

var ErrResponseOriginUnknown = errors.New("response origin is unavailable")
var ErrResponseOriginNotFound = errors.New("response origin was not found")
var ErrResponseOriginUnavailable = errors.New("response origin store is unavailable")
var ErrResponseOriginConflict = errors.New("response origin conflicts with existing provenance")

func WithResponseOrigin(ctx context.Context, origin ResponseOrigin) context.Context {
	return context.WithValue(ctx, responseOriginContextKey{}, origin)
}
func ResponseOriginFromContext(ctx context.Context) (ResponseOrigin, bool) {
	origin, ok := ctx.Value(responseOriginContextKey{}).(ResponseOrigin)
	return origin, ok
}
func WithPreviousResponseID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, previousResponseContextKey{}, id)
}
func PreviousResponseIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(previousResponseContextKey{}).(string)
	return id
}

// WithResponseOriginFallback records the decision for this request, so retry
// cannot rediscover a different parent source if the store recovers mid-request.
// Keep only the error class; backend error details belong in the runtime log.
func WithResponseOriginFallback(ctx context.Context, lookupErr error) context.Context {
	if lookupErr == nil {
		return ctx
	}
	reason := "invalid"
	switch {
	case errors.Is(lookupErr, ErrResponseOriginNotFound):
		reason = "not_found"
	case errors.Is(lookupErr, ErrResponseOriginUnavailable):
		reason = "unavailable"
	}
	return context.WithValue(ctx, responseOriginFallbackContextKey{}, reason)
}

func ResponseOriginFallbackReason(ctx context.Context) string {
	reason, _ := ctx.Value(responseOriginFallbackContextKey{}).(string)
	return reason
}

// LookupResponseOrigin upgrades legacy metadata only from its original channel.
func LookupResponseOrigin(id string) (ResponseOrigin, error) {
	if id == "" {
		return ResponseOrigin{}, ErrResponseOriginUnknown
	}
	if !common.RedisEnabled || common.RDB == nil {
		return ResponseOrigin{}, ErrResponseOriginUnavailable
	}
	value, err := common.RedisGet(fmtResponseCacheKey(id))
	if errors.Is(err, redis.Nil) {
		return ResponseOrigin{}, ErrResponseOriginNotFound
	}
	if err != nil {
		return ResponseOrigin{}, fmt.Errorf("%w: %w", ErrResponseOriginUnavailable, err)
	}
	origin := ParseResponseIdCacheValue(value)
	if origin.ChannelID <= 0 {
		return origin, ErrResponseOriginUnknown
	}
	if origin.ChannelType == 0 && DB != nil {
		if ch, err := CacheGetChannel(origin.ChannelID); err == nil && ch != nil {
			origin.ChannelType = ch.Type
			if origin.Region == "" {
				origin.Region = GetAzureChannelRegion(ch)
			}
		}
	}
	if origin.ChannelType <= 0 {
		return origin, ErrResponseOriginUnknown
	}
	if origin.ResponseID == "" {
		origin.ResponseID = id
	}
	origin.Region = normalizeResponseRegion(origin.Region)
	return origin, nil
}

func ChannelMatchesResponseOrigin(ch *Channel, origin ResponseOrigin) bool {
	if ch == nil || ch.Type != origin.ChannelType {
		return false
	}
	if origin.Region == "" {
		return origin.ChannelType != common.ChannelTypeAzure
	}
	cfg, err := ch.LoadConfig()
	return err == nil && strings.EqualFold(normalizeResponseRegion(cfg.Region), origin.Region)
}

// SelectChannelForResponseOrigin never widens a known source constraint.
func SelectChannelForResponseOrigin(ctx context.Context, group, model string, origin ResponseOrigin, excluded []int) (*Channel, int, error) {
	if origin.ChannelID <= 0 || origin.ChannelType <= 0 {
		return nil, -1, ErrResponseOriginUnknown
	}
	if !isExcludedChannel(origin.ChannelID, excluded) {
		ch, err := CacheGetChannel(origin.ChannelID)
		if err == nil && ch != nil && ch.Type == origin.ChannelType && ch.Status == common.ChannelStatusEnabled &&
			containsResponseValue(ch.Group, group) && containsResponseValue(ch.Models, model) && responseAbilityEnabled(ch.Id, group, model) {
			if origin.KeyIndex >= 0 {
				if _, err := ch.GetKeyByIndex(origin.KeyIndex); err == nil {
					return ch, origin.KeyIndex, nil
				}
			} else if ChannelMatchesResponseOrigin(ch, origin) {
				return ch, -1, nil
			}
		}
	}
	if origin.ChannelType == common.ChannelTypeAzure && origin.Region == "" {
		return nil, -1, ErrResponseOriginUnknown
	}
	// Do not reuse another key of the original resource after its pinned key failed.
	excluded = append(append([]int{}, excluded...), origin.ChannelID)
	ch, err := cacheGetRandomSatisfiedChannelWithFilter(ctx, group, model, func(ch *Channel) bool {
		return ChannelMatchesResponseOrigin(ch, origin)
	}, 0, excluded)
	if err != nil {
		return nil, -1, err
	}
	return ch, -1, nil
}
func containsResponseValue(list, value string) bool {
	for _, item := range strings.Split(list, ",") {
		if strings.TrimSpace(item) == value {
			return true
		}
	}
	return false
}

func responseAbilityEnabled(channelID int, group, model string) bool {
	if DB == nil {
		return false
	}
	var count int64
	groupColumn := "`group`"
	if common.UsingPostgreSQL {
		groupColumn = `"group"`
	}
	err := DB.Model(&Ability{}).Where("channel_id = ? AND model = ? AND enabled = ?", channelID, model, true).Where(groupColumn+" = ?", group).Count(&count).Error
	return err == nil && count > 0
}
