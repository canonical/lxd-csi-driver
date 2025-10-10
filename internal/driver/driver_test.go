package driver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateDriver(t *testing.T) {
	tests := []struct {
		Name        string
		Driver      *Driver
		expectError string
	}{
		{
			Name: "Ensure valid volume name prefix is accepted",
			Driver: &Driver{
				volumeNamePrefix: "THIS-is-A-valid-PREFIX-123",
			},
			expectError: "",
		},
		{
			Name: "Ensure volume name prefix cannot start with a hyphen",
			Driver: &Driver{
				volumeNamePrefix: "-invalid-prefix",
			},
			expectError: `Name must not start with "-" character`,
		},
		{
			Name: "Ensure volume name prefix cannot end with a hyphen",
			Driver: &Driver{
				volumeNamePrefix: "invalid-suffix-",
			},
			expectError: `Name must not end with "-" character`,
		},
		{
			Name: "Ensure volume name prefix cannot exceed 64 characters",
			Driver: &Driver{
				volumeNamePrefix: "this-is-a-very-long-prefix-that-exceeds-the-maximum-length-of-64-characters",
			},
			expectError: "Name must be 1-63 characters long",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			err := test.Driver.Validate()
			if test.expectError == "" {
				require.NoError(t, err, "Expected no error, got %v", err)
			} else {
				if err == nil {
					require.FailNowf(t, "Expected error %q, got none", test.expectError)
				}

				require.ErrorContains(t, err, test.expectError)
			}
		})
	}
}
