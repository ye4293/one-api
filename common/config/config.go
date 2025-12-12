package config

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/songquanpeng/one-api/common/env"
)

var GeminiVersion = "v1/beta"
var SystemName = "EZLINK AI"
var ServerAddress = "http://localhost:3000"
var Footer = ""
var Logo = ""
var TopUpLink = ""
var ChatLink = ""
var QuotaPerUnit = 500 * 1000.0 // $0.002 / 1K tokens
var DisplayInCurrencyEnabled = true
var DisplayTokenStatEnabled = true

// Any options with "Secret", "Token" in its key won't be return by GetOptions

var SessionSecret = uuid.New().String()

var OptionMap map[string]string
var OptionMapRWMutex sync.RWMutex

var ItemsPerPage = 10
var MaxRecentItems = 100

var PasswordLoginEnabled = true
var PasswordRegisterEnabled = true
var EmailVerificationEnabled = false
var GitHubOAuthEnabled = false
var WeChatAuthEnabled = false
var TurnstileCheckEnabled = false
var RegisterEnabled = true

var CryptPaymentEnabled = false
var StripePaymentEnabled = false
var CryptCallbackUrl = ""
var AddressOut = ""
var StripeCallbackUrl = ""
var StripePrivateKey = ""
var StripePublicKey = ""
var StripeEndpointSecret = ""

var CfR2storeEnabled = true
var CfBucketFileName = "ezlinkai-file"
var CfFileAccessKey = "42a3d63d1371f46956f7d3de36b3b9a5"
var CfFileSecretKey = "31db6128dbf10f3a4a823cea1e52af23934e77353d06f1d1f966288e217073f9"
var CfFileEndpoint = "https://f19328743901865dd8223e016b2ff78d.r2.cloudflarestorage.com"

var CfBucketImageName = ""
var CfImageAccessKey = ""
var CfImageSecretKey = ""
var CfImageEndpoint = ""

var EmailDomainRestrictionEnabled = false
var EmailDomainWhitelist = []string{
	"gmail.com",
	"163.com",
	"126.com",
	"qq.com",
	"outlook.com",
	"hotmail.com",
	"icloud.com",
	"yahoo.com",
	"foxmail.com",
}

var DebugEnabled = strings.ToLower(os.Getenv("DEBUG")) == "true"
var DebugSQLEnabled = strings.ToLower(os.Getenv("DEBUG_SQL")) == "true"
var MemoryCacheEnabled = strings.ToLower(os.Getenv("MEMORY_CACHE_ENABLED")) == "true"

var LogConsumeEnabled = true

var SMTPServer = ""
var SMTPPort = 587
var SMTPAccount = ""
var SMTPFrom = ""
var SMTPToken = ""

var GitHubClientId = ""
var GitHubClientSecret = ""
var GithubRedirectUri = ""

var GoogleOAuthEnabled = true
var GoogleClientId = ""
var GoogleClientSecret = ""
var GoogleRedirectUri = ""
var StripeKey = ""

var WeChatServerAddress = ""
var WeChatServerToken = ""
var WeChatAccountQRCodeImageURL = ""

var MessagePusherAddress = ""
var MessagePusherToken = ""

var TurnstileSiteKey = ""
var TurnstileSecretKey = ""

var QuotaForNewUser int64 = 0
var QuotaForInviter int64 = 0
var QuotaForInvitee int64 = 0
var ChannelDisableThreshold = 5.0
var AutomaticDisableChannelEnabled = false
var AutomaticEnableChannelEnabled = false
var FeiShuHookUrl = ""
var PingIntervalEnabled = false
var PingIntervalSeconds = 0
// 自动禁用关键词配置（一行一个关键词）
var AutoDisableKeywords = `api key not valid
invalid_api_key
incorrect api key provided
authentication_error
permission denied
account_deactivated
insufficient_quota
credit balance is too low
not_enough_credits
credit
balance
used all available credits
reached its monthly spending limit
resource pack exhausted
billing to be enabled
permission_denied
unauthenticated
operation not allowed
organization has been disabled
consumer
has been suspended
service account
project not found
billing account
imagen api
generativelanguage.googleapis.com
console.x.ai`
var QuotaRemindThreshold int64 = 1000
var PreConsumedQuota int64 = 500
var ApproximateTokenEnabled = false
var RetryTimes = 0

var RootUserEmail = ""

var IsMasterNode = os.Getenv("NODE_TYPE") != "slave"

var requestInterval, _ = strconv.Atoi(os.Getenv("POLLING_INTERVAL"))
var RequestInterval = time.Duration(requestInterval) * time.Second

var SyncFrequency = env.Int("SYNC_FREQUENCY", 10*60) // unit is second

var BatchUpdateEnabled = false
var BatchUpdateInterval = env.Int("BATCH_UPDATE_INTERVAL", 5)

var RelayTimeout = env.Int("RELAY_TIMEOUT", 0) // unit is second

var GeminiSafetySetting = env.String("GEMINI_SAFETY_SETTING", "BLOCK_NONE")

var Theme = env.String("THEME", "default")
var ValidThemes = map[string]bool{
	"default": true,
	"berry":   true,
	"air":     true,
}

// All duration's unit is seconds
// Shouldn't larger then RateLimitKeyExpirationDuration
var (
	GlobalApiRateLimitNum            = env.Int("GLOBAL_API_RATE_LIMIT", 180000)
	GlobalApiRateLimitDuration int64 = 30 * 60

	GlobalWebRateLimitNum            = env.Int("GLOBAL_WEB_RATE_LIMIT", 6000)
	GlobalWebRateLimitDuration int64 = 30 * 60

	UploadRateLimitNum            = 10
	UploadRateLimitDuration int64 = 600

	DownloadRateLimitNum            = 10
	DownloadRateLimitDuration int64 = 60

	CriticalRateLimitNum            = 200
	CriticalRateLimitDuration int64 = 200 * 60
)

var RateLimitKeyExpirationDuration = 20 * time.Minute

var EnableMetric = env.Bool("ENABLE_METRIC", false)
var MetricQueueSize = env.Int("METRIC_QUEUE_SIZE", 10)
var MetricSuccessRateThreshold = env.Float64("METRIC_SUCCESS_RATE_THRESHOLD", 0.8)
var MetricSuccessChanSize = env.Int("METRIC_SUCCESS_CHAN_SIZE", 1024)
var MetricFailChanSize = env.Int("METRIC_FAIL_CHAN_SIZE", 128)

var InitialRootToken = os.Getenv("INITIAL_ROOT_TOKEN")
