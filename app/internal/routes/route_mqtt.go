package routes

// File overview:
// - Responsibility: implements Fiber transport handlers and request-bound orchestration for the route mqtt surface.
// - Key symbols: `PublishMessage`.
// - Pattern: Keeps transport concerns at the HTTP boundary: requests are parsed and validated here before business work is delegated deeper into services or the ORM runtime.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/services/mqtt"
	"github.com/gofiber/fiber/v3"
)

// mqttPublishTimeout bounds broker connect + PUBACK wait so a stalled broker never pins the request.
const mqttPublishTimeout = 10 * time.Second

// PublishMessage publishes the request message at QoS 1 and reports whether the broker
// routed it to at least one subscriber (MQTT 5 PUBACK reason code).
func PublishMessage(c fiber.Ctx) error {
	var req mqtt.PublishRequest

	if err := c.Bind().Body(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request payload",
		})
	}

	if req.Topic == "" || req.Message == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Both topic and message are required",
		})
	}

	// Marshal message to JSON string
	messageBytes, err := json.Marshal(req.Message)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Failed to serialize message to JSON string",
		})
	}

	ctx, cancel := context.WithTimeout(c.Context(), mqttPublishTimeout)
	defer cancel()
	receipt, err := mqtt.PublishWithReceipt(ctx, req.Topic, messageBytes)
	// A timeout here may still have reached the broker: the caller sees an error, not a guaranteed non-delivery.
	if err != nil {
		log.Errorf("mqtt publish failed topic=%s: %v", req.Topic, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"status": "error",
			"error":  fmt.Sprintf("Failed to publish: %v", err),
		})
	}

	// Publish succeeded either way; has_subscribers tells the caller whether anyone was listening.
	notice := "Message published and delivered to at least one subscriber"
	if !receipt.HasSubscribers {
		notice = "Message published but no client is subscribed to this topic"
	}
	log.Infof("mqtt published topic=%s reason_code=0x%02x has_subscribers=%t", req.Topic, receipt.ReasonCode, receipt.HasSubscribers)
	return c.JSON(fiber.Map{
		"status":          "success",
		"topic":           req.Topic,
		"message":         req.Message,
		"published":       true,
		"has_subscribers": receipt.HasSubscribers,
		"reason_code":     receipt.ReasonCode,
		"notice":          notice,
	})
}
