package utils

import (
	"testing"
)

func TestGetEnvOrDefault(t *testing.T) {
	t.Run("Environment Variable Exists", func(t *testing.T) {
		key := "TEST_ENV_VAR"
		expectedValue := "test_value"
		t.Setenv(key, expectedValue)

		result := GetEnvOrDefault(key, "default_value")

		if result != expectedValue {
			t.Errorf("Expected %q, got %q", expectedValue, result)
		}
	})

	t.Run("Environment Variable Does Not Exist", func(t *testing.T) {
		key := "NON_EXISTENT_ENV_VAR"
		defaultValue := "default_value"

		result := GetEnvOrDefault(key, defaultValue)

		if result != defaultValue {
			t.Errorf("Expected %q, got %q", defaultValue, result)
		}
	})
}
