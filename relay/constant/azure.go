package constant

import "time"

// AzureNoRemoveDotTime 该时间之后创建的 Azure 渠道，部署名保留小数点（如 gpt-5.2），不再移除
var AzureNoRemoveDotTime = time.Date(2025, time.May, 10, 0, 0, 0, 0, time.UTC).Unix()
