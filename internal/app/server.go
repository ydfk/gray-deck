package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"mihomo-manager/internal/httpapi"
	"mihomo-manager/internal/manager"
)

type Server struct {
	httpServer *http.Server
	service    *manager.Service
}

func NewServer(cfg Config) (*Server, error) {
	service, err := manager.New(manager.Config{
		DataDir:           cfg.DataDir,
		CoreTargetOS:      cfg.CoreTargetOS,
		CoreTargetArch:    cfg.CoreTargetArch,
		ControllerAddr:    cfg.ControllerAddr,
		RuntimeMixedPort:  cfg.RuntimeMixedPort,
		RuntimeSocksPort:  cfg.RuntimeSocksPort,
		RuntimeRedirPort:  cfg.RuntimeRedirPort,
		RuntimeTProxyPort: cfg.RuntimeTProxyPort,
		RuntimeSecret:     cfg.RuntimeSecret,
		BaseConfigPath:    cfg.BaseConfigPath,
		AppConfigPath:     cfg.AppConfigPath,
		WebRoot:           cfg.WebRoot,
		DeploymentMode:    cfg.DeploymentMode,
	})
	if err != nil {
		return nil, err
	}

	handler := httpapi.NewRouter(service)

	return &Server{
		service: service,
		httpServer: &http.Server{
			Addr:    cfg.ListenAddress,
			Handler: handler,
		},
	}, nil
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	fmt.Printf("managerd listening on %s\n", s.httpServer.Addr)
	done := make(chan error, 1)
	go func() { done <- s.httpServer.ListenAndServe() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(s.httpServer.Shutdown(shutdown), s.service.Shutdown())
	}
}
