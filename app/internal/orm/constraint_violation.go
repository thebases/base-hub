package orm

// File overview:
// - Responsibility: lets a registered translator rewrite a raw PostgreSQL constraint-violation error into a
//   caller-declared friendly message (sys.model.constraint's runtime substitution) before it reaches the API
//   response. orm cannot import internal/models (models imports orm), so the actual name->message lookup lives
//   in models and registers itself here through this hook, mirroring RegisterModelChangeLogger/RegisterConstraint.
// - Key symbols: `RegisterConstraintViolationTranslator`, `translateConstraintViolation`.

import (
	"context"
	"errors"
	"sync"
)

// ConstraintViolationTranslator inspects a write error and, if it matches a
// known PostgreSQL constraint violation, returns a friendly replacement
// message.
type ConstraintViolationTranslator func(ctx context.Context, err error) (string, bool)

var constraintViolationTranslatorRegistry struct {
	mu sync.RWMutex
	fn ConstraintViolationTranslator
}

// RegisterConstraintViolationTranslator installs the one translator used by
// every runtime create/write call. Only one is expected in this codebase
// (sys.model.constraint); a later call replaces the previous one.
func RegisterConstraintViolationTranslator(fn ConstraintViolationTranslator) {
	constraintViolationTranslatorRegistry.mu.Lock()
	defer constraintViolationTranslatorRegistry.mu.Unlock()
	constraintViolationTranslatorRegistry.fn = fn
}

// translateConstraintViolation runs the registered translator, if any, and
// swaps in its friendly message on a match; otherwise it returns err unchanged.
func translateConstraintViolation(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	constraintViolationTranslatorRegistry.mu.RLock()
	fn := constraintViolationTranslatorRegistry.fn
	constraintViolationTranslatorRegistry.mu.RUnlock()
	if fn == nil {
		return err
	}
	message, ok := fn(ctx, err)
	if !ok || message == "" {
		return err
	}
	return errors.New(message)
}
