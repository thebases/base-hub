package routes

// File overview:
// - Responsibility: renders every API failure as the JSON-RPC style error body.
// - Key symbols: `APIErrorHandler`, `RecoverMiddleware`, `writeAPIError`.
// - Contract: always JSON `{id,error:{code,message,data?}}`; there is no legacy text/plain format.

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
)

const (
	localsRequestID = "rpc_request_id"
)

// setRequestID remembers the client's optional request id so success and error bodies can echo it.
// The response id is always a string: a client number or boolean is echoed as its text, and anything else (null, empty, object) means "absent" and a generated id is used.
func setRequestID(c fiber.Ctx, id any) {
	switch typed := id.(type) {
	case string:
		if typed != "" {
			c.Locals(localsRequestID, typed)
		}
	case float64:
		c.Locals(localsRequestID, strconv.FormatFloat(typed, 'f', -1, 64))
	case bool:
		c.Locals(localsRequestID, strconv.FormatBool(typed))
	default:
		c.Locals(localsRequestID, nil)
	}
}

// requestID returns the id every response carries: the client's own id when it sent one,
// otherwise a generated one (never null). It is generated once per request, so a success or
// error body and the logs agree.
func requestID(c fiber.Ctx) any {
	if id := c.Locals(localsRequestID); id != nil {
		return id
	}
	id := generateRequestID(time.Now())
	c.Locals(localsRequestID, id)
	return id
}

// requestIDSeq makes ids unique within a process even inside one millisecond.
var requestIDSeq atomic.Uint64

// requestIDInstance is a 3-digit segment derived from hostname, pid and start time, so two
// backend instances stamping the same millisecond and sequence still differ.
var requestIDInstance = func() int {
	host, _ := os.Hostname()
	sum := fnv.New32a()
	fmt.Fprintf(sum, "%s/%d/%d", host, os.Getpid(), time.Now().UnixNano())
	return int(sum.Sum32() % 1000)
}()

// generateRequestID builds the 24-digit id: UTC "ddmmyyyyhhMMss" + 3 ms digits + 3 instance digits
// + 4 sequence digits. The sequence cannot repeat within a millisecond in one process.
func generateRequestID(now time.Time) string {
	return fmt.Sprintf("%s%03d%03d%04d", now.UTC().Format("02012006150405"), now.UTC().Nanosecond()/1e6, requestIDInstance, requestIDSeq.Add(1)%10000)
}

// writeAPIError sends the v2 error body with the typed error's HTTP status.
func writeAPIError(c fiber.Ctx, apiErr *orm.Error) error {
	message := apiErr.Message
	if apiErr.Code == orm.ErrInternal {
		message = orm.InternalErrorMessage
	}
	detail := fiber.Map{"code": apiErr.Code, "message": message}
	if len(apiErr.Data) > 0 {
		detail["data"] = apiErr.Data
	}
	// Marshalled directly so null values keep their JSON null (the app encoder rewrites null to false).
	payload, err := json.Marshal(fiber.Map{"id": requestID(c), "error": detail})
	if err != nil {
		return err
	}
	return c.Status(apiErr.Status).Type("json").Send(payload)
}

// toAPIError classifies a handler failure: fiber.Error by status, everything else via orm.ErrorFrom.
func toAPIError(err error) *orm.Error {
	var typed *orm.Error
	var aliasErr *orm.AliasError
	var fiberErr *fiber.Error
	if !errors.As(err, &typed) && !errors.As(err, &aliasErr) && errors.As(err, &fiberErr) {
		return &orm.Error{Code: orm.ErrorCodeForStatus(fiberErr.Code), Status: fiberErr.Code, Message: fiberErr.Message}
	}
	return orm.ErrorFrom(err)
}

// APIErrorHandler is the app-wide Fiber ErrorHandler: every failure is the JSON-RPC error body.
func APIErrorHandler(c fiber.Ctx, err error) error {
	return writeAPIError(c, toAPIError(err))
}

// RecoverMiddleware turns a panic into a logged -32603 instead of crashing the process; the panic
// text never reaches the client.
func RecoverMiddleware(c fiber.Ctx) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("panic serving %s %s: %v", c.Method(), c.Path(), r)
			err = orm.NewError(orm.ErrInternal, 0, orm.InternalErrorMessage)
		}
	}()
	return c.Next()
}
