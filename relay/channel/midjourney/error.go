package midjourney

func MidjourneyErrorWrapper(code int, desc string) *MidjourneyResponse {
	return &MidjourneyResponse{
		Code:        code,
		Description: desc,
	}
}

func MidjourneyErrorWithStatusCodeWrapper(code int, desc string, statusCode int) *MidjourneyResponseWithStatusCode {
	return &MidjourneyResponseWithStatusCode{
		StatusCode: statusCode,
		Response:   *MidjourneyErrorWrapper(code, desc),
	}
}
