package keenetic

import "testing"

func TestFirstRCIErrorDetectsNestedError(t *testing.T) {
	responses := []map[string]any{
		{
			"interface": map[string]any{
				"OpkgTun3": map[string]any{
					"description": map[string]any{
						"status": []any{
							map[string]any{
								"status":  "error",
								"message": "something failed",
							},
						},
					},
				},
			},
		},
	}

	err := firstRCIError(responses)
	if err == nil || err.Error() != "something failed" {
		t.Fatalf("expected nested RCI error, got %v", err)
	}
}

func TestFirstRCIErrorReturnsNilForMessagesOnly(t *testing.T) {
	responses := []map[string]any{
		{
			"interface": map[string]any{
				"status": []any{
					map[string]any{
						"status":  "message",
						"message": "interface created",
					},
				},
			},
		},
	}

	if err := firstRCIError(responses); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
