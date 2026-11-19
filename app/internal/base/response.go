package base

// File overview:
// - Responsibility: captures shared API response and constant primitives for response, keeping cross-cutting behavior consistent across routes.
// - Key symbols: `ServerError`, `Success`, `HTTPResponse`.
// - Pattern: Keeps transport concerns at the HTTP boundary: requests are parsed and validated here before business work is delegated deeper into services or the ORM runtime.

import (
	"fmt"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
)

type HTTPResponse struct {
	Code    interface{} `json:"code"`
	Message interface{} `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

func ServerError(c fiber.Ctx, responseCode int, errMessage ...string) error {
	message := GetCodeMeaning(responseCode)
	if len(errMessage) > 0 && errMessage[0] != "" {
		message = errMessage[0]
	}
	errorJson := &HTTPResponse{
		Code:    responseCode,
		Message: message,
	}
	log.Errorf("[Code] %d [Message] %s", responseCode, message)
	return c.Status(500).JSON(errorJson)
}

func Success(c fiber.Ctx, data interface{}) error {
	successJson := &HTTPResponse{
		Code: SuccessCode,
		Data: data,
	}
	log.Infof("[Code] 200 [Data] %s", fmt.Sprintf("%v", data))
	return c.Status(200).JSON(successJson)
}
