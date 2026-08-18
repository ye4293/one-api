module github.com/changshiaos/charge/server/shipper

// Keep the producer SDK compatible with one-api's current Go/toolchain line.
// The SDK itself does not require the consumer service's Go 1.26 baseline.
go 1.25.0

require (
	github.com/aws/aws-sdk-go-v2 v1.43.4
	github.com/aws/aws-sdk-go-v2/config v1.27.15
	github.com/aws/aws-sdk-go-v2/service/sqs v1.46.3
	github.com/aws/smithy-go v1.27.6
)

require (
	github.com/aws/aws-sdk-go-v2/credentials v1.17.15 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.11.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.11.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.20.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.24.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.28.9 // indirect
)
