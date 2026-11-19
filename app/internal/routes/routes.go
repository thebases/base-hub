package routes

// File overview:
// - Responsibility: implements Fiber transport handlers and request-bound orchestration for the routes surface.
// - Key symbols: `SetupRoutes`.
// - Pattern: Keeps transport concerns at the HTTP boundary: requests are parsed and validated here before business work is delegated deeper into services or the ORM runtime.
// - Pattern: Reads operational behavior from shared config so environments can tune the same code paths without forking implementation logic.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/monitoring"
	"git.thebase.vn/base_hub/internal/orm"
	kafkaproducer "git.thebase.vn/base_hub/internal/services/kafka/producer"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/services/mqtt"
	"github.com/gofiber/fiber/v3"
)

// SetupRoutes initializes all API routes
func SetupRoutes(app *fiber.App) {
	// Create a nested router group so related endpoints share the same prefix and middleware.
	api := app.Group(config.Get().Server.BasePath) // Protect API with authentication
	api.Get("/metrics", monitoring.MetricsHandler())
	api.Get("/health", handleHealthCheck)
	// Detailed check is RPC-authenticated since it reveals internal dependency
	// state (DB/Kafka/MQTT); the plain /health stays public for LB probes.
	api.Get("/health/detail", handleHealthDetail)

	// Register the RPC compatibility layer before the older
	// task-specific routes so dynamic model transport stays on the canonical
	// RPC endpoints.
	RegisterRPCRoutes(api)
	// Attachment transport (SRS-F-019) shares the RPC auth semantics and the
	// /web/content SPA exclusion already reserved in server.go.
	RegisterWebContentRoutes(api)
	// Report transport (SRS-F-020) reuses the same auth semantics; the /report
	// prefix is excluded from the SPA fallback in server.go.
	RegisterReportRoutes(api)
	// Register this route group now so the backend exposes the full feature surface during startup.
	RegisterAPIDocRoutes(api)
	// Service-bus transport routes (/bus/*, /websocket/*) now live in
	// modules/base-bus and register themselves via kernelmodules.RegisterHTTPRoutes below.

	registerUtilityRoutes(api)
	registerOptionalTransportRoutes(api)

	// Let modules own their HTTP transport wiring so feature-specific routes
	// stay close to the module code that implements them.
	kernelmodules.RegisterHTTPRoutes(app)
	log.Info("Routes registered with authentication")
}

func handleHealthCheck(c fiber.Ctx) error {
	log.Infof("===== HEALTH CHECK =====")
	return c.JSON(fiber.Map{"status": "healthy"})
}

// healthDetailRootCompanyID must stay in sync with models.rootCompanyID: only
// the root company's operators may view control-plane health state.
const healthDetailRootCompanyID int64 = 1

// handleHealthDetail powers the admin "Service Health Check" page: overall
// status plus per-dependency checks and lightweight request stats. Restricted
// to the home/control-plane database, mirroring base.database's guardHomeDB —
// per-tenant service databases must not surface control-plane operational state.
func handleHealthDetail(c fiber.Ctx) error {
	identity, err := requireRPCIdentity(c, false)
	if err != nil {
		return err
	}
	dbName := strings.TrimSpace(identity.DBName)
	homeName := strings.TrimSpace(config.HomeDBName())
	if dbName == "" || dbName != homeName {
		return fiber.NewError(
			fiber.StatusForbidden,
			fmt.Sprintf("health/detail: operation not permitted outside the home database (current: %q, home: %q)", dbName, homeName),
		)
	}
	// This page has no backing model, so it can't go through the ui.menu/model
	// access-handler gate (models/sys_admin_company_access.go) that scopes the
	// rest of the Admin menu to the root company — enforce the same boundary
	// here. Keep healthDetailRootCompanyID in sync with models.rootCompanyID.
	if identity.CompanyID != healthDetailRootCompanyID {
		return fiber.NewError(fiber.StatusForbidden, "health/detail: operation restricted to the root company")
	}

	dbCheck := checkDatabaseHealth(c.Context())
	kafkaCheck := checkKafkaHealth()
	mqttCheck := checkMqttHealth()

	overallStatus := "healthy"
	if dbCheck["status"] == "down" || kafkaCheck["status"] == "down" || mqttCheck["status"] == "down" {
		overallStatus = "degraded"
	}

	return c.JSON(fiber.Map{
		"status":         overallStatus,
		"version":        config.Get().AppVersion(),
		"uptime_seconds": int64(monitoring.Uptime().Seconds()),
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
		"checks": fiber.Map{
			"database": dbCheck,
			"kafka":    kafkaCheck,
			"mqtt":     mqttCheck,
		},
		"stats": fiber.Map{
			"requests_total": monitoring.RequestCount(),
			"errors_total":   monitoring.ErrorCount(),
		},
	})
}

func checkDatabaseHealth(ctx context.Context) fiber.Map {
	db := orm.GetBunDB()
	if db == nil {
		return fiber.Map{"status": "down", "enabled": true, "error": "database not initialized"}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := db.PingContext(pingCtx); err != nil {
		return fiber.Map{"status": "down", "enabled": true, "error": err.Error()}
	}
	return fiber.Map{"status": "up", "enabled": true, "latency_ms": time.Since(start).Milliseconds()}
}

func checkKafkaHealth() fiber.Map {
	if !config.Get().Kafka.Enable {
		return fiber.Map{"status": "disabled", "enabled": false}
	}
	if kafkaproducer.Kafkabroker == nil {
		return fiber.Map{"status": "down", "enabled": true, "error": "producer not initialized"}
	}
	return fiber.Map{"status": "up", "enabled": true}
}

func checkMqttHealth() fiber.Map {
	if !config.Get().MqttClient.Enable {
		return fiber.Map{"status": "disabled", "enabled": false}
	}
	if !mqtt.IsConnected() {
		return fiber.Map{"status": "down", "enabled": true}
	}
	return fiber.Map{"status": "up", "enabled": true}
}

func registerUtilityRoutes(router fiber.Router) {
	// Keep the utility endpoints grouped here so SetupRoutes stays focused on
	// high-level API composition instead of per-path wiring noise.
	// Public, non-sensitive: the SPA language switcher reads the configured
	// language codes (app.language) from here.
	router.Get("/languages", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"languages": config.Get().AppLanguages()})
	})
	router.Get("/qr", VietQrGen)
	router.Get("/banklist/:bankCode", BankListFunc)
	router.Get("/keys", GenHexStringKeyFunc)
	router.Post("/aes", AesFunc)
	router.Post("/aeshmac", AesHmacFunc)
	router.Post("/jsonstring", JsonStringFunc)
}

func registerOptionalTransportRoutes(router fiber.Router) {
	// No HTTP->Kafka publish route: the former unauthenticated
	// POST /publish/:topic let anyone forge bank IPN events (bank_ipn_topic,
	// ipn_napas, ipn_bidv) into the consumers, and nothing in-repo called it.
	// Producers publish from server-side code only.
	if !config.Get().MqttClient.Enable {
		return
	}
	router.Group("/mqtt").Post("/publish", PublishMessage)
}

// fiber:context-methods migrated
