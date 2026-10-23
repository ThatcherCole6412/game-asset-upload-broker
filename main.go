package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	bucket := stableBucketName(apiKey)
	storage := newInfraiStorage(apiKey)
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), 20*time.Second)
	if err := storage.CreateBucket(setupCtx, bucket); err != nil {
		cancelSetup()
		log.Fatalf("create asset bucket: %v", err)
	}
	cancelSetup()

	server := &http.Server{
		Addr:              envOr("ADDR", ":8080"),
		Handler:           uploadBroker{bucket: bucket, signer: storage},
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("asset upload broker listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
