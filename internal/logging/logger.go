// Package logging provides console-only logging capabilities for the Discord AI chatbot.
// It wraps the standard log package to provide consistent logging without file output.
package logging

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// LogLevel represents the severity level of log messages
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// parseLogLevel converts a string to LogLevel
func parseLogLevel(level string) LogLevel {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return DEBUG
	case "INFO":
		return INFO
	case "WARN", "WARNING":
		return WARN
	case "ERROR":
		return ERROR
	case "FATAL":
		return FATAL
	default:
		return INFO
	}
}

// Logger wraps the standard logger with console-only capabilities
type Logger struct {
	logger *log.Logger
	level  LogLevel
}

var (
	// Global logger instance
	globalLogger *Logger
)


// InitializeLogging sets up console-only logging with the specified level
func InitializeLogging(level string) error {
	// Create console-only logger with minimal flags for quiet output
	logger := log.New(os.Stdout, "", 0)

	// Set up the global logger
	globalLogger = &Logger{
		logger: logger,
		level:  parseLogLevel(level),
	}

	return nil
}


// shouldLog returns true if the message should be logged based on the current log level
func (l *Logger) shouldLog(level LogLevel) bool {
	return level >= l.level
}

// formatMessage formats a log message with minimal formatting for quiet output
func (l *Logger) formatMessage(level LogLevel, format string, args ...interface{}) string {
	message := fmt.Sprintf(format, args...)
	// Only show level prefix for ERROR and FATAL messages
	if level >= ERROR {
		return fmt.Sprintf("[%s] %s", level.String(), message)
	}
	return message
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	if l.shouldLog(DEBUG) {
		l.logger.Print(l.formatMessage(DEBUG, format, args...))
	}
}

// Info logs an info message
func (l *Logger) Info(format string, args ...interface{}) {
	if l.shouldLog(INFO) {
		l.logger.Print(l.formatMessage(INFO, format, args...))
	}
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	if l.shouldLog(WARN) {
		l.logger.Print(l.formatMessage(WARN, format, args...))
	}
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	if l.shouldLog(ERROR) {
		l.logger.Print(l.formatMessage(ERROR, format, args...))
	}
}

// Fatal logs a fatal message and exits the program
func (l *Logger) Fatal(format string, args ...interface{}) {
	if l.shouldLog(FATAL) {
		l.logger.Print(l.formatMessage(FATAL, format, args...))
		os.Exit(1)
	}
}

// Package-level functions that use the global logger

// Debug logs a debug message using the global logger
func Debug(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Debug(format, args...)
	}
}

// Info logs an info message using the global logger
func Info(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Info(format, args...)
	}
}

// Warn logs a warning message using the global logger
func Warn(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Warn(format, args...)
	}
}

// Error logs an error message using the global logger
func Error(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Error(format, args...)
	}
}

// Fatal logs a fatal message and exits the program using the global logger
func Fatal(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Fatal(format, args...)
	}
}

// Printf provides compatibility with the standard log package
func Printf(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Info(format, args...)
	} else {
		// Fallback to standard log if global logger is not initialized
		log.Printf(format, args...)
	}
}

// Fatalf provides compatibility with the standard log package
func Fatalf(format string, args ...interface{}) {
	if globalLogger != nil {
		globalLogger.Fatal(format, args...)
	} else {
		// Fallback to standard log if global logger is not initialized
		log.Fatalf(format, args...)
	}
}

// IsInitialized returns true if the global logger has been initialized
func IsInitialized() bool {
	return globalLogger != nil
}

// GetLogLevel returns the current log level
func GetLogLevel() LogLevel {
	if globalLogger != nil {
		return globalLogger.level
	}
	return INFO
}

// GetINFOLevel returns the INFO log level constant for external comparison
func GetINFOLevel() LogLevel {
	return INFO
}



// LogToFile is a no-op for backward compatibility (no file logging)
func LogToFile(format string, args ...interface{}) {
	// No-op: file logging has been removed
}

// LogExternalContentToFile is a no-op for backward compatibility (no file logging)
func LogExternalContentToFile(format string, args ...interface{}) {
	// No-op: file logging has been removed
}

// PrintAndLog prints to terminal only
func PrintAndLog(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	fmt.Print(message)
}

// PrintlnAndLog prints to terminal with newline only
func PrintlnAndLog(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	fmt.Println(message)
}

// PrintfAndLog prints formatted to terminal only
func PrintfAndLog(format string, args ...interface{}) {
	fmt.Printf(format, args...)
}
