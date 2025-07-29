package providers

import "fmt"

// PrematureStreamFinishError is a custom error type for premature stream finishes
type PrematureStreamFinishError struct {
	FinishReason string
}

func (e *PrematureStreamFinishError) Error() string {
	return fmt.Sprintf("stream finished prematurely with reason: %s", e.FinishReason)
}
