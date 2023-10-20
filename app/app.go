package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/megadata-dev/go-snmp-olt-c320/config"
	"github.com/megadata-dev/go-snmp-olt-c320/internal/handler"
	"github.com/megadata-dev/go-snmp-olt-c320/internal/repository"
	"github.com/megadata-dev/go-snmp-olt-c320/internal/usecase"
	"github.com/megadata-dev/go-snmp-olt-c320/pkg/redis"
	"github.com/megadata-dev/go-snmp-olt-c320/pkg/snmp"
	"github.com/megadata-dev/go-snmp-olt-c320/pkg/utils"
	rds "github.com/redis/go-redis/v9"
	"log"
	"net/http"
	"os"
	"time"
)

type App struct {
	router http.Handler
}

func New() *App {
	return &App{}
}

func (a *App) Start(ctx context.Context) error {
	configPath := utils.GetConfigPath(os.Getenv("config"))
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	redisClient := redis.NewRedisClient(cfg)
	defer func(redisClient *rds.Client) {
		err := redisClient.Close()
		if err != nil {
			log.Printf("Failed to close Redis client: %v", err)
		}
	}(redisClient)

	snmpConn, err := snmp.SetupSnmpConnection(cfg)
	if err != nil {
		return fmt.Errorf("failed to set up SNMP connection: %w", err)
	}

	defer func() {
		if err := snmpConn.Conn.Close(); err != nil {
			log.Printf("Failed to close SNMP connection: %v", err)
		}
	}()

	// Initialize repository
	snmpRepo := repository.NewPonRepository(snmpConn)
	redisRepo := repository.NewOnuRedisRepo(redisClient)

	// Initialize usecase
	onuUsecase := usecase.NewOnuUsecase(snmpRepo, redisRepo, cfg)

	// Initialize handler
	onuHandler := handler.NewOnuHandler(onuUsecase)

	// Initialize router
	a.router = loadRoutes(onuHandler)

	fmt.Println("Server Successfully Running")

	// Start server
	addr := "8081"
	server := &http.Server{
		Addr:    ":" + addr,
		Handler: a.router,
	}

	ch := make(chan error, 1)

	go func() {
		err = server.ListenAndServe()
		if err != nil && !errors.Is(http.ErrServerClosed, err) {
			ch <- fmt.Errorf("failed to start server: %v", err)
		}
		close(ch)
	}()

	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		if err := server.Shutdown(timeoutCtx); err != nil {
			log.Printf("Failed to gracefully shut down the server: %v", err)
		}
	}

	return nil
}
