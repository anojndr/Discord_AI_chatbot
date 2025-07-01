// Package logging provides compatibility functions to replace standard log package usage
package logging

import (
	"log"
	"os"
)

// ReplaceStandardLogger replaces the standard log package output with our logging system
func ReplaceStandardLogger() {
	if globalLogger != nil {
		// Set the standard log output to our logger
		log.SetOutput(globalLogger.logger.Writer())
		log.SetFlags(0) // Remove flags since our logger handles them
	}
}

// LogError logs an error message - wrapper for backwards compatibility
func LogError(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Error(format, args...)
	} else {
		log.Printf("[ERROR] "+format, args...)
	}
}

// LogInfo logs an info message - wrapper for backwards compatibility
func LogInfo(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Info(format, args...)
	} else {
		log.Printf("[INFO] "+format, args...)
	}
}

// LogWarn logs a warning message - wrapper for backwards compatibility
func LogWarn(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Warn(format, args...)
	} else {
		log.Printf("[WARN] "+format, args...)
	}
}

// LogDebug logs a debug message - wrapper for backwards compatibility
func LogDebug(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Debug(format, args...)
	} else {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// LogFatal logs a fatal message and exits - wrapper for backwards compatibility
func LogFatal(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Fatal(format, args...)
	} else {
		log.Fatalf("[FATAL] "+format, args...)
	}
}

// GetWriter returns the writer for the logger, useful for redirecting other outputs
func GetWriter() *os.File {
	if globalLogger != nil && globalLogger.logger != nil {
		// Try to get the underlying writer
		return os.Stdout // fallback to stdout
	}
	return os.Stdout
}
