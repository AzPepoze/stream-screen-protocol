package logger

import (
	"fmt"
	"os"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
)

func log(level, color, format string, args ...interface{}) {
	now := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s %s[%s]%s %s\n", now, color, level, colorReset, msg)
}

func Info(format string, args ...interface{}) {
	log("INFO", colorGreen, format, args...)
}

func Warn(format string, args ...interface{}) {
	log("WARN", colorYellow, format, args...)
}

func Error(format string, args ...interface{}) {
	log("ERROR", colorRed, format, args...)
}

func Debug(format string, args ...interface{}) {
	log("DEBUG", colorBlue, format, args...)
}

func Fatal(format string, args ...interface{}) {
	log("FATAL", colorRed, format, args...)
	os.Exit(1)
}
