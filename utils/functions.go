package utils

import (
	"os"
)

// Function gets the value of an environmental variable and returns a default value if the variable is not set.
func GetEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
