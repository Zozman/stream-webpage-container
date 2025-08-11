package twitch

import (
	"context"
	"errors"
	"sync"

	"github.com/nicklaw5/helix/v2"

	"github.com/Zozman/stream-website/utils"
)

var (
	// Twitch API client instance
	client *helix.Client
	// Once object to ensure the client is initialized only once
	clientOnce sync.Once
)

// Function to return the Twitch API client and initialize it if not already done
func GetClient(ctx context.Context) *helix.Client {
	clientOnce.Do(func() {
		var err error
		client, err = initializeClient(ctx)
		if err != nil {
			panic("Failed to create Twitch client: " + err.Error())
		}
	})
	return client
}

// Create a Twitch API client using provided credentials
func initializeClient(ctx context.Context) (*helix.Client, error) {
	// Get Twitch client ID and access token from environment variables
	clientID := utils.GetEnvOrDefault("TWITCH_CLIENT_ID", "")
	clientSecret := utils.GetEnvOrDefault("TWITCH_CLIENT_SECRET", "")

	if clientID == "" || clientSecret == "" {
		return nil, errors.New("Twitch client ID and access token must be set")
	}

	// Create a new helix client with the provided credentials
	client, err := helix.NewClientWithContext(ctx, &helix.Options{
		ClientID:     clientID,
		ClientSecret: clientSecret,
	})
	if err != nil {
		return nil, err
	}

	// Setup app access token for the client
	appAccessTokenResponse, err := client.RequestAppAccessToken([]string{""})
	if err != nil {
		return nil, err
	}
	client.SetAppAccessToken(appAccessTokenResponse.Data.AccessToken)

	return client, nil
}
