package monitoring

// File overview:
// - Responsibility: collects observability primitives for metrics, keeping metrics registration and reporting in one place.
// - Key symbols: `RegisterMetrics`, `MetricsHandler`.
// - Pattern: Keeps transport concerns at the HTTP boundary: requests are parsed and validated here before business work is delegated deeper into services or the ORM runtime.

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"
)

// Define Prometheus metrics
var (
	// HTTPRequestsTotal covers every HTTP request the service handles, not just
	// IPN callbacks, so the health page has real traffic numbers to show.
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed",
		},
		[]string{"method", "status"},
	)

	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	metricsRegisterOnce sync.Once
	metricsHandlerOnce  sync.Once
	metricsHandler      fiber.Handler

	startedAt    = time.Now()
	requestCount int64
	errorCount   int64
)

// RegisterMetrics initializes Prometheus metrics
func RegisterMetrics() {
	// Metrics are process-global, so registration must happen once even when
	// tests or multiple bootstrap paths call setup repeatedly.
	metricsRegisterOnce.Do(func() {
		prometheus.MustRegister(HTTPRequestsTotal, HTTPRequestDuration)
	})
}

// Middleware records per-request Prometheus metrics and lightweight in-process
// counters so the admin health page can show live totals without scraping
// and parsing the /metrics text output.
func Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		if err != nil {
			// The status of a failed request is only set once the app's error
			// handler runs, which Fiber does after this middleware returns; run it
			// here (as Fiber's own logger middleware does) so error responses are
			// counted with their real status instead of the default 200.
			if handlerErr := c.App().Config().ErrorHandler(c, err); handlerErr != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
			err = nil
		}

		status := c.Response().StatusCode()
		HTTPRequestsTotal.WithLabelValues(c.Method(), strconv.Itoa(status)).Inc()
		HTTPRequestDuration.WithLabelValues(c.Method()).Observe(time.Since(start).Seconds())

		atomic.AddInt64(&requestCount, 1)
		if status >= 500 {
			atomic.AddInt64(&errorCount, 1)
		}
		return err
	}
}

// Uptime reports how long the process has been running.
func Uptime() time.Duration {
	return time.Since(startedAt)
}

// RequestCount reports the total number of HTTP requests handled since startup.
func RequestCount() int64 {
	return atomic.LoadInt64(&requestCount)
}

// ErrorCount reports the total number of HTTP requests that ended in a 5xx status.
func ErrorCount() int64 {
	return atomic.LoadInt64(&errorCount)
}

// MetricsHandler exposes the Prometheus metrics endpoint with Fiber adaptation
func MetricsHandler() fiber.Handler {
	// The Prometheus adapter is stateless, so reuse one Fiber handler instead
	// of rebuilding the adaptor for every route-registration call.
	metricsHandlerOnce.Do(func() {
		prometheusHandler := fasthttpadaptor.NewFastHTTPHandler(promhttp.Handler())
		metricsHandler = func(c fiber.Ctx) error {
			prometheusHandler(c.RequestCtx())
			return nil
		}
	})
	return metricsHandler
}

// fiber:context-methods migrated
