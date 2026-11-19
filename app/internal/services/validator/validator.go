package validator

// File overview:
// - Responsibility: encapsulates domain/service orchestration for validator, keeping integrations and multi-step business flows out of transport handlers.
// - Key symbols: `NewValidator`, `ParseValidationError`, `ValidationError`.
// - Pattern: Keeps transport concerns at the HTTP boundary: requests are parsed and validated here before business work is delegated deeper into services or the ORM runtime.

import (
	"encoding/json"
	"fmt"
	"io"
	"log"

	"github.com/go-playground/validator/v10"
	// "github.com/goccy/go-json"
	"github.com/gofiber/fiber/v3"
)

// Base validator instance
var baseValidator = validator.New()

// ValidationError represents a validation error
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// NewValidator initializes the validator instance
func NewValidator() *validator.Validate {
	return baseValidator
}

// ParseValidationError parses validation errors into a readable format
func ParseValidationError(err error) []ValidationError {
	var validationErrors []ValidationError

	// Walk each validationerrors) entry in order so validation and side effects stay scoped to the current element.
	for _, e := range err.(validator.ValidationErrors) {
		validationErrors = append(validationErrors, ValidationError{
			Field:   e.Field(),
			Message: fmt.Sprintf("Field '%s' failed validation on '%s'", e.Field(), e.Tag()),
		})
	}
	return validationErrors
}

// ErrorHandle sends a JSON response with validation errors
func ErrorHandle(ctx fiber.Ctx, err error) error {
	return ctx.Status(fiber.StatusBadRequest).JSON(fiber.Map{
		"errors": ParseValidationError(err),
	})
}

// ValidateJSON validates JSON input and ensures it meets struct requirements
func ValidateJSON(ctx fiber.Ctx, t any) error {
	// Parse JSON request body
	if err := json.Unmarshal(ctx.Body(), &t); err != nil {
		// Handle JSON syntax errors
		if syntaxErr, ok := err.(*json.SyntaxError); ok {
			log.Printf("JSON Syntax Error at byte %d: %s\n", syntaxErr.Offset, syntaxErr.Error())
			ctx.Locals("error", fiber.Map{
				"message": fmt.Sprintf("Invalid JSON at byte %d", syntaxErr.Offset),
			})
		}

		// Handle empty JSON body
		if err == io.EOF {
			log.Println("Empty JSON body")
			ctx.Locals("error", fiber.Map{
				"message": "Empty JSON body",
			})
		}

		// Handle other decoding errors
		log.Println("JSON Decode Error:", err)
		ctx.Locals("error", fiber.Map{
			"message": "Invalid JSON",
		})
		return err
	}

	// Log the parsed JSON
	log.Println("Parsed JSON:", t)

	// Perform struct validation
	if err := baseValidator.Struct(t); err != nil {
		// Handle validation errors
		if validationErrs, ok := err.(validator.ValidationErrors); ok {
			validationMessages := make([]string, 0)
			for _, e := range validationErrs {
				validationMessages = append(validationMessages, fmt.Sprintf("%s is required", e.Field()))
			}

			log.Println("Validation Errors:", validationMessages)
			ctx.Locals("error", fiber.Map{
				"message": validationMessages,
			})
			return err
		}

		// Handle other validation errors
		log.Println("Validation Error:", err)
		ctx.Locals("error", fiber.Map{
			"message": "Validation failed",
		})
		return err
	}

	log.Println("Struct Validated:", t)
	return nil
}
