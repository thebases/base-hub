package cmd

// File overview:
// - Responsibility: coordinates server bootstrap, middleware wiring, infrastructure startup, and graceful shutdown for the backend process.
// - Key symbols: `configPath`, `forceConfigSync`, `forceMasterDataImport`, `resolvedUserFEStaticAssets`.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/monitoring"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/routes"
	cronservice "git.thebase.vn/base_hub/internal/services/cron"
	"git.thebase.vn/base_hub/internal/services/log"
	fiberlog "git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/services/mqtt"
	busroutes "git.thebase.vn/base_hub/modules/base-bus/routes"
	workflowservice "git.thebase.vn/base_hub/modules/base-workflow/service"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/idempotency"
	fiberlogger "github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/spf13/cobra"
	"gopkg.in/natefinch/lumberjack.v2"
)

var configPath string
var forceConfigSync bool
var forceMasterDataImport bool
var resolvedUserFEStaticAssets staticAssetSource

const (
	requestDBNameCookie   = "basehub_db_name"
	requestDBLockedCookie = "basehub_db_locked"
)

/* ========= log levels using builtin log ========= */

// initFiberLogger initializes the fiber logger handled by this package.
func initFiberLogger() io.Writer {
	// lumberjack for Fiber request logs
	reqFile := &lumberjack.Logger{
		Filename:   config.Get().Logging.ReqPath,
		MaxSize:    100, // MB
		MaxBackups: 10,
		MaxAge:     14, // days
		Compress:   true,
	}
	return io.MultiWriter(os.Stdout, reqFile)
}

// ========= APP ==========

var serverCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Base Hub server",
	RunE:  runServerCommand,
}

func runServerCommand(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	cmd.Flags().Parse(args)
	config.LoadConfig(configPath)

	if !config.Get().PostgresqlDb.Enable {
		log.Errorf("postgresql.enable must be true")
		return fmt.Errorf("postgresql.enable must be true")
	}
	// Stored credentials are encrypted under a key derived from app.encryptKey; without it they
	// can be neither written nor read, so refuse to serve rather than fail per request.
	if strings.TrimSpace(config.Get().App.EncryptKey) == "" {
		log.Errorf("app.encryptKey must be set in the config file")
		return fmt.Errorf("app.encryptKey must be set in the config file: it encrypts stored credentials")
	}

	dbInstances, cleanupDBs, err := initConfiguredDatabases(ctx)
	if err != nil {
		log.Errorf("failed to init PostgreSQL DB: %v", err)
		return fmt.Errorf("failed to init PostgreSQL DB: %w", err)
	}
	// Stop steps run newest first (the old defer order) under one budget; see
	// shutdownStack for the overrun and panic behaviour.
	var stack shutdownStack
	defer func() { stack.Run(context.Background(), serverShutdownTimeout) }()
	stack.AddFunc("databases", cleanupDBs.Close)
	stack.Add("lazy module catalog sync", stopLazyCatalogSyncs)

	shutdownModels := initRuntimeModels()
	stack.AddFunc("runtime models", shutdownModels)

	if err := initializeServerRuntime(); err != nil {
		return err
	}
	stack.AddFunc("orm model worker", orm.ShutdownSharedModelWorker)
	stack.AddFunc("outbound delivery worker", workflowservice.ShutdownOutboundDeliveryWorker)

	log.Init(config.Get().Logging.Path)
	log.SetLevel(log.Level(config.Get().Logging.Level))
	reqWriter := initFiberLogger()
	log.Infof("✅ Connected to PostgreSQL databases: %s", strings.Join(orm.RegisteredDBNames(), ", "))
	log.Infof("runtime model registry initialized with %d models", len(orm.RegisteredModels()))
	log.Infof("runtime model registry auto-migrated and synced to sys_model tables")
	stack.AddFunc("request log", func() {
		if closer, ok := reqWriter.(*lumberjack.Logger); ok {
			_ = closer.Close()
		}
	})

	if err := bootstrapServerDatabases(ctx, dbInstances); err != nil {
		return err
	}

	app, err := buildServerApp(ctx, reqWriter)
	if err != nil {
		return err
	}

	kafkaProducer := startKafkaProducerIfEnabled()
	if kafkaProducer != nil {
		stack.AddFunc("kafka producer", func() { _ = kafkaProducer.Close() })
	}

	consumerCancel, consumerWG := startKafkaConsumersIfEnabled(ctx)
	stack.AddFunc("kafka consumers", func() {
		if consumerCancel != nil {
			consumerCancel()
		}
		if consumerWG != nil {
			consumerWG.Wait()
		}
	})

	return serveFiberApp(ctx, app, &stack)
}

func buildServerApp(ctx context.Context, reqWriter io.Writer) (*fiber.App, error) {
	app := fiber.New(serverFiberConfig(config.Get().Server))
	app.Use(routes.RecoverMiddleware)
	monitoring.RegisterMetrics()
	app.Use(monitoring.Middleware())

	loggerConfig := fiberlogger.Config{
		Format:        "${time} [" + config.Get().AppName() + "]: [Trace] " + sanitizeAccessLogPattern(config.Get().Logging.Pattern) + "\n",
		TimeFormat:    "2006/01/02 15:04:05.000000",
		DisableColors: false,
	}
	if config.Get().Logging.UseFile {
		loggerConfig.Stream = reqWriter
	}
	app.Use(fiberlogger.New(loggerConfig))
	fiberlog.SetLevel(fiberlog.Level(config.Get().Logging.Level))

	app.Use(cors.New(cors.Config{
		AllowOrigins: splitCSVValues(config.Get().CORS.AllowOrigins),
		AllowMethods: mergeCSVValues(
			config.Get().CORS.AllowMethods,
			fiber.MethodOptions,
		),
		AllowHeaders: mergeCSVValues(
			config.Get().CORS.AllowHeaders,
			fiber.HeaderAuthorization,
			"X-Database-Name",
			"X-Idempotency-Key",
		),
		ExposeHeaders:    splitCSVValues(config.Get().CORS.ExposeHeaders),
		AllowCredentials: config.Get().CORS.AllowCredentials,
	}))
	app.Use(bindRequestDatabase)
	// After bindRequestDatabase so the scoped key carries the request tenant.
	app.Use(scopeIdempotencyKey)
	app.Use(idempotency.New(idempotency.Config{
		Next:                shouldSkipIdempotencyMiddleware,
		Lifetime:            30 * time.Minute,
		KeyHeader:           idempotencyKeyHeader,
		KeyHeaderValidate:   validateIdempotencyKey,
		KeepResponseHeaders: nil,
		Lock:                nil,
		Storage:             resolveIdempotencyStorage(ctx),
	}))
	if config.Get().MqttClient.Enable {
		if err := mqtt.InitMQTT(); err != nil {
			return nil, err
		}
	}
	routes.SetupRoutes(app)
	routes.RegisterPWAManifestRoute(app)
	app.Get("/:module/static/*", serveModuleStaticResource)
	app.Use("/md", applyNoCacheMarkdownHeaders)
	if err := configureStaticAssetRoutes(app); err != nil {
		return nil, err
	}
	return app, nil
}

const (
	defaultServerReadTimeout = 120 * time.Second
	defaultServerIdleTimeout = 120 * time.Second
)

// serverFiberConfig builds the fiber config from `server.*`. X-Forwarded-For
// is read only when the TCP peer is a listed trusted proxy, and then the
// client IP is the rightmost untrusted hop (EnableIPValidation), so a client
// cannot pick its own IP by sending the header. No WriteTimeout: SSE streams
// and large downloads outlive any fixed bound.
func serverFiberConfig(server config.ServerConfig) fiber.Config {
	proxies := make([]string, 0, len(server.TrustedProxies))
	for _, proxy := range server.TrustedProxies {
		if proxy = strings.TrimSpace(proxy); proxy != "" {
			proxies = append(proxies, proxy)
		}
	}
	cfg := fiber.Config{
		AppName:      config.Get().AppName(),
		JSONEncoder:  base.JSONMarshal,
		ErrorHandler: routes.APIErrorHandler,
		ReadTimeout:  positiveSecondsOr(server.ReadTimeoutS, defaultServerReadTimeout),
		IdleTimeout:  positiveSecondsOr(server.IdleTimeoutS, defaultServerIdleTimeout),
	}
	if len(proxies) > 0 {
		cfg.TrustProxy = true
		cfg.TrustProxyConfig = fiber.TrustProxyConfig{Proxies: proxies}
		cfg.ProxyHeader = fiber.HeaderXForwardedFor
		cfg.EnableIPValidation = true
	}
	return cfg
}

func positiveSecondsOr(seconds int, fallback time.Duration) time.Duration {
	if seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func serveFiberApp(ctx context.Context, app *fiber.App, stack *shutdownStack) error {
	cronservice.Start(ctx)
	stack.AddFunc("cron scheduler", cronservice.Shutdown)

	serverPort := config.Get().Server.Port
	log.Infof("IPN Service running on port: %d\n", serverPort)
	errCh := make(chan error, 1)
	go func() {
		if err := app.Listen(fmt.Sprintf(":%d", serverPort)); err != nil {
			errCh <- err
		}
	}()

	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	select {
	case err := <-errCh:
		log.Errorf("Failed to start server: %v", err)
		return fmt.Errorf("server listen on port %d: %w", serverPort, err)
	case <-sigCtx.Done():
	}

	log.Infof("Graceful shutting down ...")
	// fasthttp's graceful shutdown does not track hijacked WebSocket
	// connections, so /bus/ws goroutines survive app.ShutdownWithContext
	// and keep their acquired Postgres pool connection checked out. Signal
	// them now so they release it before the pool is closed later in the
	// shutdown chain — otherwise pgxpool.Pool.Close() blocks forever.
	busroutes.Shutdown()
	ctx1, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := app.ShutdownWithContext(ctx1); err != nil {
		log.Errorf("Failed to shutdown server gracefully: %v", err)
	} else {
		log.Infof("Server shutdown complete.")
	}
	return nil
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().StringVarP(&configPath, "config", "c", "./build/config.yaml", "Path to config file")
	// serverCmd.Flags().BoolVar(&forceConfigSync, "f", false, "Force override sys.config_param from the loaded config file")
	serverCmd.Flags().BoolVar(&forceConfigSync, "force-config", false, "Force override sys.config_param from the loaded config file")
	serverCmd.Flags().BoolVar(&forceMasterDataImport, "force-master-data", false, "Force the startup master-data checkpoint to run once by setting sys.config_param init_import")

	// The control-plane provisioning logic lives in modules/database and reaches
	// back into this binary-composition layer for module-catalog sync, master-data
	// seed bootstrap, and the --force-config flag value — all of which depend on
	// embedded build assets and CLI flag state owned here, not by the module.
	orm.RegisterModuleCatalogSync(syncModuleCatalog)
	orm.RegisterProvisionSeedBootstrap(func(ctx context.Context, forceImport bool, enabledModules []string) (*orm.ProvisionSeedResult, error) {
		report, source, _, err := bootstrapModuleDataIfEnabled(ctx, forceImport, false, enabledModules)
		if err != nil {
			return nil, err
		}
		if report == nil {
			return &orm.ProvisionSeedResult{Source: source}, nil
		}
		return &orm.ProvisionSeedResult{
			Imported: report.Imported,
			Source:   source,
			Created:  report.ImportReport.Created,
			Updated:  report.ImportReport.Updated,
			Skipped:  report.ImportReport.Skipped,
		}, nil
	})
	orm.RegisterForceConfigSyncFlag(func() bool { return forceConfigSync })
}
