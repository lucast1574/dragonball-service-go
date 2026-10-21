package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dragonball-service/internal/characters"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	client, err := mongo.Connect(options.Client().ApplyURI(env("MONGODB_URI", "mongodb://localhost:27017")).SetServerSelectionTimeout(3 * time.Second).SetRetryWrites(false))
	if err != nil {
		return errors.New("invalid MONGODB_URI")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = client.Disconnect(ctx)
	}()
	handler, err := characters.NewHandler(
		env("DRAGONBALL_API_URL", "https://dragonball-api.com/api"),
		characters.NewMongoSaver(client.Database("dragonball").Collection("characters")),
	)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = client.Ping(ctx, nil)
	cancel()
	if err != nil {
		return errors.New("MongoDB is unavailable")
	}

	mux := http.NewServeMux()
	mux.Handle("POST /characters", handler)
	server := &http.Server{
		Addr: env("HTTP_ADDR", ":8080"), Handler: mux,
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	stop, cancelStop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelStop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	slog.Info("server starting", "address", server.Addr)
	select {
	case err := <-done:
		return err
	case <-stop.Done():
	}
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return err
	}
	err = <-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
