package model

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// Snapshot once before asynchronous reading or any subsequent channel change.
func RelayResponseOrigin(c *gin.Context, resp *http.Response, responseID string) ResponseOrigin {
	region := ""
	if resp != nil {
		region = resp.Header.Get("x-ms-region")
	}
	if region == "" {
		if value, ok := c.Get("Config"); ok {
			if cfg, ok := value.(ChannelConfig); ok {
				region = cfg.Region
			}
		}
	}
	return ResponseOrigin{ResponseID: responseID, ChannelID: c.GetInt("channel_id"), KeyIndex: c.GetInt("key_index"), ChannelType: c.GetInt("channel"), Region: normalizeResponseRegion(region)}
}

func RegisterResponseOrigin(origin ResponseOrigin, itemIDs ...string) error {
	if origin.ResponseID == "" {
		return nil
	}
	var firstErr error
	ids := append([]string{origin.ResponseID}, itemIDs...)
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if err := CacheResponseOrigin(id, origin); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
