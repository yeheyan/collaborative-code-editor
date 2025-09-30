// cmd/editor-service/main.go
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"collaborative-editor/internal/database"
	"collaborative-editor/internal/editor"
)

func main() {
	// Command line flags
	var (
		port = flag.String("port", "8080", "Server port")
		env  = flag.String("env", "dev", "Environment (dev, prod)")

		// Database flags
		dbHost = flag.String("db-host", getEnvOrDefault("DB_HOST", "localhost"), "Database host")
		dbPort = flag.String("db-port", getEnvOrDefault("DB_PORT", "5432"), "Database port")
		dbUser = flag.String("db-user", getEnvOrDefault("DB_USER", "postgres"), "Database user")
		dbPass = flag.String("db-pass", getEnvOrDefault("DB_PASSWORD", "postgres"), "Database password")
		dbName = flag.String("db-name", getEnvOrDefault("DB_NAME", "collaborative_editor"), "Database name")

		// Feature flags
		useDB = flag.Bool("use-db", true, "Enable database persistence")
	)
	flag.Parse()

	log.Printf("Starting editor service on port %s (env: %s)", *port, *env)

	// Initialize database connection (optional)
	var db *database.DB
	if *useDB {
		var err error
		db, err = database.NewDB(*dbHost, *dbPort, *dbUser, *dbPass, *dbName)
		if err != nil {
			log.Printf("Warning: Could not connect to database: %v", err)
			log.Println("Running without persistence - documents will be lost on restart")
			// Continue without database - in-memory only mode
		} else {
			defer db.Close()
			log.Println("Database connection established")
		}
	} else {
		log.Println("Running in memory-only mode (no persistence)")
	}

	// Create service configuration
	config := &editor.Config{
		MaxMessageSize:   512 * 1024,
		WriteTimeout:     10,
		ReadTimeout:      60,
		PingInterval:     30,
		MaxClients:       1000,
		AutoSaveInterval: 30, // seconds
	}

	// Create service with database (can be nil)
	service := editor.NewService(config, db)

	// Start the service
	if err := service.Start(); err != nil {
		log.Fatalf("Failed to start service: %v", err)
	}

	// Create HTTP server
	mux := http.NewServeMux()

	// WebSocket endpoint
	mux.HandleFunc("/ws", service.HandleWebSocket)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Metrics endpoint
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics := service.GetMetrics()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metrics)
	})

	// Static files (in development mode)
	if *env == "dev" {
		// Serve the entire public directory
		fs := http.FileServer(http.Dir("../frontend/public"))
		mux.Handle("/", fs)
		log.Println("Serving static files from ../frontend/public")
	}

	// Start HTTP server
	server := &http.Server{
		Addr:    ":" + *port,
		Handler: mux,
	}

	// Handle graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down server...")
		service.Shutdown()
		server.Close()
	}()

	log.Printf("Server running at http://localhost:%s", *port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
