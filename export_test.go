package powerslog

// ResetColdStart forgets the invocation that was recorded as the cold start.
func ResetColdStart() {
	coldStartRequestID.Store(nil)
}
