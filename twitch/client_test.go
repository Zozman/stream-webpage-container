package twitch

import (
	"context"
	"sync"
	"testing"
)

// Test helper to set environment variables
func setTestEnvVars(t *testing.T, clientID, clientSecret string) {
	t.Setenv("TWITCH_CLIENT_ID", clientID)
	t.Setenv("TWITCH_CLIENT_SECRET", clientSecret)
}

// Test helper to clean up environment variables
func cleanupTestEnvVars(t *testing.T) {
	t.Setenv("TWITCH_CLIENT_ID", "")
	t.Setenv("TWITCH_CLIENT_SECRET", "")
}

// Reset the client singleton for testing
func resetClient() {
	client = nil
	clientOnce = sync.Once{}
}

func TestInitializeClient(t *testing.T) {
	t.Cleanup(func() {
		cleanupTestEnvVars(t)
		resetClient()
	})

	t.Run("Client With Valid Credentials", func(t *testing.T) {
		setTestEnvVars(t, "test_client_id", "test_client_secret")

		ctx := context.Background()
		client, err := initializeClient(ctx)

		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if client == nil {
			t.Fatal("Expected client to be non-nil")
		}
	})

	t.Run("Client With Missing Client ID", func(t *testing.T) {
		cleanupTestEnvVars(t)
		setTestEnvVars(t, "", "test_client_secret")

		ctx := context.Background()
		_, err := initializeClient(ctx)

		if err == nil {
			t.Fatal("Expected error when client ID is missing, got nil")
		}
	})

	t.Run("Client With Missing Client Secret", func(t *testing.T) {
		cleanupTestEnvVars(t)
		setTestEnvVars(t, "test_client_id", "")

		ctx := context.Background()
		_, err := initializeClient(ctx)

		if err == nil {
			t.Fatal("Expected error when client secret is missing, got nil")
		}
	})
}

func TestGetClient(t *testing.T) {
	t.Cleanup(func() {
		cleanupTestEnvVars(t)
		resetClient()
	})

	t.Run("Get Client After Initialization", func(t *testing.T) {
		setTestEnvVars(t, "test_client_id", "test_client_secret")

		ctx := context.Background()
		client1 := GetClient(ctx)

		if client1 == nil {
			t.Fatal("Expected client to be non-nil after initialization")
		}

		client2 := GetClient(ctx)
		if client1 != client2 {
			t.Fatal("Expected GetClient to return the same instance on subsequent calls")
		}
	})
}
