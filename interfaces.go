package main

// ObservabilityLogger interface for observability integration
type ObservabilityLogger interface {
	WriteLogEntry(entry []byte) error
	Close() error
}

// NoOpObservabilityLogger is a no-op implementation
type NoOpObservabilityLogger struct{}

func (n *NoOpObservabilityLogger) WriteLogEntry(entry []byte) error {
	return nil
}

func (n *NoOpObservabilityLogger) Close() error {
	return nil
}
